package egress

import (
	"encoding/json"
	"net/http"
	"strings"
)

// PolicyRequest is the request body for setting an egress policy.
type PolicyRequest struct {
	SandboxID              string                       `json:"sandboxID"`
	AllowedDomains         []string                     `json:"allowedDomains,omitempty"`
	DeniedDomains          []string                     `json:"deniedDomains,omitempty"`
	InjectHeaders          map[string]string            `json:"injectHeaders,omitempty"`
	InjectHeadersPerDomain map[string]map[string]string `json:"injectHeadersPerDomain,omitempty"`
	MaxRequestsPerMinute   int32                        `json:"maxRequestsPerMinute,omitempty"`
	AuditEnabled           bool                         `json:"auditEnabled"`
	BlockPrivateIPs        bool                         `json:"blockPrivateIPs"`
}

// ServeHTTP implements http.Handler for the egress policy API.
// Routes:
//
//	GET    /v1/egress/policies          - List policies
//	POST   /v1/egress/policies          - Set policy
//	GET    /v1/egress/policies/{id}     - Get policy
//	DELETE /v1/egress/policies/{id}     - Delete policy
//	GET    /v1/egress/audit             - Get audit log
//	GET    /v1/egress/stats             - Get gateway stats
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Path
	switch {
	case path == "/v1/egress/policies" && r.Method == http.MethodGet:
		g.handleListPolicies(w, r)
	case path == "/v1/egress/policies" && r.Method == http.MethodPost:
		g.handleSetPolicy(w, r)
	case strings.HasPrefix(path, "/v1/egress/policies/") && r.Method == http.MethodGet:
		g.handleGetPolicy(w, r)
	case strings.HasPrefix(path, "/v1/egress/policies/") && r.Method == http.MethodDelete:
		g.handleDeletePolicy(w, r)
	case path == "/v1/egress/audit" && r.Method == http.MethodGet:
		g.handleAudit(w, r)
	case path == "/v1/egress/stats" && r.Method == http.MethodGet:
		g.handleStats(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
}

func (g *Gateway) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	policies := make([]*Policy, 0, len(g.policies))
	for _, p := range g.policies {
		if p.SandboxID != "__default__" {
			policies = append(policies, p)
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"policies": policies,
		"count":    len(policies),
	})
}

func (g *Gateway) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	var req PolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if req.SandboxID == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "sandboxID required"})
		return
	}

	policy := &Policy{
		SandboxID:              req.SandboxID,
		AllowedDomains:         req.AllowedDomains,
		DeniedDomains:          req.DeniedDomains,
		InjectHeaders:          req.InjectHeaders,
		InjectHeadersPerDomain: req.InjectHeadersPerDomain,
		MaxRequestsPerMinute:   req.MaxRequestsPerMinute,
		AuditEnabled:           req.AuditEnabled,
		BlockPrivateIPs:        req.BlockPrivateIPs,
	}
	g.SetPolicy(req.SandboxID, policy)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(policy)
}

func (g *Gateway) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	sandboxID := strings.TrimPrefix(r.URL.Path, "/v1/egress/policies/")
	policy := g.GetPolicy(sandboxID)
	if policy == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "policy not found"})
		return
	}
	json.NewEncoder(w).Encode(policy)
}

func (g *Gateway) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	sandboxID := strings.TrimPrefix(r.URL.Path, "/v1/egress/policies/")
	g.RemovePolicy(sandboxID)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (g *Gateway) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	entries := g.GetAuditEntries(limit)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
	})
}

func (g *Gateway) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := g.GetStats()
	json.NewEncoder(w).Encode(stats)
}
