/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/metrics"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

// APIServer provides the HTTP API for the NexusBox sandbox management system.
// It exposes RESTful endpoints for managing sandboxes, tenants, and monitoring.
type APIServer struct {
	// httpServer is the underlying HTTP server.
	httpServer *http.Server

	// lifecycleManager manages sandbox lifecycle.
	lifecycleManager *lifecycle.LifecycleManager

	// runtimeManager manages sandbox runtimes.
	runtimeManager *runtime.RuntimeManager

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// quotaManager manages resource quotas.
	quotaManager *quota.QuotaManager

	// metricsCollector collects system metrics.
	metricsCollector *metrics.MetricsCollector

	// port is the port to listen on.
	port int

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// APIServerConfig holds configuration for the API server.
type APIServerConfig struct {
	Port             int
	LifecycleManager *lifecycle.LifecycleManager
	RuntimeManager   *runtime.RuntimeManager
	TenantManager    *tenant.TenantManager
	QuotaManager     *quota.QuotaManager
	MetricsCollector *metrics.MetricsCollector
}

// NewAPIServer creates a new API server.
func NewAPIServer(config *APIServerConfig) *APIServer {
	s := &APIServer{
		port:             config.Port,
		lifecycleManager: config.LifecycleManager,
		runtimeManager:   config.RuntimeManager,
		tenantManager:    config.TenantManager,
		quotaManager:     config.QuotaManager,
		metricsCollector: config.MetricsCollector,
		stopCh:           make(chan struct{}),
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// registerRoutes registers all API routes.
func (s *APIServer) registerRoutes(mux *http.ServeMux) {
	// Health endpoints
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	// Sandbox API
	mux.HandleFunc("/api/v1/sandboxes", s.handleSandboxes)
	mux.HandleFunc("/api/v1/sandboxes/", s.handleSandbox)

	// Tenant API
	mux.HandleFunc("/api/v1/tenants", s.handleTenants)
	mux.HandleFunc("/api/v1/tenants/", s.handleTenant)

	// Node API
	mux.HandleFunc("/api/v1/nodes", s.handleNodes)
	mux.HandleFunc("/api/v1/nodes/", s.handleNode)

	// Metrics API
	mux.HandleFunc("/api/v1/metrics/scheduling", s.handleSchedulingMetrics)
	mux.HandleFunc("/api/v1/metrics/runtime", s.handleRuntimeMetrics)

	// Cost API
	mux.HandleFunc("/api/v1/costs", s.handleCosts)

	// Quota API
	mux.HandleFunc("/api/v1/quotas", s.handleQuotas)
	mux.HandleFunc("/api/v1/quotas/", s.handleQuota)
}

// Start starts the API server.
func (s *APIServer) Start(ctx context.Context) error {
	go func() {
		klog.Infof("API server listening on :%d", s.port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Errorf("API server error: %v", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			s.Shutdown()
		case <-s.stopCh:
			return
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the API server.
func (s *APIServer) Shutdown() {
	close(s.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		klog.Errorf("API server shutdown error: %v", err)
	}
}

// handleHealthz handles health check requests.
func (s *APIServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok")
}

// handleReadyz handles readiness check requests.
func (s *APIServer) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok")
}

// handleSandboxes handles sandbox list and create requests.
func (s *APIServer) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSandboxes(w, r)
	case http.MethodPost:
		s.createSandbox(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSandbox handles individual sandbox operations.
func (s *APIServer) handleSandbox(w http.ResponseWriter, r *http.Request) {
	// Extract sandbox name from path
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/sandboxes/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 {
		http.Error(w, "Invalid sandbox path", http.StatusBadRequest)
		return
	}

	namespace := "default"
	name := parts[0]
	if len(parts) == 2 {
		namespace = parts[0]
		name = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		s.getSandbox(w, r, namespace, name)
	case http.MethodPut:
		s.updateSandbox(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteSandbox(w, r, namespace, name)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listSandboxes lists all sandboxes.
func (s *APIServer) listSandboxes(w http.ResponseWriter, r *http.Request) {
	// In production, this would query from the informer/cache
	response := map[string]interface{}{
		"items":    []interface{}{},
		"metadata": map[string]string{"resourceVersion": "0"},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// createSandbox creates a new sandbox.
func (s *APIServer) createSandbox(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(body, &sb); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse request: %v", err), http.StatusBadRequest)
		return
	}

	// Create sandbox via lifecycle manager
	if err := s.lifecycleManager.CreateSandbox(r.Context(), &sb); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusCreated, sb)
}

// getSandbox gets a specific sandbox.
func (s *APIServer) getSandbox(w http.ResponseWriter, r *http.Request, namespace, name string) {
	// In production, this would query from the informer/cache
	s.writeJSON(w, http.StatusNotFound, map[string]string{
		"error": fmt.Sprintf("sandbox %s/%s not found", namespace, name),
	})
}

// updateSandbox updates a sandbox.
func (s *APIServer) updateSandbox(w http.ResponseWriter, r *http.Request, namespace, name string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(body, &sb); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse request: %v", err), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, http.StatusOK, sb)
}

// deleteSandbox deletes a sandbox.
func (s *APIServer) deleteSandbox(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if err := s.lifecycleManager.DeleteSandbox(r.Context(), namespace+"/"+name); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
	})
}

// handleTenants handles tenant list and create requests.
func (s *APIServer) handleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTenants(w, r)
	case http.MethodPost:
		s.createTenant(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTenant handles individual tenant operations.
func (s *APIServer) handleTenant(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/tenants/")

	switch r.Method {
	case http.MethodGet:
		s.getTenant(w, r, name)
	case http.MethodPut:
		s.updateTenant(w, r, name)
	case http.MethodDelete:
		s.deleteTenant(w, r, name)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listTenants lists all tenants.
func (s *APIServer) listTenants(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"items":    []interface{}{},
		"metadata": map[string]string{"resourceVersion": "0"},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// createTenant creates a new tenant.
func (s *APIServer) createTenant(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var t v1alpha1.Tenant
	if err := json.Unmarshal(body, &t); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse request: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.tenantManager.RegisterTenant(r.Context(), &t); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusCreated, t)
}

// getTenant gets a specific tenant.
func (s *APIServer) getTenant(w http.ResponseWriter, r *http.Request, name string) {
	info, exists := s.tenantManager.GetTenant(name)
	if !exists {
		s.writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("tenant %s not found", name),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, info)
}

// updateTenant updates a tenant.
func (s *APIServer) updateTenant(w http.ResponseWriter, r *http.Request, name string) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// deleteTenant deletes a tenant.
func (s *APIServer) deleteTenant(w http.ResponseWriter, r *http.Request, name string) {
	if err := s.tenantManager.UnregisterTenant(r.Context(), name); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleNodes handles node list requests.
func (s *APIServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	response := map[string]interface{}{
		"items":    []interface{}{},
		"metadata": map[string]string{"resourceVersion": "0"},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleNode handles individual node operations.
func (s *APIServer) handleNode(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/nodes/")
	s.writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// handleSchedulingMetrics returns scheduling metrics.
func (s *APIServer) handleSchedulingMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"totalScheduled":         0,
		"totalFailed":            0,
		"totalUnschedulable":     0,
		"avgSchedulingLatencyMs": 0.0,
		"avgBindingLatencyMs":    0.0,
		"pendingCount":           0,
		"activeNodes":            0,
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleRuntimeMetrics returns runtime metrics.
func (s *APIServer) handleRuntimeMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"activeRuntimes": 0,
		"pooledRuntimes": 0,
		"byType":         map[string]int{},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleCosts returns cost information.
func (s *APIServer) handleCosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"items": []interface{}{},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// handleQuotas handles quota list requests.
func (s *APIServer) handleQuotas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listQuotas(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleQuota handles individual quota operations.
func (s *APIServer) handleQuota(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/quotas/")
	s.writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// listQuotas lists all quotas.
func (s *APIServer) listQuotas(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"items":    []interface{}{},
		"metadata": map[string]string{"resourceVersion": "0"},
	}
	s.writeJSON(w, http.StatusOK, response)
}

// writeJSON writes a JSON response.
func (s *APIServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		klog.Errorf("Failed to encode JSON response: %v", err)
	}
}

// parseNamespaceFromQuery parses the namespace from query parameters.
func parseNamespaceFromQuery(r *http.Request) string {
	return r.URL.Query().Get("namespace")
}

// parseLimitFromQuery parses the limit from query parameters.
func parseLimitFromQuery(r *http.Request) int {
	limitStr := r.URL.Query().Get("limit")
	if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
		return limit
	}
	return 100
}

// parseContinueFromQuery parses the continue token from query parameters.
func parseContinueFromQuery(r *http.Request) string {
	return r.URL.Query().Get("continue")
}
