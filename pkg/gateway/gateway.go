package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/template"
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

// Gateway provides the unified REST API gateway for the NexusBox sandbox system.
// Inspired by agent-infra/sandbox's architecture, it exposes a single-entry API
// for shell, file, browser, code execution, and sandbox management.
type Gateway struct {
	httpServer *http.Server

	lifecycleManager *lifecycle.LifecycleManager
	runtimeManager   *runtime.RuntimeManager
	tenantManager    *tenant.TenantManager
	quotaManager     *quota.QuotaManager

	shellService    *ShellService
	fileService     *FileService
	browserService  *BrowserService
	codeService     *CodeService
	sandboxService  *SandboxService
	templateService *TemplateService
	e2bService      *E2BService

	// Observability
	metrics     *MetricsCollector
	auditLogger *AuditLogger

	mu     sync.RWMutex
	stopCh chan struct{}
}

// GatewayConfig holds configuration for the Gateway.
type GatewayConfig struct {
	Port             int
	LifecycleManager *lifecycle.LifecycleManager
	RuntimeManager   *runtime.RuntimeManager
	TenantManager    *tenant.TenantManager
	QuotaManager     *quota.QuotaManager
	Workspace        string
	// TemplateManager manages sandbox templates. If nil, a new one is created
	// and seeded with default templates.
	TemplateManager *template.Manager
}

// NewGateway creates a new Gateway instance.
func NewGateway(config *GatewayConfig) *Gateway {
	workspace := config.Workspace
	if workspace == "" {
		workspace = "/home/sandbox"
	}

	// Initialize template manager if not provided
	tmplMgr := config.TemplateManager
	if tmplMgr == nil {
		tmplMgr = template.NewManager()
	}

	g := &Gateway{
		lifecycleManager: config.LifecycleManager,
		runtimeManager:   config.RuntimeManager,
		tenantManager:    config.TenantManager,
		quotaManager:     config.QuotaManager,
		shellService:     NewShellService(config.RuntimeManager),
		fileService:      NewFileService(workspace),
		browserService:   NewBrowserService(),
		codeService:      NewCodeService(),
		sandboxService:   NewSandboxService(config.LifecycleManager, config.RuntimeManager),
		templateService:  NewTemplateService(tmplMgr),
		metrics:          NewMetricsCollector(),
		auditLogger:      NewAuditLogger(10000),
		stopCh:           make(chan struct{}),
	}

	// Initialize E2B compatibility service
	g.e2bService = NewE2BService(
		config.LifecycleManager,
		config.RuntimeManager,
		g.templateService,
		g.shellService,
		g.fileService,
		g.codeService,
	)

	mux := http.NewServeMux()
	g.registerRoutes(mux)

	g.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", config.Port),
		Handler:      recoveryMiddleware(corsMiddleware(authMiddleware(requestLogMiddleware(mux, g.metrics)))),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return g
}

// Start starts the gateway server.
func (g *Gateway) Start(ctx context.Context) error {
	// Probe port availability before starting the goroutine
	ln, err := net.Listen("tcp", g.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("gateway: failed to listen on %s: %w", g.httpServer.Addr, err)
	}

	go func() {
		klog.Infof("Gateway server listening on %s", g.httpServer.Addr)
		// Serve on the already-bound listener
		if err := g.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Gateway server error: %v", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			g.Shutdown()
		case <-g.stopCh:
			return
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the gateway.
func (g *Gateway) Shutdown() {
	close(g.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := g.httpServer.Shutdown(ctx); err != nil {
		klog.Errorf("Gateway shutdown error: %v", err)
	}
}

// registerRoutes registers all API routes following the /v1/ prefix convention.
// Service accessors for external consumers (MCP servers, SDK, etc.)

// ShellService returns the shell service instance.
func (g *Gateway) ShellService() *ShellService { return g.shellService }

// FileService returns the file service instance.
func (g *Gateway) FileService() *FileService { return g.fileService }

// BrowserService returns the browser service instance.
func (g *Gateway) BrowserService() *BrowserService { return g.browserService }

// CodeService returns the code service instance.
func (g *Gateway) CodeService() *CodeService { return g.codeService }

// TemplateService returns the template service instance.
func (g *Gateway) TemplateService() *TemplateService { return g.templateService }

// E2BService returns the E2B compatibility service instance.
func (g *Gateway) E2BService() *E2BService { return g.e2bService }

func (g *Gateway) registerRoutes(mux *http.ServeMux) {
	// Health & system
	mux.HandleFunc("/healthz", g.handleHealthz)
	mux.HandleFunc("/readyz", g.handleReadyz)
	mux.HandleFunc("/v1/", g.handleAPIIndex)
	mux.HandleFunc("/v1/system/env", g.handleSystemEnv)

	// Sandbox management API
	mux.HandleFunc("/v1/sandboxes", g.handleSandboxes)
	mux.HandleFunc("/v1/sandboxes/", g.handleSandbox)

	// Observability API
	mux.HandleFunc("/v1/metrics", g.handleMetrics)
	mux.HandleFunc("/v1/audit", g.handleAudit)
	mux.HandleFunc("/v1/health", g.handleHealth)
	mux.HandleFunc("/v1/ready", g.handleReady)

	// Shell/Bash API (inspired by agent-infra/sandbox)
	mux.HandleFunc("/v1/shell/exec", g.handleShellExec)
	mux.HandleFunc("/v1/shell/stream", g.handleShellStream)
	mux.HandleFunc("/v1/shell/sessions", g.handleShellSessions)
	mux.HandleFunc("/v1/shell/sessions/", g.handleShellSession)
	mux.HandleFunc("/v1/shell/processes", g.handleShellProcesses)
	mux.HandleFunc("/v1/bash/exec", g.handleBashExec)
	mux.HandleFunc("/v1/bash/output", g.handleBashOutput)
	mux.HandleFunc("/v1/bash/kill", g.handleBashKill)

	// File API
	mux.HandleFunc("/v1/file/read", g.handleFileRead)
	mux.HandleFunc("/v1/file/write", g.handleFileWrite)
	mux.HandleFunc("/v1/file/list", g.handleFileList)
	mux.HandleFunc("/v1/file/find", g.handleFileFind)
	mux.HandleFunc("/v1/file/glob", g.handleFileGlob)
	mux.HandleFunc("/v1/file/grep", g.handleFileGrep)
	mux.HandleFunc("/v1/file/watch", g.handleFileWatch)
	mux.HandleFunc("/v1/file/move", g.handleFileMove)
	mux.HandleFunc("/v1/file/copy", g.handleFileCopy)
	mux.HandleFunc("/v1/file/delete", g.handleFileDelete)
	mux.HandleFunc("/v1/file/stat", g.handleFileStat)

	// Browser API
	mux.HandleFunc("/v1/browser/screenshot", g.handleBrowserScreenshot)
	mux.HandleFunc("/v1/browser/navigate", g.handleBrowserNavigate)
	mux.HandleFunc("/v1/browser/click", g.handleBrowserClick)
	mux.HandleFunc("/v1/browser/type", g.handleBrowserType)
	mux.HandleFunc("/v1/browser/scroll", g.handleBrowserScroll)
	mux.HandleFunc("/v1/browser/info", g.handleBrowserInfo)
	mux.HandleFunc("/v1/browser/cookies", g.handleBrowserCookies)

	// Code execution API
	mux.HandleFunc("/v1/code/execute", g.handleCodeExecute)
	mux.HandleFunc("/v1/code/info", g.handleCodeInfo)

	// Tenant & quota API
	mux.HandleFunc("/v1/tenants", g.handleTenants)
	mux.HandleFunc("/v1/tenants/", g.handleTenant)
	mux.HandleFunc("/v1/quotas", g.handleQuotas)
	mux.HandleFunc("/v1/quotas/", g.handleQuota)

	// Proxy & preview
	mux.HandleFunc("/v1/proxy/health", g.handleProxyHealth)
	mux.HandleFunc("/v1/proxy/diagnose", g.handleProxyDiagnose)

	// Template management API
	g.templateService.RegisterRoutes(mux)

	// E2B SDK-compatible API (drop-in replacement for E2B)
	g.e2bService.RegisterRoutes(mux)
}

// --- Middleware ---

// recoveryMiddleware catches panics from downstream handlers and middleware,
// preventing the HTTP server from dropping the connection (which causes
// "Connection reset by peer" on the client side). Instead, it logs the panic
// with a stack trace and returns a 500 Internal Server Error.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				klog.Errorf("Panic recovered in %s %s: %v\n%s",
					r.Method, r.URL.Path, rec, debug.Stack())
				writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal,
					fmt.Sprintf("internal server error: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Sandbox-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health endpoints
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		// JWT validation would go here in production
		// For now, extract sandbox ID from header if present
		sandboxID := r.Header.Get("X-Sandbox-ID")
		if sandboxID != "" {
			ctx := context.WithValue(r.Context(), contextKeySandboxID{}, sandboxID)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

type contextKeySandboxID struct{}

func sandboxIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKeySandboxID{}).(string); ok {
		return v
	}
	return ""
}

// --- Health handlers ---

// handleAPIIndex returns a discovery page listing all available API endpoints.
func (g *Gateway) handleAPIIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/" {
		http.NotFound(w, r)
		return
	}

	if r.Header.Get("Accept") == "application/json" {
		writeJSON(w, http.StatusOK, apiIndex)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>NexusBox API</title>
<style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:40px auto;padding:0 20px;color:#333}
h1{color:#1a1a2e;border-bottom:2px solid #e94560;padding-bottom:8px}
h2{color:#16213e;margin-top:32px}
.endpoint{display:flex;align-items:baseline;margin:4px 0;padding:4px 8px;border-radius:4px}
.endpoint:hover{background:#f0f0f0}
.method{font-weight:bold;min-width:60px;font-family:monospace;font-size:14px}
.GET{color:#0f9d58}.POST{color:#4285f4}.PUT{color:#f4b400}.DELETE{color:#db4437}
.path{font-family:monospace;font-size:14px;margin-left:8px}
.desc{color:#666;font-size:13px;margin-left:12px}
</style></head><body>
<h1>NexusBox Sandbox API</h1>
<p>Version: 0.1.0 | Workspace: %s</p>
`, g.fileService.workspace)

	for _, group := range apiIndex.Groups {
		fmt.Fprintf(w, "<h2>%s</h2>\n", group.Name)
		for _, ep := range group.Endpoints {
			fmt.Fprintf(w, `<div class="endpoint"><span class="method %s">%s</span><span class="path">%s</span><span class="desc">%s</span></div>`+"\n",
				ep.Method, ep.Method, ep.Path, ep.Desc)
		}
	}
	fmt.Fprintf(w, "</body></html>")
}

// apiIndexData holds the API discovery information.
var apiIndex = &APIIndex{
	Version: "v1",
	Groups: []APIGroup{
		{Name: "System", Endpoints: []APIEndpoint{
			{Method: "GET", Path: "/healthz", Desc: "Liveness probe"},
			{Method: "GET", Path: "/readyz", Desc: "Readiness probe"},
			{Method: "GET", Path: "/v1/system/env", Desc: "System environment info"},
			{Method: "GET", Path: "/v1/health", Desc: "Detailed health status"},
			{Method: "GET", Path: "/v1/ready", Desc: "Detailed readiness status"},
		}},
		{Name: "Sandbox", Endpoints: []APIEndpoint{
			{Method: "GET", Path: "/v1/sandboxes", Desc: "List sandboxes"},
			{Method: "POST", Path: "/v1/sandboxes", Desc: "Create sandbox"},
			{Method: "GET", Path: "/v1/sandboxes/{name}", Desc: "Get sandbox"},
			{Method: "PUT", Path: "/v1/sandboxes/{name}", Desc: "Update sandbox"},
			{Method: "DELETE", Path: "/v1/sandboxes/{name}", Desc: "Delete sandbox"},
		}},
		{Name: "Shell", Endpoints: []APIEndpoint{
			{Method: "POST", Path: "/v1/shell/exec", Desc: "Execute command"},
			{Method: "POST", Path: "/v1/shell/stream", Desc: "Stream command output (SSE)"},
			{Method: "GET", Path: "/v1/shell/sessions", Desc: "List sessions"},
			{Method: "POST", Path: "/v1/shell/sessions", Desc: "Create session"},
			{Method: "GET", Path: "/v1/shell/sessions/{id}", Desc: "Get session"},
			{Method: "DELETE", Path: "/v1/shell/sessions/{id}", Desc: "Kill session"},
			{Method: "GET", Path: "/v1/shell/processes", Desc: "List running processes"},
			{Method: "POST", Path: "/v1/bash/exec", Desc: "Bash exec (compat)"},
			{Method: "GET", Path: "/v1/bash/output", Desc: "Bash output (compat)"},
			{Method: "POST", Path: "/v1/bash/kill", Desc: "Kill bash (compat)"},
		}},
		{Name: "File", Endpoints: []APIEndpoint{
			{Method: "POST", Path: "/v1/file/read", Desc: "Read file"},
			{Method: "POST", Path: "/v1/file/write", Desc: "Write file"},
			{Method: "POST", Path: "/v1/file/list", Desc: "List directory"},
			{Method: "POST", Path: "/v1/file/find", Desc: "Find files"},
			{Method: "POST", Path: "/v1/file/glob", Desc: "Glob pattern match"},
			{Method: "POST", Path: "/v1/file/grep", Desc: "Grep file contents"},
			{Method: "POST", Path: "/v1/file/watch", Desc: "Watch file changes"},
			{Method: "POST", Path: "/v1/file/move", Desc: "Move/rename file"},
			{Method: "POST", Path: "/v1/file/copy", Desc: "Copy file"},
			{Method: "POST", Path: "/v1/file/delete", Desc: "Delete file"},
			{Method: "POST", Path: "/v1/file/stat", Desc: "File stat info"},
		}},
		{Name: "Browser", Endpoints: []APIEndpoint{
			{Method: "POST", Path: "/v1/browser/screenshot", Desc: "Take screenshot"},
			{Method: "POST", Path: "/v1/browser/navigate", Desc: "Navigate to URL"},
			{Method: "POST", Path: "/v1/browser/click", Desc: "Click element"},
			{Method: "POST", Path: "/v1/browser/type", Desc: "Type text"},
			{Method: "POST", Path: "/v1/browser/scroll", Desc: "Scroll page"},
			{Method: "GET", Path: "/v1/browser/info", Desc: "Browser info"},
			{Method: "GET", Path: "/v1/browser/cookies", Desc: "Get cookies"},
		}},
		{Name: "Code", Endpoints: []APIEndpoint{
			{Method: "POST", Path: "/v1/code/execute", Desc: "Execute code (Python/Node)"},
			{Method: "GET", Path: "/v1/code/info", Desc: "Runtime info"},
		}},
		{Name: "Observability", Endpoints: []APIEndpoint{
			{Method: "GET", Path: "/v1/metrics", Desc: "Prometheus-style metrics"},
			{Method: "GET", Path: "/v1/audit", Desc: "Audit log entries"},
		}},
		{Name: "Tenant & Quota", Endpoints: []APIEndpoint{
			{Method: "GET", Path: "/v1/tenants", Desc: "List tenants"},
			{Method: "POST", Path: "/v1/tenants", Desc: "Create tenant"},
			{Method: "GET", Path: "/v1/tenants/{name}", Desc: "Get tenant"},
			{Method: "DELETE", Path: "/v1/tenants/{name}", Desc: "Delete tenant"},
			{Method: "GET", Path: "/v1/quotas", Desc: "List quotas"},
			{Method: "GET", Path: "/v1/quotas/{name}", Desc: "Get quota"},
		}},
		{Name: "Templates", Endpoints: []APIEndpoint{
			{Method: "GET", Path: "/v1/templates", Desc: "List templates"},
			{Method: "POST", Path: "/v1/templates", Desc: "Create template"},
			{Method: "GET", Path: "/v1/templates/{name}", Desc: "Get template"},
			{Method: "PUT", Path: "/v1/templates/{name}", Desc: "Update template"},
			{Method: "DELETE", Path: "/v1/templates/{name}", Desc: "Delete template"},
		}},
		{Name: "E2B Compatible", Endpoints: []APIEndpoint{
			{Method: "POST", Path: "/e2b/v1/sandboxes", Desc: "Create sandbox (E2B SDK)"},
			{Method: "GET", Path: "/e2b/v1/sandboxes", Desc: "List sandboxes (E2B SDK)"},
			{Method: "GET", Path: "/e2b/v1/sandboxes/{id}", Desc: "Get sandbox (E2B SDK)"},
			{Method: "DELETE", Path: "/e2b/v1/sandboxes/{id}", Desc: "Kill sandbox (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/commands", Desc: "Run command (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/files", Desc: "Read/Write file (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/code", Desc: "Execute code (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/refreshes", Desc: "Refresh timeout (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/pause", Desc: "Pause sandbox (E2B SDK)"},
			{Method: "POST", Path: "/e2b/v1/sandboxes/{id}/resume", Desc: "Resume sandbox (E2B SDK)"},
			{Method: "GET", Path: "/e2b/v1/templates", Desc: "List templates (E2B SDK)"},
			{Method: "GET", Path: "/e2b/v1/health", Desc: "E2B health check"},
		}},
	},
}

// APIIndex represents the API discovery document.
type APIIndex struct {
	Version string     `json:"version"`
	Groups  []APIGroup `json:"groups"`
}

// APIGroup represents a group of related API endpoints.
type APIGroup struct {
	Name      string        `json:"name"`
	Endpoints []APIEndpoint `json:"endpoints"`
}

// APIEndpoint represents a single API endpoint.
type APIEndpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Desc   string `json:"description"`
}

func (g *Gateway) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok")
}

func (g *Gateway) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok")
}

// handleSystemEnv returns the system environment information.
func (g *Gateway) handleSystemEnv(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace":  g.fileService.workspace,
		"shell":      "/bin/bash",
		"python":     "python3",
		"node":       "node",
		"browser":    "chromium",
		"version":    "0.1.0",
		"apiVersion": "v1",
	})
}

// --- Sandbox management handlers ---

func (g *Gateway) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.sandboxService.List(w, r)
	case http.MethodPost:
		g.sandboxService.Create(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) handleSandbox(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
	parts := strings.SplitN(path, "/", 2)
	namespace := "default"
	name := parts[0]
	if len(parts) == 2 {
		namespace = parts[0]
		name = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		g.sandboxService.Get(w, r, namespace, name)
	case http.MethodPut:
		g.sandboxService.Update(w, r, namespace, name)
	case http.MethodDelete:
		g.sandboxService.Delete(w, r, namespace, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- Observability handlers ---

func (g *Gateway) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, g.metrics.GetMetrics())
}

func (g *Gateway) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	entries := g.auditLogger.GetEntries(limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}

func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "healthy",
		"timestamp":  time.Now().Format(time.RFC3339),
		"components": g.getComponentsHealth(),
	})
}

func (g *Gateway) handleReady(w http.ResponseWriter, r *http.Request) {
	ready := true
	components := g.getComponentsHealth()
	for _, status := range components {
		if status != "ready" && status != "available" {
			ready = false
			break
		}
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]interface{}{
		"ready":      ready,
		"components": components,
	})
}

// getComponentsHealth returns the health status of all components.
func (g *Gateway) getComponentsHealth() map[string]string {
	components := make(map[string]string)
	if g.shellService != nil {
		components["shell"] = "ready"
	}
	if g.fileService != nil {
		components["file"] = "ready"
	}
	if g.browserService != nil {
		components["browser"] = "available"
	}
	if g.codeService != nil {
		components["code"] = "available"
	}
	if g.sandboxService != nil {
		components["sandbox"] = "ready"
	}
	return components
}

// --- Shell handlers ---

func (g *Gateway) handleShellExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.shellService.Exec(w, r)
}

func (g *Gateway) handleShellStream(w http.ResponseWriter, r *http.Request) {
	g.shellService.StreamExec(w, r)
}

func (g *Gateway) handleShellProcesses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	processes := g.shellService.ListProcesses()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"processes": processes,
		"count":     len(processes),
	})
}

func (g *Gateway) handleShellSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.shellService.ListSessions(w, r)
	case http.MethodPost:
		g.shellService.CreateSession(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) handleShellSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/shell/sessions/")
	switch r.Method {
	case http.MethodGet:
		g.shellService.GetSession(w, r, sessionID)
	case http.MethodDelete:
		g.shellService.KillSession(w, r, sessionID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) handleBashExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.shellService.BashExec(w, r)
}

func (g *Gateway) handleBashOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.shellService.BashOutput(w, r)
}

func (g *Gateway) handleBashKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.shellService.BashKill(w, r)
}

// --- File handlers ---

func (g *Gateway) handleFileRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Read(w, r)
}

func (g *Gateway) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Write(w, r)
}

func (g *Gateway) handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.List(w, r)
}

func (g *Gateway) handleFileFind(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Find(w, r)
}

func (g *Gateway) handleFileGlob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Glob(w, r)
}

func (g *Gateway) handleFileGrep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Grep(w, r)
}

func (g *Gateway) handleFileWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Watch(w, r)
}

func (g *Gateway) handleFileMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Move(w, r)
}

func (g *Gateway) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Copy(w, r)
}

func (g *Gateway) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Delete(w, r)
}

func (g *Gateway) handleFileStat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.fileService.Stat(w, r)
}

// --- Browser handlers ---

func (g *Gateway) handleBrowserScreenshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Screenshot(w, r)
}

func (g *Gateway) handleBrowserNavigate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Navigate(w, r)
}

func (g *Gateway) handleBrowserClick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Click(w, r)
}

func (g *Gateway) handleBrowserType(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Type(w, r)
}

func (g *Gateway) handleBrowserScroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Scroll(w, r)
}

func (g *Gateway) handleBrowserInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Info(w, r)
}

func (g *Gateway) handleBrowserCookies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.browserService.Cookies(w, r)
}

// --- Code execution handlers ---

func (g *Gateway) handleCodeExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.codeService.Execute(w, r)
}

func (g *Gateway) handleCodeInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	g.codeService.Info(w, r)
}

// --- Tenant handlers ---

func (g *Gateway) handleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.listTenants(w, r)
	case http.MethodPost:
		g.createTenant(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) handleTenant(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/tenants/")
	switch r.Method {
	case http.MethodGet:
		g.getTenant(w, r, name)
	case http.MethodDelete:
		g.deleteTenant(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (g *Gateway) listTenants(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": []interface{}{},
	})
}

func (g *Gateway) createTenant(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

func (g *Gateway) getTenant(w http.ResponseWriter, r *http.Request, name string) {
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (g *Gateway) deleteTenant(w http.ResponseWriter, r *http.Request, name string) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Quota handlers ---

func (g *Gateway) handleQuotas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": []interface{}{}})
}

func (g *Gateway) handleQuota(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/quotas/")
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// --- Proxy handlers ---

func (g *Gateway) handleProxyHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "healthy",
		"version": "0.1.0",
	})
}

func (g *Gateway) handleProxyDiagnose(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"proxy":    "ok",
		"upstream": "ok",
	})
}

// --- Utility ---

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
