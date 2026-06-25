package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/template"
)

// TemplateService exposes sandbox template management via REST API.
type TemplateService struct {
	manager *template.Manager
}

// NewTemplateService creates a new TemplateService.
func NewTemplateService(mgr *template.Manager) *TemplateService {
	return &TemplateService{manager: mgr}
}

// RegisterRoutes registers template routes on the given mux.
func (s *TemplateService) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/templates", s.handleTemplates)
	mux.HandleFunc("/v1/templates/", s.handleTemplate)
}

// handleTemplates handles collection-level operations (list, create).
func (s *TemplateService) handleTemplates(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.List(w, r)
	case http.MethodPost:
		s.Create(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
	}
}

// handleTemplate handles item-level operations (get, update, delete).
func (s *TemplateService) handleTemplate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/templates/")
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "template name required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.Get(w, r, name)
	case http.MethodPut:
		s.Update(w, r, name)
	case http.MethodDelete:
		s.Delete(w, r, name)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
	}
}

// List lists all templates.
func (s *TemplateService) List(w http.ResponseWriter, r *http.Request) {
	templates := s.manager.ListTemplates()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": templates,
		"count": len(templates),
	})
}

// Create creates a new template.
func (s *TemplateService) Create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var tmpl v1alpha1.SandboxTemplate
	if err := json.Unmarshal(body, &tmpl); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest,
			fmt.Sprintf("invalid request: %v", err))
		return
	}

	if tmpl.Name == "" && tmpl.ObjectMeta.Name != "" {
		tmpl.Name = tmpl.ObjectMeta.Name
	}
	if tmpl.Name == "" {
		// Try to extract from raw JSON
		var raw map[string]interface{}
		json.Unmarshal(body, &raw)
		if n, ok := raw["name"].(string); ok {
			tmpl.Name = n
			tmpl.ObjectMeta.Name = n
		}
	}

	if err := s.manager.CreateTemplate(r.Context(), &tmpl); err != nil {
		writeAPIError(w, http.StatusConflict, ErrCodeConflict, err.Error())
		return
	}

	klog.Infof("Created template %s via API", tmpl.Name)
	writeJSON(w, http.StatusCreated, tmpl)
}

// Get retrieves a template by name.
func (s *TemplateService) Get(w http.ResponseWriter, r *http.Request, name string) {
	tmpl, err := s.manager.GetTemplate(name)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tmpl)
}

// Update updates an existing template.
func (s *TemplateService) Update(w http.ResponseWriter, r *http.Request, name string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var tmpl v1alpha1.SandboxTemplate
	if err := json.Unmarshal(body, &tmpl); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest,
			fmt.Sprintf("invalid request: %v", err))
		return
	}

	updated, err := s.manager.UpdateTemplate(r.Context(), name, &tmpl)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// Delete deletes a template.
func (s *TemplateService) Delete(w http.ResponseWriter, r *http.Request, name string) {
	if err := s.manager.DeleteTemplate(name); err != nil {
		writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

// GetTemplate is a convenience accessor for other services (e.g. E2B service).
func (s *TemplateService) GetTemplate(name string) (*v1alpha1.SandboxTemplate, error) {
	return s.manager.GetTemplate(name)
}

// ListTemplates is a convenience accessor for other services.
func (s *TemplateService) ListTemplates() []*v1alpha1.SandboxTemplate {
	return s.manager.ListTemplates()
}
