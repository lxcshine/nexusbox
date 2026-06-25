package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile is a small helper for tests.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readSnapshotFile reads a file relative to a snapshot dir.
func readSnapshotFile(t *testing.T, snapshotDir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(snapshotDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestFilesystemBackend_CreateAndRestore_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "src")
	restore := filepath.Join(tmp, "restored")
	snapDir := filepath.Join(tmp, "snap")

	// Build a source tree with nested files.
	writeFile(t, filepath.Join(source, "a.txt"), "alpha")
	writeFile(t, filepath.Join(source, "sub", "b.txt"), "beta")
	writeFile(t, filepath.Join(source, "deep", "nested", "c.txt"), "gamma")

	b := filesystemBackend{}
	artifacts, err := b.Create(context.Background(), source, snapDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if artifacts.Backend != "filesystem" {
		t.Fatalf("backend = %q, want %q", artifacts.Backend, "filesystem")
	}
	if artifacts.Size <= 0 {
		t.Fatalf("size = %d, want > 0", artifacts.Size)
	}

	// Restore into a fresh path.
	if err := b.Restore(context.Background(), snapDir, restore); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readSnapshotFile(t, restore, "a.txt"); got != "alpha" {
		t.Errorf("a.txt = %q, want alpha", got)
	}
	if got := readSnapshotFile(t, restore, "sub/b.txt"); got != "beta" {
		t.Errorf("sub/b.txt = %q, want beta", got)
	}
	if got := readSnapshotFile(t, restore, "deep/nested/c.txt"); got != "gamma" {
		t.Errorf("deep/nested/c.txt = %q, want gamma", got)
	}
}

func TestSnapshotManager_CreateRestoreDelete(t *testing.T) {
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "snapshots")
	source := filepath.Join(tmp, "src")
	restore := filepath.Join(tmp, "restored")

	writeFile(t, filepath.Join(source, "main.go"), "package main\nfunc main() {}\n")
	writeFile(t, filepath.Join(source, "go.mod"), "module demo\n")

	// Force the filesystem backend so the test is deterministic cross-platform.
	sm := NewSnapshotManager(baseDir, filesystemBackend{})

	ctx := context.Background()
	snapID, err := sm.CreateSnapshotFromPath(ctx, "sb-1", nil, source)
	if err != nil {
		t.Fatalf("CreateSnapshotFromPath: %v", err)
	}
	if !strings.HasPrefix(snapID, "snap-sb-1-") {
		t.Fatalf("snapshot ID = %q, want prefix snap-sb-1-", snapID)
	}

	// Mutate the source to simulate post-snapshot changes; restore must
	// overwrite the original content.
	writeFile(t, filepath.Join(source, "main.go"), "package main // BROKEN\n")

	if err := sm.RestoreSnapshot(ctx, snapID, "sb-2", restore); err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if got := readSnapshotFile(t, restore, "main.go"); got != "package main\nfunc main() {}\n" {
		t.Errorf("restored main.go = %q, want original content", got)
	}
	if got := readSnapshotFile(t, restore, "go.mod"); got != "module demo\n" {
		t.Errorf("restored go.mod = %q, want original content", got)
	}

	// Metadata checks.
	meta, err := sm.GetSnapshot(snapID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if meta.Backend != "filesystem" {
		t.Errorf("meta.Backend = %q, want filesystem", meta.Backend)
	}
	if meta.SourcePath != source {
		t.Errorf("meta.SourcePath = %q, want %q", meta.SourcePath, source)
	}
	if meta.Checksum == "" {
		t.Errorf("meta.Checksum is empty, want a sha256 hex string")
	}

	// ListSnapshots returns our snapshot.
	list := sm.ListSnapshots("sb-1")
	if len(list) != 1 {
		t.Fatalf("ListSnapshots(sb-1) = %d items, want 1", len(list))
	}
	if list[0].ID != snapID {
		t.Errorf("ListSnapshots[0].ID = %q, want %q", list[0].ID, snapID)
	}

	// Delete removes metadata + directory.
	if err := sm.DeleteSnapshot(snapID); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, err := sm.GetSnapshot(snapID); err == nil {
		t.Errorf("GetSnapshot after delete returned nil error, want error")
	}
	if _, err := os.Stat(filepath.Join(baseDir, snapID)); !os.IsNotExist(err) {
		t.Errorf("snapshot dir still exists after delete: %v", err)
	}
}

func TestSnapshotManager_PruneOldSnapshots(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSnapshotManager(filepath.Join(tmp, "snaps"), filesystemBackend{})
	source := filepath.Join(tmp, "src")
	writeFile(t, filepath.Join(source, "f.txt"), "x")

	ctx := context.Background()
	id, err := sm.CreateSnapshotFromPath(ctx, "sb-old", nil, source)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Manually backdate the snapshot's CreatedAt to exceed the prune age.
	sm.mu.Lock()
	if m, ok := sm.snapshots[id]; ok {
		m.CreatedAt = time.Now().Add(-2 * time.Hour)
	}
	sm.mu.Unlock()

	pruned, err := sm.PruneSnapshots(time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	if _, err := sm.GetSnapshot(id); err == nil {
		t.Errorf("GetSnapshot after prune returned nil error, want error")
	}
}

func TestSnapshotManager_EmptySourcePathRejected(t *testing.T) {
	sm := NewSnapshotManager(t.TempDir(), filesystemBackend{})
	if _, err := sm.CreateSnapshotFromPath(context.Background(), "sb", nil, ""); err == nil {
		t.Errorf("Create with empty source returned nil error, want error")
	}
}

func TestSnapshotManager_ListSnapshotsSortedNewestFirst(t *testing.T) {
	tmp := t.TempDir()
	sm := NewSnapshotManager(filepath.Join(tmp, "snaps"), filesystemBackend{})
	source := filepath.Join(tmp, "src")
	writeFile(t, filepath.Join(source, "f.txt"), "x")
	ctx := context.Background()

	id1, _ := sm.CreateSnapshotFromPath(ctx, "sb", nil, source)
	// Ensure the second snapshot has a strictly later timestamp.
	time.Sleep(5 * time.Millisecond)
	id2, _ := sm.CreateSnapshotFromPath(ctx, "sb", nil, source)

	list := sm.ListSnapshots("sb")
	if len(list) != 2 {
		t.Fatalf("ListSnapshots = %d, want 2", len(list))
	}
	if list[0].ID != id2 {
		t.Errorf("ListSnapshots[0].ID = %q, want %q (newest first)", list[0].ID, id2)
	}
	if list[1].ID != id1 {
		t.Errorf("ListSnapshots[1].ID = %q, want %q", list[1].ID, id1)
	}
}

func TestNewSnapshotManager_DefaultBaseDir(t *testing.T) {
	// Passing empty baseDir should yield a non-empty platform default.
	sm := NewSnapshotManager("", filesystemBackend{})
	if sm.baseDir == "" {
		t.Errorf("default baseDir is empty")
	}
	if sm.Backend() != "filesystem" {
		t.Errorf("Backend() = %q, want filesystem", sm.Backend())
	}
}
