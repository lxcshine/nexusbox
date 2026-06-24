package recovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/store/etcd"
	"k8s.io/klog/v2"
)

// StateRecoveryManager handles state recovery after failures.
type StateRecoveryManager struct {
	mu    sync.RWMutex
	store *etcd.Store

	// Callbacks for reconciling state
	onSandboxRecovery func(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error
	onNodeRecovery    func(ctx context.Context, node *sandboxv1alpha1.SandboxNode) error
	onTenantRecovery  func(ctx context.Context, tn *sandboxv1alpha1.Tenant) error
}

// NewStateRecoveryManager creates a new state recovery manager.
func NewStateRecoveryManager(store *etcd.Store) *StateRecoveryManager {
	return &StateRecoveryManager{
		store: store,
	}
}

// OnSandboxRecovery sets the callback for sandbox recovery.
func (m *StateRecoveryManager) OnSandboxRecovery(fn func(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error) {
	m.onSandboxRecovery = fn
}

// OnNodeRecovery sets the callback for node recovery.
func (m *StateRecoveryManager) OnNodeRecovery(fn func(ctx context.Context, node *sandboxv1alpha1.SandboxNode) error) {
	m.onNodeRecovery = fn
}

// OnTenantRecovery sets the callback for tenant recovery.
func (m *StateRecoveryManager) OnTenantRecovery(fn func(ctx context.Context, tn *sandboxv1alpha1.Tenant) error) {
	m.onTenantRecovery = fn
}

// RecoverAll recovers all state from etcd after a restart.
func (m *StateRecoveryManager) RecoverAll(ctx context.Context) error {
	klog.Info("Starting full state recovery...")

	// 1. Recover tenants
	if err := m.recoverTenants(ctx); err != nil {
		return fmt.Errorf("failed to recover tenants: %w", err)
	}

	// 2. Recover nodes
	if err := m.recoverNodes(ctx); err != nil {
		return fmt.Errorf("failed to recover nodes: %w", err)
	}

	// 3. Recover sandboxes
	if err := m.recoverSandboxes(ctx); err != nil {
		return fmt.Errorf("failed to recover sandboxes: %w", err)
	}

	klog.Info("State recovery complete")
	return nil
}

// RecoverSandboxesForNode recovers sandboxes assigned to a specific node
// after the node comes back online.
func (m *StateRecoveryManager) RecoverSandboxesForNode(ctx context.Context, nodeName string) error {
	sandboxes, err := m.store.ListSandboxes(ctx, "")
	if err != nil {
		return fmt.Errorf("failed to list sandboxes: %w", err)
	}

	recovered := 0
	for _, sb := range sandboxes {
		if sb.Status.NodeName != nodeName {
			continue
		}

		// Check if sandbox is in a running state but the runtime is gone
		if sb.Status.Phase == sandboxv1alpha1.SandboxRunning ||
			sb.Status.Phase == sandboxv1alpha1.SandboxPaused {
			klog.Infof("Recovering sandbox %s/%s on node %s", sb.Namespace, sb.Name, nodeName)

			if m.onSandboxRecovery != nil {
				if err := m.onSandboxRecovery(ctx, sb); err != nil {
					klog.Warningf("Failed to recover sandbox %s/%s: %v", sb.Namespace, sb.Name, err)
					continue
				}
			}
			recovered++
		}
	}

	klog.Infof("Recovered %d sandboxes for node %s", recovered, nodeName)
	return nil
}

// ReconcileSandboxState checks and corrects sandbox state discrepancies.
func (m *StateRecoveryManager) ReconcileSandboxState(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If sandbox is marked as Running but has no RuntimeID, it's stale
	if sb.Status.Phase == sandboxv1alpha1.SandboxRunning && sb.Status.RuntimeID == "" {
		klog.Warningf("Sandbox %s/%s marked Running but has no RuntimeID, resetting to Pending",
			sb.Namespace, sb.Name)
		sb.Status.Phase = sandboxv1alpha1.SandboxPending
		sb.Status.Reason = "RecoveryReset"
		sb.Status.Message = "Runtime ID missing after recovery, rescheduling"
		return m.store.UpdateSandboxStatus(ctx, sb)
	}

	// If sandbox has been in Creating state for too long, reset
	if sb.Status.Phase == sandboxv1alpha1.SandboxCreating {
		if sb.Status.LastScheduledTime != nil {
			elapsed := time.Since(sb.Status.LastScheduledTime.Time)
			if elapsed > 5*time.Minute {
				klog.Warningf("Sandbox %s/%s stuck in Creating for %v, resetting",
					sb.Namespace, sb.Name, elapsed)
				sb.Status.Phase = sandboxv1alpha1.SandboxPending
				sb.Status.Reason = "CreatingTimeout"
				return m.store.UpdateSandboxStatus(ctx, sb)
			}
		}
	}

	return nil
}

// --- Internal methods ---

func (m *StateRecoveryManager) recoverTenants(ctx context.Context) error {
	tenants, err := m.store.ListTenants(ctx)
	if err != nil {
		return err
	}

	for _, tn := range tenants {
		klog.V(4).Infof("Recovering tenant %s (phase: %s)", tn.Name, tn.Status.Phase)
		if m.onTenantRecovery != nil {
			if err := m.onTenantRecovery(ctx, tn); err != nil {
				klog.Warningf("Failed to recover tenant %s: %v", tn.Name, err)
			}
		}
	}
	klog.Infof("Recovered %d tenants", len(tenants))
	return nil
}

func (m *StateRecoveryManager) recoverNodes(ctx context.Context) error {
	nodes, err := m.store.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		klog.V(4).Infof("Recovering node %s (phase: %s)", node.Name, node.Status.Phase)
		// Mark nodes as NotReady until heartbeat is received
		if node.Status.Phase == sandboxv1alpha1.NodeRunning {
			node.Status.Phase = sandboxv1alpha1.NodeNotReady
			m.store.UpdateNode(ctx, node)
		}
		if m.onNodeRecovery != nil {
			if err := m.onNodeRecovery(ctx, node); err != nil {
				klog.Warningf("Failed to recover node %s: %v", node.Name, err)
			}
		}
	}
	klog.Infof("Recovered %d nodes", len(nodes))
	return nil
}

func (m *StateRecoveryManager) recoverSandboxes(ctx context.Context) error {
	sandboxes, err := m.store.ListSandboxes(ctx, "")
	if err != nil {
		return err
	}

	for _, sb := range sandboxes {
		if err := m.ReconcileSandboxState(ctx, sb); err != nil {
			klog.Warningf("Failed to reconcile sandbox %s/%s: %v", sb.Namespace, sb.Name, err)
		}
		if m.onSandboxRecovery != nil {
			if err := m.onSandboxRecovery(ctx, sb); err != nil {
				klog.Warningf("Failed to recover sandbox %s/%s: %v", sb.Namespace, sb.Name, err)
			}
		}
	}
	klog.Infof("Recovered %d sandboxes", len(sandboxes))
	return nil
}
