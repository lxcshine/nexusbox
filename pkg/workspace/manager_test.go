

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManager_CreateAndGet(t *testing.T) {
	m, err := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	w, err := m.Create(CreateOpts{
		ID:           "proj-a",
		OwnerSession: "sess-1",
		Quota:        Quota{DiskLimitBytes: 1024, MaxProcs: 4},
		Labels:       map[string]string{"team": "alpha"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ID != "proj-a" {
		t.Errorf("ID = %q, want proj-a", w.ID)
	}
	for _, dir := range []string{w.Root, w.DataDir, w.TmpDir, w.CacheDir} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("workspace dir %s not created: %v", dir, err)
		}
	}

	got, err := m.Get("proj-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.OwnerSession != "sess-1" {
		t.Errorf("OwnerSession = %q, want sess-1", got.OwnerSession)
	}
	if got.Quota.DiskLimitBytes != 1024 {
		t.Errorf("Quota.DiskLimitBytes = %d, want 1024", got.Quota.DiskLimitBytes)
	}
	if got.Labels["team"] != "alpha" {
		t.Errorf("Labels[team] = %q, want alpha", got.Labels["team"])
	}
}

func TestManager_Create_DuplicateIDRejected(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	if _, err := m.Create(CreateOpts{ID: "dup"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := m.Create(CreateOpts{ID: "dup"}); err == nil {
		t.Errorf("second Create with same ID returned nil error, want ErrWorkspaceExists")
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	if _, err := m.Get("nope"); err == nil {
		t.Errorf("Get(missing) returned nil error, want ErrWorkspaceNotFound")
	}
}

func TestManager_List_FilteredByOwner(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	_, _ = m.Create(CreateOpts{ID: "a", OwnerSession: "s1"})
	_, _ = m.Create(CreateOpts{ID: "b", OwnerSession: "s1"})
	_, _ = m.Create(CreateOpts{ID: "c", OwnerSession: "s2"})

	all := m.List("")
	if len(all) != 3 {
		t.Errorf("List('') = %d, want 3", len(all))
	}
	s1 := m.List("s1")
	if len(s1) != 2 {
		t.Errorf("List('s1') = %d, want 2", len(s1))
	}
	s2 := m.List("s2")
	if len(s2) != 1 {
		t.Errorf("List('s2') = %d, want 1", len(s2))
	}
}

func TestManager_Release_RemovesDirectoryAndEntry(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	w, _ := m.Create(CreateOpts{ID: "gone"})

	if err := m.Release("gone"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(w.Root); !os.IsNotExist(err) {
		t.Errorf("workspace dir still exists after Release: %v", err)
	}
	if _, err := m.Get("gone"); err == nil {
		t.Errorf("Get after Release returned nil error, want ErrWorkspaceNotFound")
	}
}

func TestManager_Release_NotFound(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	if err := m.Release("missing"); err == nil {
		t.Errorf("Release(missing) returned nil error, want ErrWorkspaceNotFound")
	}
}

func TestManager_Create_RandomIDWhenOmitted(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	w, err := m.Create(CreateOpts{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ID == "" {
		t.Errorf("ID is empty, want a random ID")
	}
	if len(w.ID) != 12 {
		t.Errorf("random ID length = %d, want 12 hex chars", len(w.ID))
	}
}

// TestWorkspace_ResolvePath_Confinement is the core isolation guarantee:
// no path supplied by an AI session bound to workspace A can resolve to a
// location outside A's root.
func TestWorkspace_ResolvePath_Confinement(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	wA, _ := m.Create(CreateOpts{ID: "a"})
	wB, _ := m.Create(CreateOpts{ID: "b"})

	// Write a secret into B.
	secret := filepath.Join(wB.DataDir, "secret.txt")
	_ = os.WriteFile(secret, []byte("B-only"), 0644)

	// A can read its own files.
	ownFile, err := wA.ResolvePath("data/myfile.txt")
	if err != nil {
		t.Fatalf("ResolvePath own file: %v", err)
	}
	if !strings.HasPrefix(ownFile, wA.Root) {
		t.Errorf("own file resolved outside workspace: %s", ownFile)
	}

	// Traversal attempts that escape the root must be rejected.
	escapeAttempts := []string{
		"../../../" + filepath.Base(wB.Root) + "/data/secret.txt",
		"..\\..\\..\\" + filepath.Base(wB.Root),
		"../../" + filepath.Base(wB.Root),
	}
	for _, attempt := range escapeAttempts {
		if _, err := wA.ResolvePath(attempt); err == nil {
			t.Errorf("escape attempt %q was NOT rejected", attempt)
		}
	}

	// Absolute paths are NOT rejected — they are re-rooted under A so the
	// AI session cannot reach B's files. This is the key isolation guarantee:
	// an absolute path supplied by A resolves inside A, never inside B.
	absoluteAttempts := []string{
		"/" + filepath.Base(wB.Root) + "/data/secret.txt",
		"////etc/passwd",
	}
	for _, attempt := range absoluteAttempts {
		resolved, err := wA.ResolvePath(attempt)
		if err != nil {
			t.Errorf("absolute attempt %q was rejected: %v", attempt, err)
			continue
		}
		if strings.HasPrefix(resolved, wB.Root) {
			t.Errorf("absolute path %q leaked into B: %s", attempt, resolved)
		}
		if !strings.HasPrefix(resolved, wA.Root) {
			t.Errorf("absolute path %q was not re-rooted under A: %s", attempt, resolved)
		}
		if _, err := os.Stat(resolved); !os.IsNotExist(err) {
			t.Errorf("re-rooted path unexpectedly exists (cross-workspace leak): %s", resolved)
		}
	}

	// Absolute path to B's secret must be confined to A (i.e. it gets
	// re-rooted under A, not pointing at B's file).
	resolved, err := wA.ResolvePath(secret)
	if err != nil {
		t.Fatalf("ResolvePath(absolute) returned error: %v", err)
	}
	if strings.HasPrefix(resolved, wB.Root) {
		t.Errorf("absolute path to B leaked into B: %s", resolved)
	}
	if !strings.HasPrefix(resolved, wA.Root) {
		t.Errorf("absolute path was not re-rooted under A: %s", resolved)
	}
	// The re-rooted path must not actually contain B's secret content.
	if _, err := os.Stat(resolved); !os.IsNotExist(err) {
		t.Errorf("re-rooted path unexpectedly exists (cross-workspace leak): %s", resolved)
	}
}

func TestWorkspace_ResolveData_Tmp_Cache(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	w, _ := m.Create(CreateOpts{ID: "w"})

	d, err := w.ResolveData("file.txt")
	if err != nil {
		t.Fatalf("ResolveData: %v", err)
	}
	if d != filepath.Join(w.DataDir, "file.txt") {
		t.Errorf("ResolveData = %s, want %s", d, filepath.Join(w.DataDir, "file.txt"))
	}
	tmp, err := w.ResolveTmp("scratch.bin")
	if err != nil {
		t.Fatalf("ResolveTmp: %v", err)
	}
	if tmp != filepath.Join(w.TmpDir, "scratch.bin") {
		t.Errorf("ResolveTmp = %s, want %s", tmp, filepath.Join(w.TmpDir, "scratch.bin"))
	}
	c, err := w.ResolveCache("layer.tar")
	if err != nil {
		t.Fatalf("ResolveCache: %v", err)
	}
	if c != filepath.Join(w.CacheDir, "layer.tar") {
		t.Errorf("ResolveCache = %s, want %s", c, filepath.Join(w.CacheDir, "layer.tar"))
	}

	// Traversal in subdir resolver must also be rejected.
	if _, err := w.ResolveData("../../escape"); err == nil {
		t.Errorf("ResolveData traversal was NOT rejected")
	}
}

func TestWorkspace_ResolvePath_EmptyReturnsRoot(t *testing.T) {
	m, _ := NewManager(ManagerConfig{BaseRoot: t.TempDir()})
	w, _ := m.Create(CreateOpts{ID: "w"})
	p, err := w.ResolvePath("")
	if err != nil {
		t.Fatalf("ResolvePath(''): %v", err)
	}
	if p != w.Root {
		t.Errorf("ResolvePath('') = %s, want %s", p, w.Root)
	}
}

func TestNewManager_CreatesBaseRoot(t *testing.T) {
	base := filepath.Join(t.TempDir(), "nested", "base")
	m, err := NewManager(ManagerConfig{BaseRoot: base})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if info, err := os.Stat(m.BaseRoot()); err != nil || !info.IsDir() {
		t.Errorf("base root %s not created: %v", m.BaseRoot(), err)
	}
}

func TestNewManager_DefaultBaseRoot(t *testing.T) {
	m, err := NewManager(ManagerConfig{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.BaseRoot() == "" {
		t.Errorf("default base root is empty")
	}
}
