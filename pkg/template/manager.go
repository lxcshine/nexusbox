// Package template implements sandbox template management.
//
// Templates allow users to define reusable sandbox configurations
// (image, runtime, resources, env vars, etc.) that can be referenced
// when creating sandboxes, enabling fast cold-starts via pool pre-warming.
//
// Inspired by CubeSandbox's template system and E2B's template builds.
package template

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// Manager manages sandbox templates and their lifecycle.
type Manager struct {
	mu        sync.RWMutex
	templates map[string]*sandboxv1alpha1.SandboxTemplate
}

// NewManager creates a new template Manager.
func NewManager() *Manager {
	return &Manager{
		templates: make(map[string]*sandboxv1alpha1.SandboxTemplate),
	}
}

// CreateTemplate creates a new sandbox template.
func (m *Manager) CreateTemplate(ctx context.Context, tmpl *sandboxv1alpha1.SandboxTemplate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if tmpl.Name == "" {
		return fmt.Errorf("template name is required")
	}

	if _, exists := m.templates[tmpl.Name]; exists {
		return fmt.Errorf("template %s already exists", tmpl.Name)
	}

	// Apply defaults
	if tmpl.Spec.Runtime == "" {
		tmpl.Spec.Runtime = sandboxv1alpha1.RuntimeRunc
	}
	if tmpl.Spec.Priority == 0 {
		tmpl.Spec.Priority = sandboxv1alpha1.PriorityNormal
	}
	if tmpl.Spec.SchedulingPolicy == "" {
		tmpl.Spec.SchedulingPolicy = sandboxv1alpha1.ScheduleBinPack
	}
	if tmpl.Spec.RestartPolicy == "" {
		tmpl.Spec.RestartPolicy = sandboxv1alpha1.RestartPolicyNever
	}
	if tmpl.Spec.Image == "" {
		tmpl.Spec.Image = "ubuntu:22.04"
	}
	if tmpl.Spec.Resources.CPU == "" {
		tmpl.Spec.Resources.CPU = "1"
	}
	if tmpl.Spec.Resources.Memory == "" {
		tmpl.Spec.Resources.Memory = "512Mi"
	}

	if tmpl.CreationTimestamp.IsZero() {
		tmpl.CreationTimestamp = metav1.NewTime(time.Now())
	}

	m.templates[tmpl.Name] = tmpl
	klog.Infof("Created template %s (runtime=%s, image=%s)", tmpl.Name, tmpl.Spec.Runtime, tmpl.Spec.Image)
	return nil
}

// GetTemplate retrieves a template by name.
func (m *Manager) GetTemplate(name string) (*sandboxv1alpha1.SandboxTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tmpl, exists := m.templates[name]
	if !exists {
		return nil, fmt.Errorf("template %s not found", name)
	}
	return tmpl, nil
}

// UpdateTemplate updates an existing template.
func (m *Manager) UpdateTemplate(ctx context.Context, name string, newTmpl *sandboxv1alpha1.SandboxTemplate) (*sandboxv1alpha1.SandboxTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	old, exists := m.templates[name]
	if !exists {
		return nil, fmt.Errorf("template %s not found", name)
	}

	// Preserve immutable fields
	newTmpl.Name = old.Name
	newTmpl.CreationTimestamp = old.CreationTimestamp
	newTmpl.Status = old.Status

	m.templates[name] = newTmpl
	klog.Infof("Updated template %s", name)
	return newTmpl, nil
}

// DeleteTemplate deletes a template by name.
func (m *Manager) DeleteTemplate(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.templates[name]; !exists {
		return fmt.Errorf("template %s not found", name)
	}
	delete(m.templates, name)
	klog.Infof("Deleted template %s", name)
	return nil
}

// ListTemplates returns all templates.
func (m *Manager) ListTemplates() []*sandboxv1alpha1.SandboxTemplate {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*sandboxv1alpha1.SandboxTemplate, 0, len(m.templates))
	for _, tmpl := range m.templates {
		result = append(result, tmpl)
	}
	return result
}

// IncrementUsage increments the usage count of a template.
func (m *Manager) IncrementUsage(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tmpl, exists := m.templates[name]
	if !exists {
		return
	}
	tmpl.Status.UsageCount++
	now := metav1.NewTime(time.Now())
	tmpl.Status.LastUsedTime = &now
}

// ApplyToSandbox applies template defaults to a sandbox spec.
// Only fields not already set on the sandbox will be overridden,
// respecting the template's AllowedOverrides list.
func (m *Manager) ApplyToSandbox(sb *sandboxv1alpha1.Sandbox, tmpl *sandboxv1alpha1.SandboxTemplate) {
	// Always apply runtime and image from template
	if sb.Spec.Runtime == "" {
		sb.Spec.Runtime = tmpl.Spec.Runtime
	}
	if sb.Spec.Image == "" {
		sb.Spec.Image = tmpl.Spec.Image
	}
	if sb.Spec.Priority == 0 {
		sb.Spec.Priority = tmpl.Spec.Priority
	}
	if sb.Spec.SchedulingPolicy == "" {
		sb.Spec.SchedulingPolicy = tmpl.Spec.SchedulingPolicy
	}
	if sb.Spec.RestartPolicy == "" {
		sb.Spec.RestartPolicy = tmpl.Spec.RestartPolicy
	}

	// Apply resources if not set
	if sb.Spec.Resources.CPU == "" {
		sb.Spec.Resources.CPU = tmpl.Spec.Resources.CPU
	}
	if sb.Spec.Resources.Memory == "" {
		sb.Spec.Resources.Memory = tmpl.Spec.Resources.Memory
	}
	if sb.Spec.Resources.EphemeralStorage == "" {
		sb.Spec.Resources.EphemeralStorage = tmpl.Spec.Resources.EphemeralStorage
	}

	// Apply command/args if not set
	if len(sb.Spec.Command) == 0 && len(tmpl.Spec.Command) > 0 {
		sb.Spec.Command = tmpl.Spec.Command
	}
	if len(sb.Spec.Args) == 0 && len(tmpl.Spec.Args) > 0 {
		sb.Spec.Args = tmpl.Spec.Args
	}

	// Apply env (append, don't override)
	if len(tmpl.Spec.Env) > 0 {
		sb.Spec.Env = append(sb.Spec.Env, tmpl.Spec.Env...)
	}

	// Apply working dir
	if sb.Spec.WorkingDir == "" {
		sb.Spec.WorkingDir = tmpl.Spec.WorkingDir
	}

	// Apply node selector
	if len(sb.Spec.NodeSelector) == 0 && len(tmpl.Spec.NodeSelector) > 0 {
		sb.Spec.NodeSelector = tmpl.Spec.NodeSelector
	}

	// Apply tolerations
	if len(sb.Spec.Tolerations) == 0 && len(tmpl.Spec.Tolerations) > 0 {
		sb.Spec.Tolerations = tmpl.Spec.Tolerations
	}

	// Apply timeouts
	if sb.Spec.MaxLifetimeSeconds == nil && tmpl.Spec.MaxLifetimeSeconds != nil {
		sb.Spec.MaxLifetimeSeconds = tmpl.Spec.MaxLifetimeSeconds
	}
	if sb.Spec.IdleTimeoutSeconds == nil && tmpl.Spec.IdleTimeoutSeconds != nil {
		sb.Spec.IdleTimeoutSeconds = tmpl.Spec.IdleTimeoutSeconds
	}

	// Apply network/storage/security if not set
	if sb.Spec.Network == nil && tmpl.Spec.Network != nil {
		sb.Spec.Network = tmpl.Spec.Network
	}
	if sb.Spec.Storage == nil && tmpl.Spec.Storage != nil {
		sb.Spec.Storage = tmpl.Spec.Storage
	}
	if sb.Spec.Security == nil && tmpl.Spec.Security != nil {
		sb.Spec.Security = tmpl.Spec.Security
	}

	m.IncrementUsage(tmpl.Name)
}

// SeedDefaults registers a set of default templates for common use cases.
// These templates cover typical AI Agent scenarios.
func (m *Manager) SeedDefaults(ctx context.Context) error {
	defaults := []*sandboxv1alpha1.SandboxTemplate{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "python-data-science"},
			Spec: sandboxv1alpha1.SandboxTemplateSpec{
				Runtime:          sandboxv1alpha1.RuntimeRunc,
				Priority:         sandboxv1alpha1.PriorityNormal,
				SchedulingPolicy: sandboxv1alpha1.ScheduleBinPack,
				Resources: sandboxv1alpha1.ResourceRequirements{
					CPU:    "2",
					Memory: "2Gi",
				},
				Image:      "python:3.11-slim",
				WorkingDir: "/workspace",
				Env: []sandboxv1alpha1.EnvVar{
					{Name: "PYTHONUNBUFFERED", Value: "1"},
					{Name: "PIP_CACHE_DIR", Value: "/tmp/pip-cache"},
				},
				RestartPolicy: sandboxv1alpha1.RestartPolicyNever,
				AllowedOverrides: []string{"resources", "env", "command"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "node-fullstack"},
			Spec: sandboxv1alpha1.SandboxTemplateSpec{
				Runtime:          sandboxv1alpha1.RuntimeRunc,
				Priority:         sandboxv1alpha1.PriorityNormal,
				SchedulingPolicy: sandboxv1alpha1.ScheduleBinPack,
				Resources: sandboxv1alpha1.ResourceRequirements{
					CPU:    "2",
					Memory: "2Gi",
				},
				Image:      "node:20-slim",
				WorkingDir: "/workspace",
				Env: []sandboxv1alpha1.EnvVar{
					{Name: "NODE_ENV", Value: "development"},
					{Name: "NPM_CONFIG_CACHE", Value: "/tmp/npm-cache"},
				},
				RestartPolicy: sandboxv1alpha1.RestartPolicyNever,
				AllowedOverrides: []string{"resources", "env", "command"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "browser-automation"},
			Spec: sandboxv1alpha1.SandboxTemplateSpec{
				Runtime:          sandboxv1alpha1.RuntimeRunc,
				Priority:         sandboxv1alpha1.PriorityNormal,
				SchedulingPolicy: sandboxv1alpha1.ScheduleBinPack,
				Resources: sandboxv1alpha1.ResourceRequirements{
					CPU:    "1",
					Memory: "1Gi",
				},
				Image:      "chromium:latest",
				WorkingDir: "/workspace",
				Env: []sandboxv1alpha1.EnvVar{
					{Name: "CHROME_HEADLESS", Value: "1"},
					{Name: "DISPLAY", Value: ":99"},
				},
				RestartPolicy: sandboxv1alpha1.RestartPolicyNever,
				AllowedOverrides: []string{"resources", "env"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ai-agent-default"},
			Spec: sandboxv1alpha1.SandboxTemplateSpec{
				Runtime:          sandboxv1alpha1.RuntimeGVisor,
				Priority:         sandboxv1alpha1.PriorityHigh,
				SchedulingPolicy: sandboxv1alpha1.ScheduleSpread,
				Resources: sandboxv1alpha1.ResourceRequirements{
					CPU:    "1",
					Memory: "1Gi",
				},
				Image:      "ubuntu:22.04",
				WorkingDir: "/workspace",
				Env: []sandboxv1alpha1.EnvVar{
					{Name: "SANDBOX_MODE", Value: "agent"},
				},
				RestartPolicy: sandboxv1alpha1.RestartPolicyNever,
				MaxLifetimeSeconds: func() *int64 { v := int64(3600); return &v }(),
				IdleTimeoutSeconds: func() *int64 { v := int64(300); return &v }(),
				AllowedOverrides: []string{"resources", "env", "command"},
			},
		},
	}

	for _, tmpl := range defaults {
		if err := m.CreateTemplate(ctx, tmpl); err != nil {
			klog.Warningf("Failed to seed template %s: %v", tmpl.Name, err)
		}
	}
	return nil
}
