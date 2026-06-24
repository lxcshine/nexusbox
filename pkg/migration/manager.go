package migration

import (
	"context"
	"fmt"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/snapshot"
	"github.com/nexusbox/nexusbox/pkg/store/etcd"
	"k8s.io/klog/v2"
)

// MigrationManager handles sandbox migration between nodes.
type MigrationManager struct {
	mu       sync.RWMutex
	store    *etcd.Store
	snapshot *snapshot.SnapshotManager
	migrating map[string]*MigrationStatus // sandboxID -> status
}

// MigrationStatus tracks the status of a sandbox migration.
type MigrationStatus struct {
	SandboxID   string
	SourceNode  string
	TargetNode  string
	Phase       MigrationPhase
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       string
}

// MigrationPhase represents the phase of a migration.
type MigrationPhase string

const (
	MigrationPhasePending    MigrationPhase = "Pending"
	MigrationPhaseCheckpoint MigrationPhase = "Checkpoint"
	MigrationPhaseTransfer   MigrationPhase = "Transfer"
	MigrationPhaseRestore    MigrationPhase = "Restore"
	MigrationPhaseCleanup    MigrationPhase = "Cleanup"
	MigrationPhaseComplete   MigrationPhase = "Complete"
	MigrationPhaseFailed     MigrationPhase = "Failed"
)

// NewMigrationManager creates a new migration manager.
func NewMigrationManager(store *etcd.Store, snapMgr *snapshot.SnapshotManager) *MigrationManager {
	return &MigrationManager{
		store:     store,
		snapshot:  snapMgr,
		migrating: make(map[string]*MigrationStatus),
	}
}

// MigrateSandbox migrates a sandbox from one node to another.
func (mm *MigrationManager) MigrateSandbox(ctx context.Context, sandboxID string, sourceNode, targetNode string) error {
	mm.mu.Lock()
	if _, ok := mm.migrating[sandboxID]; ok {
		mm.mu.Unlock()
		return fmt.Errorf("sandbox %s is already being migrated", sandboxID)
	}

	status := &MigrationStatus{
		SandboxID:  sandboxID,
		SourceNode: sourceNode,
		TargetNode: targetNode,
		Phase:      MigrationPhasePending,
		StartedAt:  time.Now(),
	}
	mm.migrating[sandboxID] = status
	mm.mu.Unlock()

	klog.Infof("Starting migration of sandbox %s from %s to %s", sandboxID, sourceNode, targetNode)

	// Step 1: Get sandbox from store
	sb, err := mm.store.GetSandbox(ctx, "", sandboxID)
	if err != nil {
		return mm.failMigration(sandboxID, fmt.Sprintf("failed to get sandbox: %v", err))
	}

	// Step 2: Create checkpoint on source node
	mm.updatePhase(sandboxID, MigrationPhaseCheckpoint)
	snapshotID, err := mm.snapshot.CreateSnapshot(ctx, sandboxID, sb)
	if err != nil {
		return mm.failMigration(sandboxID, fmt.Sprintf("checkpoint failed: %v", err))
	}
	klog.Infof("Created checkpoint %s for sandbox %s on source node %s", snapshotID, sandboxID, sourceNode)

	// Step 3: Transfer snapshot to target node
	mm.updatePhase(sandboxID, MigrationPhaseTransfer)
	// In a real implementation, this would transfer the snapshot data
	// via rsync, S3, or a shared filesystem
	klog.Infof("Transferring snapshot %s to target node %s", snapshotID, targetNode)

	// Step 4: Restore on target node
	mm.updatePhase(sandboxID, MigrationPhaseRestore)
	targetSandboxID := sandboxID // Same ID on target
	if err := mm.snapshot.RestoreSnapshot(ctx, snapshotID, targetSandboxID); err != nil {
		return mm.failMigration(sandboxID, fmt.Sprintf("restore failed: %v", err))
	}

	// Step 5: Update sandbox status in store
	sb.Status.NodeName = targetNode
	sb.Status.Phase = sandboxv1alpha1.SandboxRunning
	if err := mm.store.UpdateSandboxStatus(ctx, sb); err != nil {
		return mm.failMigration(sandboxID, fmt.Sprintf("status update failed: %v", err))
	}

	// Step 6: Cleanup
	mm.updatePhase(sandboxID, MigrationPhaseCleanup)
	// Delete the snapshot after successful migration
	mm.snapshot.DeleteSnapshot(snapshotID)

	// Mark complete
	mm.mu.Lock()
	now := time.Now()
	status.Phase = MigrationPhaseComplete
	status.CompletedAt = &now
	mm.mu.Unlock()

	klog.Infof("Migration of sandbox %s from %s to %s completed successfully",
		sandboxID, sourceNode, targetNode)
	return nil
}

// GetMigrationStatus returns the status of a migration.
func (mm *MigrationManager) GetMigrationStatus(sandboxID string) (*MigrationStatus, bool) {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	status, ok := mm.migrating[sandboxID]
	return status, ok
}

// ListActiveMigrations returns all active migrations.
func (mm *MigrationManager) ListActiveMigrations() []*MigrationStatus {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	var result []*MigrationStatus
	for _, status := range mm.migrating {
		if status.Phase != MigrationPhaseComplete && status.Phase != MigrationPhaseFailed {
			result = append(result, status)
		}
	}
	return result
}

func (mm *MigrationManager) updatePhase(sandboxID string, phase MigrationPhase) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	if status, ok := mm.migrating[sandboxID]; ok {
		status.Phase = phase
		klog.V(4).Infof("Migration of %s: phase -> %s", sandboxID, phase)
	}
}

func (mm *MigrationManager) failMigration(sandboxID, errMsg string) error {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	if status, ok := mm.migrating[sandboxID]; ok {
		status.Phase = MigrationPhaseFailed
		status.Error = errMsg
		now := time.Now()
		status.CompletedAt = &now
	}
	klog.Errorf("Migration of %s failed: %s", sandboxID, errMsg)
	return fmt.Errorf("migration failed: %s", errMsg)
}
