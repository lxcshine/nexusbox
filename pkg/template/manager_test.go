package template

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if len(m.templates) != 0 {
		t.Fatalf("expected empty templates map, got %d", len(m.templates))
	}
}

func TestManager_CreateTemplate_DefaultsApplied(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tmpl"},
		Spec: sandboxv1alpha1.SandboxTemplateSpec{
			Runtime: "runc",
			Image:   "python:3.11",
		},
	}

	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	got, err := m.GetTemplate("test-tmpl")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}

	if got.Spec.Runtime != "runc" {
		t.Errorf("expected runtime=runc, got %s", got.Spec.Runtime)
	}
	if got.Spec.Image != "python:3.11" {
		t.Errorf("expected image=python:3.11, got %s", got.Spec.Image)
	}
	if got.Spec.SchedulingPolicy == "" {
		t.Error("expected SchedulingPolicy default to be set")
	}
	if got.Spec.RestartPolicy == "" {
		t.Error("expected RestartPolicy default to be set")
	}
	if got.Spec.Resources.CPU == "" {
		t.Error("expected CPU default to be set")
	}
	if got.Spec.Resources.Memory == "" {
		t.Error("expected Memory default to be set")
	}
}

func TestManager_CreateTemplate_RequiresName(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{}

	if err := m.CreateTemplate(ctx, tmpl); err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
}

func TestManager_CreateTemplate_DuplicateRejected(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "dup"},
		Spec:       sandboxv1alpha1.SandboxTemplateSpec{Runtime: "runc"},
	}

	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	if err := m.CreateTemplate(ctx, tmpl); err == nil {
		t.Fatal("expected duplicate create to fail, got nil")
	}
}

func TestManager_GetTemplate_NotFound(t *testing.T) {
	m := NewManager()

	if _, err := m.GetTemplate("nonexistent"); err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
}

func TestManager_UpdateTemplate(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "upd"},
		Spec:       sandboxv1alpha1.SandboxTemplateSpec{Runtime: "runc", Image: "ubuntu:22.04"},
	}
	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	updated := &sandboxv1alpha1.SandboxTemplate{
		Spec: sandboxv1alpha1.SandboxTemplateSpec{
			Runtime: "gvisor",
			Image:   "alpine:3.19",
		},
	}
	if _, err := m.UpdateTemplate(ctx, "upd", updated); err != nil {
		t.Fatalf("UpdateTemplate failed: %v", err)
	}

	got, err := m.GetTemplate("upd")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}
	if got.Spec.Image != "alpine:3.19" {
		t.Errorf("expected image=alpine:3.19, got %s", got.Spec.Image)
	}
	if got.Name != "upd" {
		t.Errorf("expected name to be preserved as 'upd', got %s", got.Name)
	}
}

func TestManager_UpdateTemplate_NotFound(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	if _, err := m.UpdateTemplate(ctx, "missing", &sandboxv1alpha1.SandboxTemplate{}); err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
}

func TestManager_DeleteTemplate(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "del"},
		Spec:       sandboxv1alpha1.SandboxTemplateSpec{Runtime: "runc"},
	}
	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	if err := m.DeleteTemplate("del"); err != nil {
		t.Fatalf("DeleteTemplate failed: %v", err)
	}

	if _, err := m.GetTemplate("del"); err == nil {
		t.Fatal("expected GetTemplate to fail after delete")
	}
}

func TestManager_DeleteTemplate_NotFound(t *testing.T) {
	m := NewManager()
	if err := m.DeleteTemplate("missing"); err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
}

func TestManager_ListTemplates(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	for _, name := range []string{"a", "b", "c"} {
		if err := m.CreateTemplate(ctx, &sandboxv1alpha1.SandboxTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       sandboxv1alpha1.SandboxTemplateSpec{Runtime: "runc"},
		}); err != nil {
			t.Fatalf("CreateTemplate %s failed: %v", name, err)
		}
	}

	list := m.ListTemplates()
	if len(list) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(list))
	}
}

func TestManager_IncrementUsage(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "use"},
		Spec:       sandboxv1alpha1.SandboxTemplateSpec{Runtime: "runc"},
	}
	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	m.IncrementUsage("use")
	m.IncrementUsage("use")
	m.IncrementUsage("use")

	got, err := m.GetTemplate("use")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}
	if got.Status.UsageCount != 3 {
		t.Errorf("expected UsageCount=3, got %d", got.Status.UsageCount)
	}
	if got.Status.LastUsedTime == nil {
		t.Error("expected LastUsedTime to be set")
	}
}

func TestManager_IncrementUsage_MissingTemplate(t *testing.T) {
	m := NewManager()

	// Should be a no-op, not panic
	m.IncrementUsage("missing")
}

func TestManager_ApplyToSandbox(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "apply"},
		Spec: sandboxv1alpha1.SandboxTemplateSpec{
			Runtime:   "gvisor",
			Image:     "python:3.11",
			WorkingDir: "/workspace",
			Env: []sandboxv1alpha1.EnvVar{
				{Name: "FOO", Value: "bar"},
			},
		},
	}
	if err := m.CreateTemplate(ctx, tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	// Empty sandbox should inherit everything
	sb := &sandboxv1alpha1.Sandbox{}
	m.ApplyToSandbox(sb, tmpl)

	if sb.Spec.Runtime != "gvisor" {
		t.Errorf("expected runtime=gvisor, got %s", sb.Spec.Runtime)
	}
	if sb.Spec.Image != "python:3.11" {
		t.Errorf("expected image=python:3.11, got %s", sb.Spec.Image)
	}
	if sb.Spec.WorkingDir != "/workspace" {
		t.Errorf("expected WorkingDir=/workspace, got %s", sb.Spec.WorkingDir)
	}

	// Pre-set fields should be preserved
	sb2 := &sandboxv1alpha1.Sandbox{
		Spec: sandboxv1alpha1.SandboxSpec{
			Runtime: "runc",
			Image:   "alpine:3.19",
		},
	}
	m.ApplyToSandbox(sb2, tmpl)
	if sb2.Spec.Runtime != "runc" {
		t.Errorf("expected runtime to remain runc, got %s", sb2.Spec.Runtime)
	}
	if sb2.Spec.Image != "alpine:3.19" {
		t.Errorf("expected image to remain alpine:3.19, got %s", sb2.Spec.Image)
	}
}

func TestManager_SeedDefaults(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	if err := m.SeedDefaults(ctx); err != nil {
		t.Fatalf("SeedDefaults failed: %v", err)
	}

	expected := []string{
		"python-data-science",
		"node-fullstack",
		"browser-automation",
		"ai-agent-default",
	}

	for _, name := range expected {
		if _, err := m.GetTemplate(name); err != nil {
			t.Errorf("expected template %s to exist after SeedDefaults: %v", name, err)
		}
	}

	if len(m.ListTemplates()) < len(expected) {
		t.Errorf("expected at least %d templates after seed, got %d", len(expected), len(m.ListTemplates()))
	}
}

func TestManager_SeedDefaults_Idempotent(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	// First seed
	if err := m.SeedDefaults(ctx); err != nil {
		t.Fatalf("first SeedDefaults failed: %v", err)
	}
	firstCount := len(m.ListTemplates())

	// Second seed should not duplicate
	if err := m.SeedDefaults(ctx); err != nil {
		t.Fatalf("second SeedDefaults failed: %v", err)
	}
	secondCount := len(m.ListTemplates())

	if firstCount != secondCount {
		t.Errorf("SeedDefaults not idempotent: first=%d, second=%d", firstCount, secondCount)
	}
}
