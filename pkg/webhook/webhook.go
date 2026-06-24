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

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

// WebhookServer handles admission webhook requests for the NexusBox system.
// It validates and mutates Sandbox and Tenant CRD objects before they are
// persisted to the API server.
type WebhookServer struct {
	// httpServer is the underlying HTTP server.
	httpServer *http.Server

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// quotaManager manages resource quotas.
	quotaManager *quota.QuotaManager

	// port is the port to listen on.
	port int

	// certFile is the path to the TLS certificate.
	certFile string

	// keyFile is the path to the TLS key.
	keyFile string
}

// WebhookConfig holds configuration for the webhook server.
type WebhookConfig struct {
	Port          int
	CertFile      string
	KeyFile       string
	TenantManager *tenant.TenantManager
	QuotaManager  *quota.QuotaManager
}

// NewWebhookServer creates a new webhook server.
func NewWebhookServer(config *WebhookConfig) *WebhookServer {
	s := &WebhookServer{
		port:          config.Port,
		certFile:      config.CertFile,
		keyFile:       config.KeyFile,
		tenantManager: config.TenantManager,
		quotaManager:  config.QuotaManager,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/validate/sandbox", s.handleValidateSandbox)
	mux.HandleFunc("/validate/tenant", s.handleValidateTenant)
	mux.HandleFunc("/mutate/sandbox", s.handleMutateSandbox)
	mux.HandleFunc("/mutate/tenant", s.handleMutateTenant)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	return s
}

// Start starts the webhook server.
func (s *WebhookServer) Start(ctx context.Context) error {
	go func() {
		var err error
		if s.certFile != "" && s.keyFile != "" {
			klog.Infof("Webhook server listening on :%d (TLS)", s.port)
			err = s.httpServer.ListenAndServeTLS(s.certFile, s.keyFile)
		} else {
			klog.Infof("Webhook server listening on :%d", s.port)
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			klog.Errorf("Webhook server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		s.Shutdown()
	}()

	return nil
}

// Shutdown gracefully shuts down the webhook server.
func (s *WebhookServer) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		klog.Errorf("Webhook server shutdown error: %v", err)
	}
}

// AdmissionReview represents a Kubernetes admission review request/response.
type AdmissionReview struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Request    *AdmissionRequest  `json:"request,omitempty"`
	Response   *AdmissionResponse `json:"response,omitempty"`
}

// AdmissionRequest represents a Kubernetes admission request.
type AdmissionRequest struct {
	UID       string               `json:"uid"`
	Kind      GroupVersionKind     `json:"kind"`
	Resource  GroupVersionResource `json:"resource"`
	Name      string               `json:"name"`
	Namespace string               `json:"namespace"`
	Operation string               `json:"operation"`
	UserInfo  UserInfo             `json:"userInfo"`
	Object    json.RawMessage      `json:"object,omitempty"`
	OldObject json.RawMessage      `json:"oldObject,omitempty"`
	DryRun    *bool                `json:"dryRun,omitempty"`
	Options   json.RawMessage      `json:"options,omitempty"`
}

// AdmissionResponse represents a Kubernetes admission response.
type AdmissionResponse struct {
	UID              string            `json:"uid"`
	Allowed          bool              `json:"allowed"`
	Result           *Status           `json:"status,omitempty"`
	Patch            []byte            `json:"patch,omitempty"`
	PatchType        *PatchType        `json:"patchType,omitempty"`
	AuditAnnotations map[string]string `json:"auditAnnotations,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
}

// GroupVersionKind represents a Kubernetes GroupVersionKind.
type GroupVersionKind struct {
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

// GroupVersionResource represents a Kubernetes GroupVersionResource.
type GroupVersionResource struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}

// UserInfo represents user information in an admission request.
type UserInfo struct {
	Username string   `json:"username"`
	UID      string   `json:"uid"`
	Groups   []string `json:"groups"`
}

// Status represents a Kubernetes status object.
type Status struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// PatchType represents the type of patch in an admission response.
type PatchType string

const (
	PatchTypeJSONPatch PatchType = "JSONPatch"
)

// handleValidateSandbox validates sandbox creation/update requests.
func (s *WebhookServer) handleValidateSandbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		klog.Errorf("Failed to decode admission review: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response := s.validateSandbox(&review)
	review.Response = response
	review.Request = nil

	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.Errorf("Failed to encode admission review: %v", err)
	}
}

// validateSandbox validates a sandbox object.
func (s *WebhookServer) validateSandbox(review *AdmissionReview) *AdmissionResponse {
	req := review.Request
	response := &AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	// Parse the sandbox object
	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(req.Object, &sb); err != nil {
		response.Allowed = false
		response.Result = &Status{
			Code:    400,
			Message: fmt.Sprintf("Failed to parse sandbox: %v", err),
		}
		return response
	}

	// Validate tenant reference
	if sb.Spec.TenantRef.Name == "" {
		response.Allowed = false
		response.Result = &Status{
			Code:    400,
			Message: "Tenant reference is required",
		}
		return response
	}

	// Validate runtime type
	validRuntimes := map[string]bool{
		"kata-containers": true,
		"gvisor":          true,
		"runc":            true,
	}
	if !validRuntimes[string(sb.Spec.Runtime)] {
		response.Allowed = false
		response.Result = &Status{
			Code:    400,
			Message: fmt.Sprintf("Invalid runtime type: %s. Must be one of: kata-containers, gvisor, runc", sb.Spec.Runtime),
		}
		return response
	}

	// Validate resources
	if sb.Spec.Resources.CPU == "" && sb.Spec.Resources.Memory == "" {
		response.Warnings = append(response.Warnings, "No resource requests specified, defaults will be applied")
	}

	// Validate tenant exists and is active
	if s.tenantManager != nil {
		if err := s.tenantManager.CanCreateSandbox(context.Background(), sb.Spec.TenantRef.Name, &sb.Spec.Resources); err != nil {
			response.Allowed = false
			response.Result = &Status{
				Code:    403,
				Message: fmt.Sprintf("Tenant validation failed: %v", err),
			}
			return response
		}
	}

	// Validate quota
	if s.quotaManager != nil {
		if err := s.quotaManager.CheckQuota(sb.Spec.TenantRef.Name, &sb.Spec.Resources); err != nil {
			response.Allowed = false
			response.Result = &Status{
				Code:    403,
				Message: fmt.Sprintf("Quota check failed: %v", err),
			}
			return response
		}
	}

	return response
}

// handleValidateTenant validates tenant creation/update requests.
func (s *WebhookServer) handleValidateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response := s.validateTenant(&review)
	review.Response = response
	review.Request = nil

	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.Errorf("Failed to encode admission review: %v", err)
	}
}

// validateTenant validates a tenant object.
func (s *WebhookServer) validateTenant(review *AdmissionReview) *AdmissionResponse {
	req := review.Request
	response := &AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	var t v1alpha1.Tenant
	if err := json.Unmarshal(req.Object, &t); err != nil {
		response.Allowed = false
		response.Result = &Status{
			Code:    400,
			Message: fmt.Sprintf("Failed to parse tenant: %v", err),
		}
		return response
	}

	// Validate resource quota
	if t.Spec.ResourceQuota.CPU == "" {
		response.Warnings = append(response.Warnings, "No CPU quota specified")
	}
	if t.Spec.ResourceQuota.Memory == "" {
		response.Warnings = append(response.Warnings, "No memory quota specified")
	}

	// Validate isolation policy
	if t.Spec.IsolationLevel == v1alpha1.IsolationLevelMaximum {
		response.Warnings = append(response.Warnings, "Maximum isolation level may require dedicated nodes")
	}

	return response
}

// handleMutateSandbox mutates sandbox objects with defaults.
func (s *WebhookServer) handleMutateSandbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response := s.mutateSandbox(&review)
	review.Response = response
	review.Request = nil

	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.Errorf("Failed to encode admission review: %v", err)
	}
}

// mutateSandbox applies default values to a sandbox object.
func (s *WebhookServer) mutateSandbox(review *AdmissionReview) *AdmissionResponse {
	req := review.Request
	response := &AdmissionResponse{
		UID:     req.UID,
		Allowed: true,
	}

	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(req.Object, &sb); err != nil {
		response.Allowed = false
		response.Result = &Status{
			Code:    400,
			Message: fmt.Sprintf("Failed to parse sandbox: %v", err),
		}
		return response
	}

	// Apply defaults
	var patches []map[string]interface{}

	// Default runtime to runc if not specified
	if sb.Spec.Runtime == "" {
		patches = append(patches, map[string]interface{}{
			"op":    "add",
			"path":  "/spec/runtime",
			"value": "runc",
		})
	}

	// Default resources if not specified
	if sb.Spec.Resources.CPU == "" {
		patches = append(patches, map[string]interface{}{
			"op":    "add",
			"path":  "/spec/resources/cpu",
			"value": "100m",
		})
	}

	if sb.Spec.Resources.Memory == "" {
		patches = append(patches, map[string]interface{}{
			"op":    "add",
			"path":  "/spec/resources/memory",
			"value": "128Mi",
		})
	}

	// Default graceful shutdown
	if sb.Spec.GracefulShutdownSeconds == nil {
		defaultShutdown := int32(30)
		patches = append(patches, map[string]interface{}{
			"op":    "add",
			"path":  "/spec/gracefulShutdownSeconds",
			"value": defaultShutdown,
		})
	}

	// Apply patches
	if len(patches) > 0 {
		patchBytes, err := json.Marshal(patches)
		if err != nil {
			klog.Errorf("Failed to marshal patches: %v", err)
		} else {
			response.Patch = patchBytes
			patchType := PatchTypeJSONPatch
			response.PatchType = &patchType
		}
	}

	return response
}

// handleMutateTenant mutates tenant objects with defaults.
func (s *WebhookServer) handleMutateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var review AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	response := &AdmissionResponse{
		UID:     review.Request.UID,
		Allowed: true,
	}

	review.Response = response
	review.Request = nil

	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.Errorf("Failed to encode admission review: %v", err)
	}
}
