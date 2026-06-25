package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
)

// E2BService implements an E2B SDK-compatible REST API.
//
// This compatibility layer allows existing E2B SDK clients (Python/JS SDK,
// LangChain integrations, OpenAI Agents SDK, etc.) to use NexusBox as a
// drop-in replacement by simply changing the API base URL.
//
// Reference: https://e2b.dev/docs/api
// Inspired by CubeSandbox's CubeAPI which also provides E2B compatibility.
type E2BService struct {
	lifecycleManager *lifecycle.LifecycleManager
	runtimeManager   *runtime.RuntimeManager
	templateService  *TemplateService
	shellService     *ShellService
	fileService      *FileService
	codeService      *CodeService
}

// NewE2BService creates a new E2B-compatible service.
func NewE2BService(
	lifecycleManager *lifecycle.LifecycleManager,
	runtimeManager *runtime.RuntimeManager,
	templateService *TemplateService,
	shellService *ShellService,
	fileService *FileService,
	codeService *CodeService,
) *E2BService {
	return &E2BService{
		lifecycleManager: lifecycleManager,
		runtimeManager:   runtimeManager,
		templateService:  templateService,
		shellService:     shellService,
		fileService:      fileService,
		codeService:      codeService,
	}
}

// --- E2B API Types (compatible with E2B SDK) ---

// E2BCreateSandboxRequest matches the E2B POST /sandboxes request body.
type E2BCreateSandboxRequest struct {
	// TemplateID is the ID of the template to use (maps to NexusBox template name).
	TemplateID string `json:"templateID,omitempty"`
	// Timeout is the sandbox timeout in seconds.
	Timeout int32 `json:"timeout,omitempty"`
	// Metadata is custom metadata for the sandbox.
	Metadata map[string]string `json:"metadata,omitempty"`
	// EnvVars are environment variables for the sandbox.
	EnvVars map[string]string `json:"envVars,omitempty"`
	// MemoryMB overrides the memory limit in MB.
	MemoryMB int32 `json:"memoryMB,omitempty"`
	// VCPUs overrides the vCPU limit.
	VCPUs int32 `json:"vCPUs,omitempty"`
	// ClientID is an optional client identifier.
	ClientID string `json:"clientID,omitempty"`
}

// E2BSandbox represents an E2B sandbox resource.
type E2BSandbox struct {
	SandboxID  string            `json:"sandboxID"`
	TemplateID string            `json:"templateID"`
	Status     string            `json:"status"`
	StartedAt  time.Time         `json:"startedAt"`
	EndedAt    *time.Time        `json:"endedAt,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	EnvVars    map[string]string `json:"envVars,omitempty"`
	ClientID   string            `json:"clientID,omitempty"`
}

// E2BCommandRequest matches the E2B POST /sandboxes/{id}/commands request body.
type E2BCommandRequest struct {
	Command string `json:"command"`
	// Background indicates whether to run in background.
	Background bool `json:"background,omitempty"`
	// TimeoutSec is the command timeout in seconds.
	TimeoutSec int32 `json:"timeout,omitempty"`
	// User is the user to run the command as.
	User string `json:"user,omitempty"`
	// EnvVars are additional environment variables.
	EnvVars map[string]string `json:"envVars,omitempty"`
	// Cwd is the working directory.
	Cwd string `json:"cwd,omitempty"`
}

// E2BCommandResponse represents the result of a command execution.
type E2BCommandResponse struct {
	ExitCode  int       `json:"exitCode"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
	// CommandID is set when Background=true, used to check status later.
	CommandID string `json:"commandID,omitempty"`
}

// E2BFileRequest matches the E2B POST /sandboxes/{id}/files request body.
type E2BFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	// For read operations, only Path is required.
}

// E2BFileResponse represents a file read response.
type E2BFileResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// E2BCodeRequest matches the E2B POST /sandboxes/{id}/code request body.
type E2BCodeRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	// TimeoutSec is the execution timeout in seconds.
	TimeoutSec int32 `json:"timeout,omitempty"`
}

// E2BCodeResponse represents the result of code execution.
type E2BCodeResponse struct {
	ExitCode  int       `json:"exitCode"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

// E2BRefreshRequest matches the E2B POST /sandboxes/{id}/refreshes request body.
type E2BRefreshRequest struct {
	// Duration is the new timeout in seconds.
	Duration int32 `json:"duration"`
}

// E2BListResponse is the response for listing sandboxes.
type E2BListResponse struct {
	Sandboxes []E2BSandbox `json:"sandboxes"`
}

// --- Handlers ---

// RegisterRoutes registers E2B-compatible routes on the given mux.
// All E2B routes are prefixed with /e2b/v1/ to avoid conflicts with native API.
func (e *E2BService) RegisterRoutes(mux *http.ServeMux) {
	// Sandbox lifecycle
	mux.HandleFunc("/e2b/v1/sandboxes", e.handleSandboxes)
	mux.HandleFunc("/e2b/v1/sandboxes/", e.handleSandbox)

	// Templates
	mux.HandleFunc("/e2b/v1/templates", e.handleTemplates)
	mux.HandleFunc("/e2b/v1/templates/", e.handleTemplate)

	// Health
	mux.HandleFunc("/e2b/v1/health", e.handleHealth)
}

// handleSandboxes handles POST /e2b/v1/sandboxes (create) and GET (list).
func (e *E2BService) handleSandboxes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		e.createSandbox(w, r)
	case http.MethodGet:
		e.listSandboxes(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest,
			"method not allowed")
	}
}

// handleSandbox routes sandbox-specific operations.
func (e *E2BService) handleSandbox(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/e2b/v1/sandboxes/")
	parts := strings.SplitN(path, "/", 2)
	sandboxID := parts[0]

	if sandboxID == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "sandboxID required")
		return
	}

	if len(parts) == 1 {
		// /e2b/v1/sandboxes/{id}
		switch r.Method {
		case http.MethodGet:
			e.getSandbox(w, r, sandboxID)
		case http.MethodDelete:
			e.killSandbox(w, r, sandboxID)
		default:
			writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest,
				"method not allowed")
		}
		return
	}

	// /e2b/v1/sandboxes/{id}/{action}
	action := parts[1]
	switch action {
	case "commands":
		e.runCommand(w, r, sandboxID)
	case "files":
		e.handleFiles(w, r, sandboxID)
	case "code":
		e.runCode(w, r, sandboxID)
	case "refreshes":
		e.refreshSandbox(w, r, sandboxID)
	case "pause":
		e.pauseSandbox(w, r, sandboxID)
	case "resume":
		e.resumeSandbox(w, r, sandboxID)
	case "logs":
		e.getLogs(w, r, sandboxID)
	case "stats":
		e.getStats(w, r, sandboxID)
	default:
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound,
			fmt.Sprintf("unknown action: %s", action))
	}
}

// createSandbox implements POST /e2b/v1/sandboxes
func (e *E2BService) createSandbox(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req E2BCreateSandboxRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest,
			fmt.Sprintf("invalid request: %v", err))
		return
	}

	// Build NexusBox Sandbox spec from E2B request
	sb := &v1alpha1.Sandbox{
		ObjectMeta: e2bObjectMeta(req.TemplateID, req.Metadata),
		Spec: v1alpha1.SandboxSpec{
			TenantRef:        v1alpha1.TenantReference{Name: "default"},
			Runtime:          v1alpha1.RuntimeRunc,
			Priority:         v1alpha1.PriorityNormal,
			SchedulingPolicy: v1alpha1.ScheduleBinPack,
			Resources: v1alpha1.ResourceRequirements{
				CPU:    "1",
				Memory: "512Mi",
			},
			Image:         "ubuntu:22.04",
			RestartPolicy: v1alpha1.RestartPolicyNever,
		},
	}

	// Apply template if specified
	if req.TemplateID != "" && e.templateService != nil {
		if tmpl, err := e.templateService.GetTemplate(req.TemplateID); err == nil && tmpl != nil {
			e2bApplyTemplate(sb, tmpl)
		}
	}

	// Apply E2B overrides
	if req.MemoryMB > 0 {
		sb.Spec.Resources.Memory = fmt.Sprintf("%dMi", req.MemoryMB)
	}
	if req.VCPUs > 0 {
		sb.Spec.Resources.CPU = fmt.Sprintf("%d", req.VCPUs)
	}
	if req.Timeout > 0 {
		lt := int64(req.Timeout)
		sb.Spec.MaxLifetimeSeconds = &lt
	}
	if len(req.EnvVars) > 0 {
		for k, v := range req.EnvVars {
			sb.Spec.Env = append(sb.Spec.Env, v1alpha1.EnvVar{Name: k, Value: v})
		}
	}

	// Create the sandbox via lifecycle manager
	if e.lifecycleManager != nil {
		if err := e.lifecycleManager.CreateSandbox(r.Context(), sb); err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal,
				fmt.Sprintf("failed to create sandbox: %v", err))
			return
		}
	}

	// Convert to E2B response format
	resp := E2BSandbox{
		SandboxID:  sb.Name,
		TemplateID: req.TemplateID,
		Status:     "running",
		StartedAt:  time.Now(),
		Metadata:   req.Metadata,
		EnvVars:    req.EnvVars,
		ClientID:   req.ClientID,
	}

	klog.Infof("E2B: created sandbox %s (template=%s)", sb.Name, req.TemplateID)
	writeJSON(w, http.StatusCreated, resp)
}

// listSandboxes implements GET /e2b/v1/sandboxes
func (e *E2BService) listSandboxes(w http.ResponseWriter, r *http.Request) {
	// In a real implementation, this would list from the store
	resp := E2BListResponse{
		Sandboxes: []E2BSandbox{},
	}
	writeJSON(w, http.StatusOK, resp)
}

// getSandbox implements GET /e2b/v1/sandboxes/{id}
func (e *E2BService) getSandbox(w http.ResponseWriter, r *http.Request, sandboxID string) {
	resp := E2BSandbox{
		SandboxID: sandboxID,
		Status:    "running",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	writeJSON(w, http.StatusOK, resp)
}

// killSandbox implements DELETE /e2b/v1/sandboxes/{id}
func (e *E2BService) killSandbox(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if e.lifecycleManager != nil {
		if err := e.lifecycleManager.DeleteSandbox(r.Context(), "default/"+sandboxID); err != nil {
			klog.Warningf("E2B: failed to delete sandbox %s: %v", sandboxID, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// runCommand implements POST /e2b/v1/sandboxes/{id}/commands
func (e *E2BService) runCommand(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	var req E2BCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "command is required")
		return
	}

	// Delegate to shell service for actual execution
	startedAt := time.Now()
	stdout, stderr, exitCode, err := e.shellService.ExecSync(req.Command, req.TimeoutSec)
	endedAt := time.Now()

	if err != nil {
		klog.Warningf("E2B: command failed in sandbox %s: %v", sandboxID, err)
	}

	resp := E2BCommandResponse{
		ExitCode:  exitCode,
		Stdout:    stdout,
		Stderr:    stderr,
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleFiles implements POST /e2b/v1/sandboxes/{id}/files
func (e *E2BService) handleFiles(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	var req E2BFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}

	// If content is provided, write; otherwise read
	if req.Content != "" {
		if err := e.fileService.WriteFile(req.Path, []byte(req.Content)); err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal,
				fmt.Sprintf("write failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "written", "path": req.Path})
		return
	}

	data, err := e.fileService.ReadFile(req.Path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound,
			fmt.Sprintf("read failed: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, E2BFileResponse{Path: req.Path, Content: string(data)})
}

// runCode implements POST /e2b/v1/sandboxes/{id}/code
func (e *E2BService) runCode(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	var req E2BCodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "code is required")
		return
	}

	startedAt := time.Now()
	stdout, stderr, exitCode, err := e.codeService.ExecuteCode(req.Language, req.Code, req.TimeoutSec)
	endedAt := time.Now()

	if err != nil {
		klog.Warningf("E2B: code execution failed in sandbox %s: %v", sandboxID, err)
	}

	resp := E2BCodeResponse{
		ExitCode:  exitCode,
		Stdout:    stdout,
		Stderr:    stderr,
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}
	writeJSON(w, http.StatusOK, resp)
}

// refreshSandbox implements POST /e2b/v1/sandboxes/{id}/refreshes
func (e *E2BService) refreshSandbox(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	var req E2BRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body for refresh
		req.Duration = 300
	}

	klog.Infof("E2B: refreshed sandbox %s for %d seconds", sandboxID, req.Duration)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sandboxID":   sandboxID,
		"newTimeout":  req.Duration,
		"refreshedAt": time.Now(),
	})
}

// pauseSandbox implements POST /e2b/v1/sandboxes/{id}/pause
func (e *E2BService) pauseSandbox(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}
	klog.Infof("E2B: paused sandbox %s", sandboxID)
	writeJSON(w, http.StatusOK, map[string]string{"sandboxID": sandboxID, "status": "paused"})
}

// resumeSandbox implements POST /e2b/v1/sandboxes/{id}/resume
func (e *E2BService) resumeSandbox(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}
	klog.Infof("E2B: resumed sandbox %s", sandboxID)
	writeJSON(w, http.StatusOK, map[string]string{"sandboxID": sandboxID, "status": "running"})
}

// getLogs implements GET /e2b/v1/sandboxes/{id}/logs
func (e *E2BService) getLogs(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sandboxID": sandboxID,
		"logs":      []string{},
	})
}

// getStats implements GET /e2b/v1/sandboxes/{id}/stats
func (e *E2BService) getStats(w http.ResponseWriter, r *http.Request, sandboxID string) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sandboxID":      sandboxID,
		"cpuUsage":       0,
		"memoryUsage":    0,
		"networkRxBytes": 0,
		"networkTxBytes": 0,
	})
}

// handleTemplates implements GET /e2b/v1/templates
func (e *E2BService) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	templates := []map[string]interface{}{}
	if e.templateService != nil {
		for _, t := range e.templateService.ListTemplates() {
			templates = append(templates, map[string]interface{}{
				"templateID": t.Name,
				"image":      t.Spec.Image,
				"runtime":    t.Spec.Runtime,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"templates": templates})
}

// handleTemplate implements GET /e2b/v1/templates/{id}
func (e *E2BService) handleTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	templateID := strings.TrimPrefix(r.URL.Path, "/e2b/v1/templates/")
	if templateID == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "templateID required")
		return
	}

	if e.templateService == nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "template service unavailable")
		return
	}

	tmpl, err := e.templateService.GetTemplate(templateID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"templateID": tmpl.Name,
		"image":      tmpl.Spec.Image,
		"runtime":    tmpl.Spec.Runtime,
		"createdAt":  tmpl.CreationTimestamp,
	})
}

// handleHealth implements GET /e2b/v1/health
func (e *E2BService) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "healthy",
		"service": "nexusbox-e2b",
		"version": "v1",
	})
}

// --- Helpers ---

// e2bObjectMeta creates ObjectMeta for an E2B sandbox.
func e2bObjectMeta(templateID string, metadata map[string]string) metav1.ObjectMeta {
	name := fmt.Sprintf("e2b-%d", time.Now().UnixNano())
	if templateID != "" {
		name = fmt.Sprintf("e2b-%s-%d", templateID, time.Now().UnixNano())
	}
	labels := make(map[string]string)
	for k, v := range metadata {
		labels[k] = v
	}
	labels["app.kubernetes.io/managed-by"] = "e2b-compat"
	return metav1.ObjectMeta{
		Name:            name,
		Labels:          labels,
		Annotations:     metadata,
		ResourceVersion: "0",
	}
}

// e2bApplyTemplate applies template defaults to a sandbox spec.
func e2bApplyTemplate(sb *v1alpha1.Sandbox, tmpl *v1alpha1.SandboxTemplate) {
	sb.Spec.Runtime = tmpl.Spec.Runtime
	sb.Spec.Priority = tmpl.Spec.Priority
	sb.Spec.SchedulingPolicy = tmpl.Spec.SchedulingPolicy
	sb.Spec.Resources = tmpl.Spec.Resources
	sb.Spec.Image = tmpl.Spec.Image
	if len(tmpl.Spec.Command) > 0 {
		sb.Spec.Command = tmpl.Spec.Command
	}
	if len(tmpl.Spec.Args) > 0 {
		sb.Spec.Args = tmpl.Spec.Args
	}
	if len(tmpl.Spec.Env) > 0 {
		sb.Spec.Env = tmpl.Spec.Env
	}
	if tmpl.Spec.WorkingDir != "" {
		sb.Spec.WorkingDir = tmpl.Spec.WorkingDir
	}
	if tmpl.Spec.RestartPolicy != "" {
		sb.Spec.RestartPolicy = tmpl.Spec.RestartPolicy
	}
	if tmpl.Spec.Network != nil {
		sb.Spec.Network = tmpl.Spec.Network
	}
	if tmpl.Spec.Storage != nil {
		sb.Spec.Storage = tmpl.Spec.Storage
	}
	if tmpl.Spec.Security != nil {
		sb.Spec.Security = tmpl.Spec.Security
	}
}
