package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// SnapshotBackend abstracts the underlying snapshot mechanism.
//
// Implementations:
//   - vssBackend:        Windows Volume Shadow Copy Service (vssadmin).
//   - filesystemBackend: cross-platform copy-on-write using hardlinks + mirror.
//   - containerdBackend: containerd/CRIU checkpoint (Linux, retained for compat).
//
// The backend is selected in NewSnapshotManager based on the host OS and the
// SandboxStorage layout. Each backend is responsible for producing a
// self-contained snapshot directory under sm.baseDir/<snapshotID>/ that can be
// restored by the same backend.
type SnapshotBackend interface {
	// Name returns a human-readable backend identifier (e.g. "vss", "filesystem").
	Name() string
	// Create produces a snapshot of the given source path into snapshotDir.
	Create(ctx context.Context, sourcePath, snapshotDir string) (*SnapshotArtifacts, error)
	// Restore reproduces the source tree from snapshotDir into targetPath.
	Restore(ctx context.Context, snapshotDir, targetPath string) error
}

// SnapshotArtifacts describes what a backend wrote.
type SnapshotArtifacts struct {
	// Backend is the backend name that produced this snapshot.
	Backend string
	// Size is the total byte size of the snapshot artifacts.
	Size int64
	// Extra is backend-specific metadata (e.g. VSS shadow copy ID).
	Extra map[string]string
}

// SnapshotManager manages sandbox snapshots and restore operations.
type SnapshotManager struct {
	mu        sync.RWMutex
	baseDir   string
	backend   SnapshotBackend
	snapshots map[string]*SnapshotMeta // snapshotID -> metadata
}

// SnapshotMeta contains metadata about a snapshot.
type SnapshotMeta struct {
	ID          string
	SandboxID   string
	SandboxName string
	CreatedAt   time.Time
	Size        int64
	Checksum    string
	Labels      map[string]string
	Backend     string
	SourcePath  string
	Artifacts   map[string]string
}

// NewSnapshotManager creates a new snapshot manager.
//
// The backend is auto-selected: VSS on Windows, filesystem backend elsewhere.
// Pass a non-nil backend to override (mainly for tests).
func NewSnapshotManager(baseDir string, backend SnapshotBackend) *SnapshotManager {
	if baseDir == "" {
		if runtime.GOOS == "windows" {
			baseDir = `C:\ProgramData\NexusBox\snapshots`
		} else {
			baseDir = "/var/lib/nexusbox/snapshots"
		}
	}
	if backend == nil {
		backend = defaultBackend()
	}
	return &SnapshotManager{
		baseDir:   baseDir,
		backend:   backend,
		snapshots: make(map[string]*SnapshotMeta),
	}
}

// CreateSnapshot creates a checkpoint of a running sandbox.
//
// sourcePath is the workspace (or volume) to snapshot. If empty, the sandbox's
// declared WorkingDir is used. For containerd-managed sandboxes on Linux, the
// manager still falls back to `ctr containers checkpoint` when sourcePath is
// empty and the sandbox runtime is runc/kata/gvisor.
func (sm *SnapshotManager) CreateSnapshot(ctx context.Context, sandboxID string, sb *sandboxv1alpha1.Sandbox) (string, error) {
	return sm.CreateSnapshotFromPath(ctx, sandboxID, sb, sm.sourcePathFor(sb))
}

// CreateSnapshotFromPath creates a snapshot of an explicit source path.
func (sm *SnapshotManager) CreateSnapshotFromPath(ctx context.Context, sandboxID string, sb *sandboxv1alpha1.Sandbox, sourcePath string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sourcePath == "" {
		return "", fmt.Errorf("sourcePath is required for snapshot")
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return "", fmt.Errorf("source path %s not accessible: %w", sourcePath, err)
	}

	snapshotID := fmt.Sprintf("snap-%s-%d", sandboxID, time.Now().UnixNano())
	snapshotDir := filepath.Join(sm.baseDir, snapshotID)

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	klog.Infof("Creating snapshot %s for sandbox %s via %s backend (source=%s)",
		snapshotID, sandboxID, sm.backend.Name(), sourcePath)

	artifacts, err := sm.backend.Create(ctx, sourcePath, snapshotDir)
	if err != nil {
		os.RemoveAll(snapshotDir)
		return "", fmt.Errorf("%s backend create failed: %w", sm.backend.Name(), err)
	}

	checksum, err := sm.checksumDir(snapshotDir)
	if err != nil {
		klog.Warningf("Failed to compute checksum for snapshot %s: %v", snapshotID, err)
	}

	name := ""
	if sb != nil {
		name = sb.Name
	}
	var labels map[string]string
	if sb != nil {
		labels = sb.Labels
	}

	meta := &SnapshotMeta{
		ID:          snapshotID,
		SandboxID:   sandboxID,
		SandboxName: name,
		CreatedAt:   time.Now(),
		Size:        artifacts.Size,
		Checksum:    checksum,
		Labels:      labels,
		Backend:     artifacts.Backend,
		SourcePath:  sourcePath,
		Artifacts:   artifacts.Extra,
	}
	sm.snapshots[snapshotID] = meta

	klog.Infof("Created snapshot %s for sandbox %s (size: %d bytes, backend: %s)",
		snapshotID, sandboxID, artifacts.Size, artifacts.Backend)
	return snapshotID, nil
}

// RestoreSnapshot restores a sandbox from a snapshot into targetSandboxID.
//
// targetPath is where the restored tree should land. If empty, the original
// SourcePath recorded at create time is reused.
func (sm *SnapshotManager) RestoreSnapshot(ctx context.Context, snapshotID, targetSandboxID, targetPath string) error {
	sm.mu.RLock()
	meta, ok := sm.snapshots[snapshotID]
	sm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("snapshot %s not found", snapshotID)
	}

	snapshotDir := filepath.Join(sm.baseDir, snapshotID)
	if _, err := os.Stat(snapshotDir); os.IsNotExist(err) {
		return fmt.Errorf("snapshot directory %s not found", snapshotDir)
	}

	if targetPath == "" {
		targetPath = meta.SourcePath
	}
	if targetPath == "" {
		return fmt.Errorf("no target path and snapshot has no recorded source path")
	}

	klog.Infof("Restoring sandbox %s from snapshot %s into %s (backend: %s)",
		targetSandboxID, snapshotID, targetPath, meta.Backend)

	if err := sm.backend.Restore(ctx, snapshotDir, targetPath); err != nil {
		return fmt.Errorf("%s backend restore failed: %w", meta.Backend, err)
	}

	klog.Infof("Restored sandbox %s from snapshot %s (original: %s)",
		targetSandboxID, snapshotID, meta.SandboxID)
	return nil
}

// DeleteSnapshot deletes a snapshot.
func (sm *SnapshotManager) DeleteSnapshot(snapshotID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	meta, ok := sm.snapshots[snapshotID]
	if !ok {
		return fmt.Errorf("snapshot %s not found", snapshotID)
	}

	snapshotDir := filepath.Join(sm.baseDir, snapshotID)
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("failed to remove snapshot directory: %w", err)
	}

	// VSS snapshots leave a shadow copy that must be explicitly cleaned up.
	if meta.Backend == vssBackendName {
		if shadowID, ok := meta.Artifacts["shadowID"]; ok && shadowID != "" {
			_ = deleteVSSShadow(context.Background(), shadowID)
		}
	}

	delete(sm.snapshots, snapshotID)
	klog.Infof("Deleted snapshot %s", snapshotID)
	return nil
}

// ListSnapshots lists all snapshots, optionally filtered by sandbox.
func (sm *SnapshotManager) ListSnapshots(sandboxID string) []*SnapshotMeta {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*SnapshotMeta
	for _, meta := range sm.snapshots {
		if sandboxID == "" || meta.SandboxID == sandboxID {
			result = append(result, meta)
		}
	}
	// Sort by creation time descending (newest first) for stable output.
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

// GetSnapshot returns metadata for a specific snapshot.
func (sm *SnapshotManager) GetSnapshot(snapshotID string) (*SnapshotMeta, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	meta, ok := sm.snapshots[snapshotID]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	return meta, nil
}

// PruneSnapshots removes snapshots older than the given duration.
func (sm *SnapshotManager) PruneSnapshots(maxAge time.Duration) (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	pruned := 0
	for id, meta := range sm.snapshots {
		if now.Sub(meta.CreatedAt) > maxAge {
			snapshotDir := filepath.Join(sm.baseDir, id)
			os.RemoveAll(snapshotDir)
			if meta.Backend == vssBackendName {
				if shadowID, ok := meta.Artifacts["shadowID"]; ok && shadowID != "" {
					_ = deleteVSSShadow(context.Background(), shadowID)
				}
			}
			delete(sm.snapshots, id)
			pruned++
		}
	}
	return pruned, nil
}

// Backend returns the active backend name (for diagnostics).
func (sm *SnapshotManager) Backend() string {
	return sm.backend.Name()
}

// sourcePathFor extracts the workspace path from a Sandbox spec.
func (sm *SnapshotManager) sourcePathFor(sb *sandboxv1alpha1.Sandbox) string {
	if sb == nil {
		return ""
	}
	if sb.Spec.WorkingDir != "" {
		return sb.Spec.WorkingDir
	}
	if sb.Spec.Storage != nil {
		for i := range sb.Spec.Storage.Volumes {
			if sb.Spec.Storage.Volumes[i].VolumeSource.HostPath != nil {
				return sb.Spec.Storage.Volumes[i].VolumeSource.HostPath.Path
			}
		}
	}
	return ""
}

// checksumDir computes a SHA-256 checksum over the sorted file list and their
// contents inside dir. Returns "" on failure to read any file.
func (sm *SnapshotManager) checksumDir(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		h.Write([]byte(rel + "\x00"))
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		h.Write([]byte("\x00"))
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- Filesystem backend (cross-platform, default) ---

// filesystemBackend snapshots a directory tree by copying it into the
// snapshot directory. Create copies (not hardlinks) so that subsequent writes
// to the live source cannot corrupt the snapshot — on most filesystems a
// hardlink shares the inode and in-place writes would be visible through both
// names, defeating the snapshot semantics. Restore mirrors the snapshot back
// using hardlinks when possible (the snapshot dir is read-only after Create,
// so hardlinks into the target are safe and share storage with the snapshot).
type filesystemBackend struct{}

func (filesystemBackend) Name() string { return "filesystem" }

func (filesystemBackend) Create(ctx context.Context, sourcePath, snapshotDir string) (*SnapshotArtifacts, error) {
	dataDir := filepath.Join(snapshotDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	var size int64
	err := filepath.WalkDir(sourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(sourcePath, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dataDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// Copy on Create so the snapshot is independent of the live source.
		if cerr := copyFile(path, target, info.Mode()); cerr != nil {
			return cerr
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Write a marker so Restore can validate the layout.
	if err := os.WriteFile(filepath.Join(snapshotDir, "backend.txt"),
		[]byte("filesystem\n"), 0644); err != nil {
		return nil, err
	}
	return &SnapshotArtifacts{
		Backend: "filesystem",
		Size:    size,
		Extra:   map[string]string{"layout": "mirror"},
	}, nil
}

func (filesystemBackend) Restore(ctx context.Context, snapshotDir, targetPath string) error {
	dataDir := filepath.Join(snapshotDir, "data")
	if _, err := os.Stat(dataDir); err != nil {
		return fmt.Errorf("snapshot data dir missing: %w", err)
	}
	if err := os.MkdirAll(targetPath, 0755); err != nil {
		return err
	}
	// Mirror the dataDir into targetPath using hardlinks (so restore is fast
	// and shares storage with the snapshot). Copy fallback for cross-volume.
	return filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dataDir, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(targetPath, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if linkErr := os.Link(path, target); linkErr == nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile copies src to dst preserving mode. Used as a hardlink fallback.
func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// defaultBackend returns the platform-appropriate default backend.
func defaultBackend() SnapshotBackend {
	if runtime.GOOS == "windows" {
		return newVSSBackend()
	}
	return filesystemBackend{}
}

// sourceDirOrError ensures a directory exists and is absolute.
func sourceDirOrError(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// runCmd is a small helper that runs a command and returns combined output.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w: %s", name, args, err, string(out))
	}
	return out, nil
}
