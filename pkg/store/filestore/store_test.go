package filestore

import (
	"os"
	"path/filepath"
	"testing"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Stop()

	if store.dataPath != filepath.Join(tmpDir, "state.json") {
		t.Errorf("dataPath = %s, want %s", store.dataPath, filepath.Join(tmpDir, "state.json"))
	}
}

func TestSandboxCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	defer store.Stop()

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "test-sandbox"},
		Spec:       sandboxv1alpha1.SandboxSpec{Runtime: sandboxv1alpha1.RuntimeRunc},
	}

	// Create.
	if err := store.CreateSandbox(sb); err != nil {
		t.Fatalf("CreateSandbox failed: %v", err)
	}

	// Get.
	got, err := store.GetSandbox("test-sandbox")
	if err != nil {
		t.Fatalf("GetSandbox failed: %v", err)
	}
	if got.Name != "test-sandbox" {
		t.Errorf("Name = %s, want test-sandbox", got.Name)
	}

	// List.
	list, err := store.ListSandboxes()
	if err != nil {
		t.Fatalf("ListSandboxes failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListSandboxes len = %d, want 1", len(list))
	}

	// Update.
	got.Spec.Image = "python:3.11"
	if err := store.UpdateSandbox(got); err != nil {
		t.Fatalf("UpdateSandbox failed: %v", err)
	}
	updated, _ := store.GetSandbox("test-sandbox")
	if updated.Spec.Image != "python:3.11" {
		t.Errorf("Image = %s, want python:3.11", updated.Spec.Image)
	}

	// Delete.
	if err := store.DeleteSandbox("test-sandbox"); err != nil {
		t.Fatalf("DeleteSandbox failed: %v", err)
	}
	_, err = store.GetSandbox("test-sandbox")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestTemplateCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	defer store.Stop()

	tmpl := &sandboxv1alpha1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "python-template"},
		Spec: sandboxv1alpha1.SandboxTemplateSpec{
			Image: "python:3.11-slim",
		},
	}

	if err := store.CreateTemplate(tmpl); err != nil {
		t.Fatalf("CreateTemplate failed: %v", err)
	}

	got, err := store.GetTemplate("python-template")
	if err != nil {
		t.Fatalf("GetTemplate failed: %v", err)
	}
	if got.Spec.Image != "python:3.11-slim" {
		t.Errorf("Image = %s, want python:3.11-slim", got.Spec.Image)
	}

	list, _ := store.ListTemplates()
	if len(list) != 1 {
		t.Errorf("ListTemplates len = %d, want 1", len(list))
	}

	if err := store.DeleteTemplate("python-template"); err != nil {
		t.Fatalf("DeleteTemplate failed: %v", err)
	}
}

func TestSnapshotCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	defer store.Stop()

	snap := &SnapshotRecord{
		ID:        "snap-1",
		SandboxID: "sandbox-1",
		Path:      "/tmp/snap1",
		Size:      1024,
	}

	if err := store.SaveSnapshot(snap); err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	got, err := store.GetSnapshot("snap-1")
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}
	if got.SandboxID != "sandbox-1" {
		t.Errorf("SandboxID = %s, want sandbox-1", got.SandboxID)
	}

	list, _ := store.ListSnapshots("sandbox-1")
	if len(list) != 1 {
		t.Errorf("ListSnapshots len = %d, want 1", len(list))
	}

	if err := store.DeleteSnapshot("snap-1"); err != nil {
		t.Fatalf("DeleteSnapshot failed: %v", err)
	}
}

func TestFlush(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	defer store.Stop()

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "persist-test"},
	}
	store.CreateSandbox(sb)

	if err := store.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(store.dataPath); err != nil {
		t.Fatalf("state file not created: %v", err)
	}
}

func TestLoadPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create and persist.
	store1, _ := NewStore(tmpDir)
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "persisted-sandbox"},
	}
	store1.CreateSandbox(sb)
	store1.Flush()
	store1.Stop()

	// Create new store, should load existing state.
	store2, _ := NewStore(tmpDir)
	defer store2.Stop()

	got, err := store2.GetSandbox("persisted-sandbox")
	if err != nil {
		t.Fatalf("GetSandbox after reload failed: %v", err)
	}
	if got.Name != "persisted-sandbox" {
		t.Errorf("Name = %s, want persisted-sandbox", got.Name)
	}
}

func TestTenantCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := NewStore(tmpDir)
	defer store.Stop()

	tenant := &sandboxv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tenant"},
	}

	if err := store.CreateTenant(tenant); err != nil {
		t.Fatalf("CreateTenant failed: %v", err)
	}

	got, err := store.GetTenant("test-tenant")
	if err != nil {
		t.Fatalf("GetTenant failed: %v", err)
	}
	if got.Name != "test-tenant" {
		t.Errorf("Name = %s, want test-tenant", got.Name)
	}

	list, _ := store.ListTenants()
	if len(list) != 1 {
		t.Errorf("ListTenants len = %d, want 1", len(list))
	}

	if err := store.DeleteTenant("test-tenant"); err != nil {
		t.Fatalf("DeleteTenant failed: %v", err)
	}
}
