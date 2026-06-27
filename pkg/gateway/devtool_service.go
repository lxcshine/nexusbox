package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/nexusbox/nexusbox/pkg/devtool"
	"k8s.io/klog/v2"
)

// devToolStartTimeout is the maximum time to wait for a dev tool to start.
const devToolStartTimeout = 30 * time.Second

// DevToolService handles dev tool API requests.
// Endpoints:
//
//	POST   /v1/devtools              - Start a dev tool for a sandbox
//	GET    /v1/devtools              - List all dev tool instances
//	GET    /v1/devtools/{id}         - Get dev tool status
//	DELETE /v1/devtools/{id}         - Stop a dev tool
//	GET    /v1/devtools/{id}/health  - Health check
//	ANY    /v1/devtools/proxy/{type}/{sandboxID}/... - Reverse proxy
type DevToolService struct {
	manager *devtool.DevToolManager
}

// NewDevToolService creates a new DevToolService.
func NewDevToolService(mgr *devtool.DevToolManager) *DevToolService {
	return &DevToolService{manager: mgr}
}

// ServeHTTP routes dev tool requests.
func (s *DevToolService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/devtools")
	path = strings.TrimPrefix(path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleList(w, r)
		case http.MethodPost:
			s.handleCreate(w, r)
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}

	// Check if this is a proxy request
	if strings.HasPrefix(path, "proxy/") {
		if s.manager == nil {
			http.Error(w, `{"error":"dev tool manager not available"}`, http.StatusServiceUnavailable)
			return
		}
		s.manager.Proxy().ServeHTTP(w, r)
		return
	}

	// Otherwise, it's an instance-level request: /{id} or /{id}/health
	parts := strings.SplitN(path, "/", 2)
	instanceID := parts[0]

	if len(parts) == 2 && parts[1] == "health" {
		s.handleHealth(w, r, instanceID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGet(w, r, instanceID)
	case http.MethodDelete:
		s.handleDelete(w, r, instanceID)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// startDevToolRequest is the request body for POST /v1/devtools.
type startDevToolRequest struct {
	SandboxID  string                `json:"sandboxId"`
	WorkingDir string                `json:"workingDir"`
	Config     devtool.DevToolConfig `json:"config"`
}

// handleCreate starts a new dev tool instance.
func (s *DevToolService) handleCreate(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		writeDevToolJSONError(w, "dev tool manager not available", http.StatusServiceUnavailable)
		return
	}

	var req startDevToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDevToolJSONError(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.SandboxID == "" {
		writeDevToolJSONError(w, "sandboxId is required", http.StatusBadRequest)
		return
	}
	if req.WorkingDir == "" {
		writeDevToolJSONError(w, "workingDir is required", http.StatusBadRequest)
		return
	}

	req.Config.Enabled = true // force-enable since user is explicitly requesting

	ctx, cancel := context.WithTimeout(r.Context(), devToolStartTimeout)
	defer cancel()

	inst, err := s.manager.Start(ctx, req.SandboxID, req.Config, req.WorkingDir)
	if err != nil {
		klog.Warningf("Failed to start dev tool: %v", err)
		writeDevToolJSONError(w, "failed to start dev tool: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeDevToolJSON(w, http.StatusCreated, inst)
}

// handleList lists all dev tool instances.
func (s *DevToolService) handleList(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		writeDevToolJSON(w, http.StatusOK, []interface{}{})
		return
	}
	instances := s.manager.List()
	writeDevToolJSON(w, http.StatusOK, instances)
}

// handleGet returns a specific dev tool instance.
func (s *DevToolService) handleGet(w http.ResponseWriter, r *http.Request, instanceID string) {
	if s.manager == nil {
		writeDevToolJSONError(w, "dev tool manager not available", http.StatusServiceUnavailable)
		return
	}
	inst, ok := s.manager.Get(instanceID)
	if !ok {
		writeDevToolJSONError(w, "instance not found", http.StatusNotFound)
		return
	}
	writeDevToolJSON(w, http.StatusOK, inst)
}

// handleDelete stops a dev tool instance.
func (s *DevToolService) handleDelete(w http.ResponseWriter, r *http.Request, instanceID string) {
	if s.manager == nil {
		writeDevToolJSONError(w, "dev tool manager not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.manager.Stop(r.Context(), instanceID); err != nil {
		writeDevToolJSONError(w, "failed to stop dev tool: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeDevToolJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// handleHealth checks the health of a dev tool instance.
func (s *DevToolService) handleHealth(w http.ResponseWriter, r *http.Request, instanceID string) {
	if s.manager == nil {
		writeDevToolJSONError(w, "dev tool manager not available", http.StatusServiceUnavailable)
		return
	}
	healthy, err := s.manager.HealthCheck(r.Context(), instanceID)
	if err != nil {
		writeDevToolJSONError(w, err.Error(), http.StatusNotFound)
		return
	}
	writeDevToolJSON(w, http.StatusOK, map[string]interface{}{
		"healthy": healthy,
	})
}

// writeDevToolJSON writes a JSON response.
func writeDevToolJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeDevToolJSONError writes a JSON error response.
func writeDevToolJSONError(w http.ResponseWriter, message string, status int) {
	writeDevToolJSON(w, status, map[string]string{"error": message})
}
