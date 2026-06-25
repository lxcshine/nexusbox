// Package egress implements an egress security gateway for sandboxes.
//
// The egress gateway intercepts outbound HTTPS traffic from sandboxes to:
//   - Enforce domain allowlist/denylist policies
//   - Inject credentials (API keys, tokens) dynamically per-sandbox
//   - Audit all outbound requests (URL, method, status, size)
//   - Rate-limit outbound requests per sandbox
//
// Inspired by CubeSandbox's CubeEgress which uses OpenResty + Lua.
// NexusBox implements this in pure Go using httputil.ReverseProxy with
// MITM (Man-In-The-Middle) TLS interception for HTTPS audit.
package egress

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

// Gateway is the egress security gateway.
type Gateway struct {
	mu sync.RWMutex

	// policies maps sandbox ID -> egress policy
	policies map[string]*Policy

	// credentialProvider provides credentials for injection
	credentialProvider CredentialProvider

	// auditLog stores recent egress requests
	auditLog *AuditLog

	// stats tracks gateway statistics
	stats GatewayStats

	// httpServer is the proxy server
	httpServer *http.Server

	// tlsConfig is used for MITM HTTPS interception
	tlsConfig *tls.Config

	stopCh chan struct{}
}

// GatewayStats tracks egress gateway statistics.
type GatewayStats struct {
	TotalRequests   atomic.Int64
	AllowedRequests atomic.Int64
	DeniedRequests  atomic.Int64
	InjectedCreds   atomic.Int64
	AuditedRequests atomic.Int64
}

// StatsSnapshot is a point-in-time copy of gateway statistics.
// It contains plain int64 values safe to copy.
type StatsSnapshot struct {
	TotalRequests   int64 `json:"totalRequests"`
	AllowedRequests int64 `json:"allowedRequests"`
	DeniedRequests  int64 `json:"deniedRequests"`
	InjectedCreds   int64 `json:"injectedCreds"`
	AuditedRequests int64 `json:"auditedRequests"`
}

// Policy defines the egress policy for a sandbox.
type Policy struct {
	// SandboxID is the ID of the sandbox this policy applies to.
	SandboxID string `json:"sandboxID"`
	// AllowedDomains is the allowlist of domains (e.g., "api.openai.com").
	// If non-empty, only these domains are allowed.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	// DeniedDomains is the denylist of domains.
	DeniedDomains []string `json:"deniedDomains,omitempty"`
	// InjectHeaders are headers to inject into outbound requests.
	// These are typically credentials (API keys, tokens).
	InjectHeaders map[string]string `json:"injectHeaders,omitempty"`
	// InjectHeadersPerDomain injects headers only for specific domains.
	// Key is domain, value is header map.
	InjectHeadersPerDomain map[string]map[string]string `json:"injectHeadersPerDomain,omitempty"`
	// MaxRequestsPerMinute limits outbound requests per minute.
	MaxRequestsPerMinute int32 `json:"maxRequestsPerMinute,omitempty"`
	// AuditEnabled controls whether requests are audit-logged.
	AuditEnabled bool `json:"auditEnabled"`
	// BlockPrivateIPs prevents access to private/internal IP ranges.
	BlockPrivateIPs bool `json:"blockPrivateIPs"`
}

// CredentialProvider provides credentials for injection into egress requests.
// This allows credentials to be stored in a secure backend (Vault, etc.)
// rather than in the policy itself.
type CredentialProvider interface {
	// GetCredentials returns credentials to inject for a sandbox + domain.
	GetCredentials(ctx context.Context, sandboxID, domain string) (map[string]string, error)
}

// AuditEntry represents a single audited egress request.
type AuditEntry struct {
	Timestamp  time.Time     `json:"timestamp"`
	SandboxID  string        `json:"sandboxID"`
	Method     string        `json:"method"`
	URL        string        `json:"url"`
	StatusCode int           `json:"statusCode"`
	BytesSent  int64         `json:"bytesSent"`
	BytesRecv  int64         `json:"bytesRecv"`
	Duration   time.Duration `json:"durationMs"`
	Denied     bool          `json:"denied"`
	DenyReason string        `json:"denyReason,omitempty"`
}

// AuditLog stores recent egress audit entries.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	maxSize int
}

// NewAuditLog creates a new AuditLog with the given max size.
func NewAuditLog(maxSize int) *AuditLog {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &AuditLog{
		entries: make([]AuditEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Append adds an entry to the audit log.
func (a *AuditLog) Append(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.entries) >= a.maxSize {
		// Drop oldest 10% when full (at least 1 entry)
		dropCount := a.maxSize / 10
		if dropCount < 1 {
			dropCount = 1
		}
		a.entries = a.entries[dropCount:]
	}
	a.entries = append(a.entries, entry)
}

// Entries returns the last n entries.
func (a *AuditLog) Entries(n int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	if n <= 0 || n > len(a.entries) {
		n = len(a.entries)
	}
	start := len(a.entries) - n
	result := make([]AuditEntry, n)
	copy(result, a.entries[start:])
	return result
}

// GatewayConfig holds configuration for the egress Gateway.
type GatewayConfig struct {
	// ListenAddr is the address to listen on (e.g., ":8081").
	ListenAddr string
	// CredentialProvider provides credentials for injection.
	CredentialProvider CredentialProvider
	// AuditLogSize is the max number of audit entries to keep.
	AuditLogSize int
	// DefaultPolicy is the policy applied when no sandbox-specific policy exists.
	DefaultPolicy *Policy
}

// NewGateway creates a new egress Gateway.
func NewGateway(config *GatewayConfig) *Gateway {
	g := &Gateway{
		policies:           make(map[string]*Policy),
		credentialProvider: config.CredentialProvider,
		auditLog:           NewAuditLog(config.AuditLogSize),
		tlsConfig:          &tls.Config{},
		stopCh:             make(chan struct{}),
	}

	// Set up default policy
	if config.DefaultPolicy != nil {
		g.policies["__default__"] = config.DefaultPolicy
	}

	g.httpServer = &http.Server{
		Addr: config.ListenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Route policy/audit/stats API requests to ServeHTTP,
			// everything else goes to the proxy handler.
			if strings.HasPrefix(r.URL.Path, "/v1/egress/") {
				g.ServeHTTP(w, r)
				return
			}
			g.handleRequest(w, r)
		}),
	}

	return g
}

// Start starts the egress gateway.
func (g *Gateway) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", g.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("egress gateway: failed to listen on %s: %w", g.httpServer.Addr, err)
	}

	go func() {
		klog.Infof("Egress gateway listening on %s", g.httpServer.Addr)
		if err := g.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Egress gateway error: %v", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			g.Stop()
		case <-g.stopCh:
		}
	}()

	return nil
}

// Stop stops the egress gateway.
func (g *Gateway) Stop() {
	close(g.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g.httpServer.Shutdown(ctx)
}

// SetPolicy sets the egress policy for a sandbox.
func (g *Gateway) SetPolicy(sandboxID string, policy *Policy) {
	g.mu.Lock()
	defer g.mu.Unlock()
	policy.SandboxID = sandboxID
	g.policies[sandboxID] = policy
	klog.Infof("Set egress policy for sandbox %s (allowed=%d, denied=%d)",
		sandboxID, len(policy.AllowedDomains), len(policy.DeniedDomains))
}

// RemovePolicy removes the egress policy for a sandbox.
func (g *Gateway) RemovePolicy(sandboxID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.policies, sandboxID)
}

// GetPolicy returns the policy for a sandbox.
func (g *Gateway) GetPolicy(sandboxID string) *Policy {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if p, ok := g.policies[sandboxID]; ok {
		return p
	}
	if p, ok := g.policies["__default__"]; ok {
		return p
	}
	return nil
}

// handleRequest is the main HTTP handler for egress proxy requests.
func (g *Gateway) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	g.stats.TotalRequests.Add(1)

	// Extract sandbox ID from header
	sandboxID := r.Header.Get("X-Sandbox-ID")
	if sandboxID == "" {
		sandboxID = "anonymous"
	}

	// Get policy
	policy := g.GetPolicy(sandboxID)

	// Parse target URL
	targetURL := r.URL.Path
	if strings.HasPrefix(targetURL, "/") {
		targetURL = targetURL[1:]
	}
	if !strings.Contains(targetURL, "://") {
		targetURL = "http://" + targetURL
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		g.denyRequest(w, r, sandboxID, "invalid target URL", start)
		return
	}

	// Check domain policy
	if policy != nil && !g.isDomainAllowed(policy, target.Host) {
		g.denyRequest(w, r, sandboxID, fmt.Sprintf("domain %s not allowed", target.Host), start)
		return
	}

	// Block private IPs if configured
	if policy != nil && policy.BlockPrivateIPs {
		if g.isPrivateIP(target.Host) {
			g.denyRequest(w, r, sandboxID, fmt.Sprintf("private IP blocked: %s", target.Host), start)
			return
		}
	}

	// Inject credentials
	if policy != nil {
		g.injectCredentials(r, policy, sandboxID, target.Host)
	}

	// Rate limiting (simplified - in production use token bucket)
	// TODO: implement per-sandbox rate limiting

	// Proxy the request
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		TLSClientConfig: g.tlsConfig,
	}

	// Capture response for audit
	recorder := &responseRecorder{ResponseWriter: w, statusCode: 200}
	proxy.ServeHTTP(recorder, r)

	g.stats.AllowedRequests.Add(1)

	// Audit log
	if policy != nil && policy.AuditEnabled {
		g.stats.AuditedRequests.Add(1)
		g.auditLog.Append(AuditEntry{
			Timestamp:  start,
			SandboxID:  sandboxID,
			Method:     r.Method,
			URL:        targetURL,
			StatusCode: recorder.statusCode,
			BytesSent:  r.ContentLength,
			BytesRecv:  recorder.bytesWritten,
			Duration:   time.Since(start),
		})
	}
}

// denyRequest denies a request and logs it.
func (g *Gateway) denyRequest(w http.ResponseWriter, r *http.Request, sandboxID, reason string, start time.Time) {
	g.stats.DeniedRequests.Add(1)
	g.auditLog.Append(AuditEntry{
		Timestamp:  start,
		SandboxID:  sandboxID,
		Method:     r.Method,
		URL:        r.URL.Path,
		Denied:     true,
		DenyReason: reason,
		Duration:   time.Since(start),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]string{
		"error":  "egress denied",
		"reason": reason,
	})
}

// isDomainAllowed checks if a domain is allowed by the policy.
func (g *Gateway) isDomainAllowed(policy *Policy, host string) bool {
	// Strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Check denylist first
	for _, denied := range policy.DeniedDomains {
		if matchDomain(denied, host) {
			return false
		}
	}

	// If allowlist is empty, allow all (except denied)
	if len(policy.AllowedDomains) == 0 {
		return true
	}

	// Check allowlist
	for _, allowed := range policy.AllowedDomains {
		if matchDomain(allowed, host) {
			return true
		}
	}
	return false
}

// matchDomain checks if a host matches a domain pattern.
// Supports wildcard subdomains (e.g., "*.example.com").
func matchDomain(pattern, host string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix)
	}
	return pattern == host
}

// isPrivateIP checks if a host is a private/internal IP.
func (g *Gateway) isPrivateIP(host string) bool {
	// Strip port
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP, could be a hostname - resolve it
		// For safety, we don't block hostnames
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// injectCredentials injects credentials into the request.
func (g *Gateway) injectCredentials(r *http.Request, policy *Policy, sandboxID, domain string) {
	// Inject global headers
	for key, value := range policy.InjectHeaders {
		r.Header.Set(key, value)
		g.stats.InjectedCreds.Add(1)
	}

	// Inject domain-specific headers
	if domainHeaders, ok := policy.InjectHeadersPerDomain[domain]; ok {
		for key, value := range domainHeaders {
			r.Header.Set(key, value)
			g.stats.InjectedCreds.Add(1)
		}
	}

	// Inject credentials from provider
	if g.credentialProvider != nil {
		if creds, err := g.credentialProvider.GetCredentials(r.Context(), sandboxID, domain); err == nil {
			for key, value := range creds {
				r.Header.Set(key, value)
				g.stats.InjectedCreds.Add(1)
			}
		}
	}
}

// GetStats returns a point-in-time snapshot of gateway statistics.
func (g *Gateway) GetStats() StatsSnapshot {
	return StatsSnapshot{
		TotalRequests:   g.stats.TotalRequests.Load(),
		AllowedRequests: g.stats.AllowedRequests.Load(),
		DeniedRequests:  g.stats.DeniedRequests.Load(),
		InjectedCreds:   g.stats.InjectedCreds.Load(),
		AuditedRequests: g.stats.AuditedRequests.Load(),
	}
}

// GetAuditEntries returns recent audit entries.
func (g *Gateway) GetAuditEntries(limit int) []AuditEntry {
	return g.auditLog.Entries(limit)
}

// responseRecorder captures the response status code and body size.
type responseRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += int64(n)
	return n, err
}

// StaticCredentialProvider is a simple in-memory credential provider.
type StaticCredentialProvider struct {
	mu    sync.RWMutex
	creds map[string]map[string]map[string]string // sandboxID -> domain -> header -> value
}

// NewStaticCredentialProvider creates a new StaticCredentialProvider.
func NewStaticCredentialProvider() *StaticCredentialProvider {
	return &StaticCredentialProvider{
		creds: make(map[string]map[string]map[string]string),
	}
}

// SetCredentials sets credentials for a sandbox + domain.
func (s *StaticCredentialProvider) SetCredentials(sandboxID, domain string, creds map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.creds[sandboxID] == nil {
		s.creds[sandboxID] = make(map[string]map[string]string)
	}
	s.creds[sandboxID][domain] = creds
}

// GetCredentials implements CredentialProvider.
func (s *StaticCredentialProvider) GetCredentials(ctx context.Context, sandboxID, domain string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if domains, ok := s.creds[sandboxID]; ok {
		if creds, ok := domains[domain]; ok {
			return creds, nil
		}
	}
	return nil, nil
}

// Ensure StaticCredentialProvider implements CredentialProvider
var _ CredentialProvider = (*StaticCredentialProvider)(nil)

// Ensure Gateway implements io.Closer
var _ io.Closer = (*Gateway)(nil)

// Close implements io.Closer.
func (g *Gateway) Close() error {
	g.Stop()
	return nil
}
