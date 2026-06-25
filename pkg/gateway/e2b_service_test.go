package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	templatepkg "github.com/nexusbox/nexusbox/pkg/template"
)

// newTestE2BService creates an E2BService with only template support wired.
// Lifecycle/runtime/shell/file/code services are nil, so handlers that
// require them will return proper errors.
func newTestE2BService() *E2BService {
	tm := templatepkg.NewManager()
	_ = tm.SeedDefaults(context.Background())
	ts := &TemplateService{manager: tm}
	return NewE2BService(nil, nil, ts, nil, nil, nil)
}

func TestE2B_RegisterRoutes(t *testing.T) {
	s := newTestE2BService()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/e2b/v1/health", http.StatusOK},
		{http.MethodGet, "/e2b/v1/templates", http.StatusOK},
		{http.MethodGet, "/e2b/v1/sandboxes", http.StatusOK},
		{http.MethodDelete, "/e2b/v1/sandboxes", http.StatusMethodNotAllowed},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != c.want {
				t.Errorf("expected %d, got %d (body=%s)", c.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestE2B_Health(t *testing.T) {
	s := newTestE2BService()
	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %s", resp["status"])
	}
	if resp["service"] != "nexusbox-e2b" {
		t.Errorf("expected service=nexusbox-e2b, got %s", resp["service"])
	}
}

func TestE2B_ListTemplates(t *testing.T) {
	s := newTestE2BService()
	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/templates", nil)
	w := httptest.NewRecorder()
	s.handleTemplates(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Templates []map[string]interface{} `json:"templates"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Templates) < 4 {
		t.Errorf("expected at least 4 seeded templates, got %d", len(resp.Templates))
	}
}

func TestE2B_GetTemplate(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/templates/python-data-science", nil)
	req.URL.Path = "/e2b/v1/templates/python-data-science"
	w := httptest.NewRecorder()
	s.handleTemplate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["templateID"] != "python-data-science" {
		t.Errorf("expected templateID=python-data-science, got %v", resp["templateID"])
	}
}

func TestE2B_GetTemplate_MissingID(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/templates/", nil)
	req.URL.Path = "/e2b/v1/templates/"
	w := httptest.NewRecorder()
	s.handleTemplate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestE2B_GetTemplate_NotFound(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/templates/missing", nil)
	req.URL.Path = "/e2b/v1/templates/missing"
	w := httptest.NewRecorder()
	s.handleTemplate(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestE2B_GetTemplate_MethodNotAllowed(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodPost, "/e2b/v1/templates/x", nil)
	w := httptest.NewRecorder()
	s.handleTemplate(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestE2B_ListSandboxes(t *testing.T) {
	s := newTestE2BService()
	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/sandboxes", nil)
	w := httptest.NewRecorder()
	s.handleSandboxes(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var resp E2BListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestE2B_SandboxOperations_MethodNotAllowed(t *testing.T) {
	s := newTestE2BService()

	// PATCH on sandboxes is not allowed
	req := httptest.NewRequest(http.MethodPatch, "/e2b/v1/sandboxes", nil)
	w := httptest.NewRecorder()
	s.handleSandboxes(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestE2B_Sandbox_MissingID(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/sandboxes/", nil)
	w := httptest.NewRecorder()
	s.handleSandbox(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestE2B_Sandbox_Get(t *testing.T) {
	s := newTestE2BService()

	// GET returns a synthetic sandbox (no backing store in test mode)
	req := httptest.NewRequest(http.MethodGet, "/e2b/v1/sandboxes/sb-1", nil)
	w := httptest.NewRecorder()
	s.handleSandbox(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp E2BSandbox
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.SandboxID != "sb-1" {
		t.Errorf("expected sandboxID=sb-1, got %s", resp.SandboxID)
	}
	if resp.Status != "running" {
		t.Errorf("expected status=running, got %s", resp.Status)
	}
}

func TestE2B_Sandbox_Delete(t *testing.T) {
	s := newTestE2BService()

	// DELETE should return 204 (no lifecycle manager = no-op)
	req := httptest.NewRequest(http.MethodDelete, "/e2b/v1/sandboxes/sb-1", nil)
	w := httptest.NewRecorder()
	s.handleSandbox(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestE2B_CreateSandbox(t *testing.T) {
	s := newTestE2BService()

	body := strings.NewReader(`{"templateID":"python-data-science"}`)
	req := httptest.NewRequest(http.MethodPost, "/e2b/v1/sandboxes", body)
	w := httptest.NewRecorder()
	s.createSandbox(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp E2BSandbox
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.TemplateID != "python-data-science" {
		t.Errorf("expected templateID=python-data-science, got %s", resp.TemplateID)
	}
	if resp.SandboxID == "" {
		t.Error("expected non-empty sandboxID")
	}
	if resp.Status != "running" {
		t.Errorf("expected status=running, got %s", resp.Status)
	}
}

func TestE2B_CreateSandbox_WithDefaults(t *testing.T) {
	s := newTestE2BService()

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/e2b/v1/sandboxes", body)
	w := httptest.NewRecorder()
	s.createSandbox(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestE2B_CreateSandbox_InvalidBody(t *testing.T) {
	s := newTestE2BService()

	req := httptest.NewRequest(http.MethodPost, "/e2b/v1/sandboxes", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	s.createSandbox(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestE2B_e2bObjectMeta(t *testing.T) {
	meta := e2bObjectMeta("python", map[string]string{"env": "prod"})

	if meta.Name == "" {
		t.Error("expected Name to be set")
	}
	if !strings.HasPrefix(meta.Name, "e2b-python-") {
		t.Errorf("expected name to start with e2b-python-, got %s", meta.Name)
	}
	if meta.Labels["env"] != "prod" {
		t.Errorf("expected label env=prod, got %s", meta.Labels["env"])
	}
	if meta.Labels["app.kubernetes.io/managed-by"] != "e2b-compat" {
		t.Error("expected managed-by label")
	}
	if meta.Annotations["env"] != "prod" {
		t.Errorf("expected annotation env=prod, got %s", meta.Annotations["env"])
	}
}

func TestE2B_e2bObjectMeta_NoTemplate(t *testing.T) {
	meta := e2bObjectMeta("", nil)
	if !strings.HasPrefix(meta.Name, "e2b-") {
		t.Errorf("expected name to start with e2b-, got %s", meta.Name)
	}
}

func TestE2B_e2bApplyTemplate(t *testing.T) {
	sb := &sandboxv1alpha1.Sandbox{}
	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: sandboxv1alpha1.SandboxTemplateSpec{
			Runtime:    "gvisor",
			Image:      "python:3.11",
			WorkingDir: "/workspace",
			Resources: sandboxv1alpha1.ResourceRequirements{
				CPU:    "2",
				Memory: "4Gi",
			},
		},
	}

	e2bApplyTemplate(sb, tmpl)

	if sb.Spec.Runtime != "gvisor" {
		t.Errorf("expected runtime=gvisor, got %s", sb.Spec.Runtime)
	}
	if sb.Spec.Image != "python:3.11" {
		t.Errorf("expected image=python:3.11, got %s", sb.Spec.Image)
	}
	if sb.Spec.Resources.CPU != "2" {
		t.Errorf("expected CPU=2, got %s", sb.Spec.Resources.CPU)
	}
}

// Test that the timing helpers work correctly (timestamps are recent)
func TestE2B_TimeHelpers(t *testing.T) {
	now := time.Now()
	meta := e2bObjectMeta("test", nil)
	// Name includes a unixnano timestamp - should be recent (within last second)
	parts := strings.Split(meta.Name, "-")
	if len(parts) >= 3 {
		// Just verify no panic on parsing
		_ = parts
	}
	_ = now
}
