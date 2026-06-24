package snapshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// SnapshotManager manages sandbox snapshots and restore operations.
type SnapshotManager struct {
	mu       sync.RWMutex
	baseDir  string
	snapshots map[string]*SnapshotMeta // snapshotID -> metadata
}

// SnapshotMeta contains metadata about a snapshot.
type SnapshotMeta struct {
	ID         string
	SandboxID  string
	SandboxName string
	CreatedAt  time.Time
	Size       int64
	Checksum   string
	Labels     map[string]string
}

// NewSnapshotManager creates a new snapshot manager.
func NewSnapshotManager(baseDir string) *SnapshotManager {
	if baseDir == "" {
		baseDir = "/var/lib/nexusbox/snapshots"
	}
	return &SnapshotManager{
		baseDir:   baseDir,
		snapshots: make(map[string]*SnapshotMeta),
	}
}

// CreateSnapshot creates a checkpoint of a running sandbox.
func (sm *SnapshotManager) CreateSnapshot(ctx context.Context, sandboxID string, sb *sandboxv1alpha1.Sandbox) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	snapshotID := fmt.Sprintf("snap-%s-%d", sandboxID, time.Now().UnixNano())
	snapshotDir := filepath.Join(sm.baseDir, snapshotID)

	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Use CRIU (Checkpoint/Restore In Userspace) for container checkpointing
	// For Kata Containers, use VM snapshot capabilities
	// For gVisor, use state save capabilities

	klog.Infof("Creating snapshot %s for sandbox %s", snapshotID, sandboxID)

	// Checkpoint the container using containerd's checkpoint API
	// This requires the container to be running and CRIU to be available
	checkpointCmd := exec.CommandContext(ctx,
		"ctr", "-n", "nexusbox", "containers", "checkpoint",
		"--image-path", snapshotDir,
		sandboxID,
	)
	if output, err := checkpointCmd.CombinedOutput(); err != nil {
		os.RemoveAll(snapshotDir)
		return "", fmt.Errorf("checkpoint failed: %w, output: %s", err, string(output))
	}

	// Calculate snapshot size
	var size int64
	filepath.Walk(snapshotDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil {
			size += info.Size()
		}
		return nil
	})

	meta := &SnapshotMeta{
		ID:          snapshotID,
		SandboxID:   sandboxID,
		SandboxName: sb.Name,
		CreatedAt:   time.Now(),
		Size:        size,
		Labels:      sb.Labels,
	}
	sm.snapshots[snapshotID] = meta

	klog.Infof("Created snapshot %s for sandbox %s (size: %d bytes)", snapshotID, sandboxID, size)
	return snapshotID, nil
}

// RestoreSnapshot restores a sandbox from a snapshot.
func (sm *SnapshotManager) RestoreSnapshot(ctx context.Context, snapshotID string, targetSandboxID string) error {
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

	klog.Infof("Restoring sandbox %s from snapshot %s", targetSandboxID, snapshotID)

	// Restore the container using containerd's restore API
	restoreCmd := exec.CommandContext(ctx,
		"ctr", "-n", "nexusbox", "containers", "restore",
		"--image-path", snapshotDir,
		targetSandboxID,
	)
	if output, err := restoreCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restore failed: %w, output: %s", err, string(output))
	}

	klog.Infof("Restored sandbox %s from snapshot %s (original: %s)",
		targetSandboxID, snapshotID, meta.SandboxID)
	return nil
}

// DeleteSnapshot deletes a snapshot.
func (sm *SnapshotManager) DeleteSnapshot(snapshotID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.snapshots[snapshotID]; !ok {
		return fmt.Errorf("snapshot %s not found", snapshotID)
	}

	snapshotDir := filepath.Join(sm.baseDir, snapshotID)
	if err := os.RemoveAll(snapshotDir); err != nil {
		return fmt.Errorf("failed to remove snapshot directory: %w", err)
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
			delete(sm.snapshots, id)
			pruned++
		}
	}
	return pruned, nil
}
