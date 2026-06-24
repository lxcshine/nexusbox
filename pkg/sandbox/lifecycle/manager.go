/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/tenant"
)

// LifecycleManager manages the lifecycle of sandboxes.
// It handles state transitions, timeout management, and coordinates
// between the scheduler, runtime, and tenant manager.
type LifecycleManager struct {
	mu sync.RWMutex

	// sandboxes tracks all managed sandboxes indexed by namespace/name.
	sandboxes map[string]*SandboxState

	// runtimeManager manages sandbox runtimes.
	runtimeManager *runtime.RuntimeManager

	// tenantManager manages tenant information.
	tenantManager *tenant.TenantManager

	// eventRecorder records lifecycle events.
	eventRecorder LifecycleEventRecorder

	// informer watches for sandbox CRD changes.
	informer cache.SharedIndexInformer

	// stopCh is used to signal shutdown.
	stopCh chan struct{}

	// maxRetryCount is the maximum number of retries for a sandbox.
	maxRetryCount int32

	// retryBackoff is the base backoff duration for retries.
	retryBackoff time.Duration

	// maxRetryBackoff is the maximum backoff duration.
	maxRetryBackoff time.Duration
}

// SandboxState holds the runtime state of a sandbox.
type SandboxState struct {
	// Key is the namespace/name identifier.
	Key string
	// Sandbox is the sandbox CRD object.
	Sandbox *sandboxv1alpha1.Sandbox
	// CurrentPhase is the current lifecycle phase.
	CurrentPhase sandboxv1alpha1.SandboxPhase
	// TargetPhase is the desired lifecycle phase.
	TargetPhase sandboxv1alpha1.SandboxPhase
	// RetryCount is the number of retries.
	RetryCount int32
	// LastTransitionTime is the time of the last phase transition.
	LastTransitionTime time.Time
	// RuntimeHandle is the runtime-specific handle.
	RuntimeHandle runtime.RuntimeHandle
	// NodeName is the node where the sandbox is running.
	NodeName string
	// ScheduledNode is the node the sandbox was scheduled to.
	ScheduledNode string
	// CreationTime is the time the sandbox was created.
	CreationTime time.Time
	// StartTime is the time the sandbox started running.
	StartTime time.Time
	// IdleSince is the time since the sandbox has been idle.
	IdleSince time.Time
	// ExpirationTime is the time the sandbox will expire.
	ExpirationTime time.Time
	// DeletionTimestamp is when the sandbox was marked for deletion.
	DeletionTimestamp *time.Time
	// LastScheduledTime is the last time the sandbox was scheduled.
	LastScheduledTime time.Time
	// PendingActions are actions waiting to be executed.
	PendingActions []LifecycleAction
}

// LifecycleAction represents an action to be performed on a sandbox.
type LifecycleAction string

const (
	// ActionSchedule indicates the sandbox needs to be scheduled.
	ActionSchedule LifecycleAction = "Schedule"
	// ActionCreate indicates the sandbox runtime needs to be created.
	ActionCreate LifecycleAction = "Create"
	// ActionStart indicates the sandbox needs to be started.
	ActionStart LifecycleAction = "Start"
	// ActionStop indicates the sandbox needs to be stopped.
	ActionStop LifecycleAction = "Stop"
	// ActionPause indicates the sandbox needs to be paused.
	ActionPause LifecycleAction = "Pause"
	// ActionResume indicates the sandbox needs to be resumed.
	ActionResume LifecycleAction = "Resume"
	// ActionDelete indicates the sandbox needs to be deleted.
	ActionDelete LifecycleAction = "Delete"
	// ActionEvict indicates the sandbox needs to be evicted.
	ActionEvict LifecycleAction = "Evict"
	// ActionRetry indicates the sandbox needs to be retried.
	ActionRetry LifecycleAction = "Retry"
	// ActionTimeout indicates the sandbox has timed out.
	ActionTimeout LifecycleAction = "Timeout"
)

// LifecycleEventRecorder records lifecycle events.
type LifecycleEventRecorder interface {
	RecordSandboxEvent(sandboxName, namespace, eventType, reason, message string)
}

// PhaseTransition defines a valid phase transition.
type PhaseTransition struct {
	From sandboxv1alpha1.SandboxPhase
	To   sandboxv1alpha1.SandboxPhase
}

// validTransitions defines the valid phase transitions for a sandbox.
var validTransitions = map[PhaseTransition]bool{
	{sandboxv1alpha1.SandboxPending, sandboxv1alpha1.SandboxScheduling}:  true,
	{sandboxv1alpha1.SandboxPending, sandboxv1alpha1.SandboxFailed}:      true,
	{sandboxv1alpha1.SandboxPending, sandboxv1alpha1.SandboxTerminating}: true,
	{sandboxv1alpha1.SandboxScheduling, sandboxv1alpha1.SandboxCreating}: true,
	{sandboxv1alpha1.SandboxScheduling, sandboxv1alpha1.SandboxPending}:  true,
	{sandboxv1alpha1.SandboxScheduling, sandboxv1alpha1.SandboxFailed}:   true,
	{sandboxv1alpha1.SandboxCreating, sandboxv1alpha1.SandboxRunning}:    true,
	{sandboxv1alpha1.SandboxCreating, sandboxv1alpha1.SandboxFailed}:     true,
	{sandboxv1alpha1.SandboxCreating, sandboxv1alpha1.SandboxPending}:    true,
	{sandboxv1alpha1.SandboxRunning, sandboxv1alpha1.SandboxPausing}:     true,
	{sandboxv1alpha1.SandboxRunning, sandboxv1alpha1.SandboxStopping}:    true,
	{sandboxv1alpha1.SandboxRunning, sandboxv1alpha1.SandboxTerminating}: true,
	{sandboxv1alpha1.SandboxRunning, sandboxv1alpha1.SandboxFailed}:      true,
	{sandboxv1alpha1.SandboxRunning, sandboxv1alpha1.SandboxEvicted}:     true,
	{sandboxv1alpha1.SandboxPausing, sandboxv1alpha1.SandboxPaused}:      true,
	{sandboxv1alpha1.SandboxPausing, sandboxv1alpha1.SandboxFailed}:      true,
	{sandboxv1alpha1.SandboxPausing, sandboxv1alpha1.SandboxRunning}:     true,
	{sandboxv1alpha1.SandboxPaused, sandboxv1alpha1.SandboxResuming}:     true,
	{sandboxv1alpha1.SandboxPaused, sandboxv1alpha1.SandboxStopping}:     true,
	{sandboxv1alpha1.SandboxPaused, sandboxv1alpha1.SandboxTerminating}:  true,
	{sandboxv1alpha1.SandboxResuming, sandboxv1alpha1.SandboxRunning}:    true,
	{sandboxv1alpha1.SandboxResuming, sandboxv1alpha1.SandboxFailed}:     true,
	{sandboxv1alpha1.SandboxResuming, sandboxv1alpha1.SandboxPaused}:     true,
	{sandboxv1alpha1.SandboxStopping, sandboxv1alpha1.SandboxStopped}:    true,
	{sandboxv1alpha1.SandboxStopping, sandboxv1alpha1.SandboxFailed}:     true,
	{sandboxv1alpha1.SandboxStopped, sandboxv1alpha1.SandboxTerminating}: true,
	{sandboxv1alpha1.SandboxStopped, sandboxv1alpha1.SandboxCreating}:    true,
	{sandboxv1alpha1.SandboxEvicted, sandboxv1alpha1.SandboxPending}:     true,
	{sandboxv1alpha1.SandboxEvicted, sandboxv1alpha1.SandboxTerminating}: true,
	{sandboxv1alpha1.SandboxFailed, sandboxv1alpha1.SandboxPending}:      true,
	{sandboxv1alpha1.SandboxFailed, sandboxv1alpha1.SandboxTerminating}:  true,
}

// NewLifecycleManager creates a new LifecycleManager.
func NewLifecycleManager(
	runtimeManager *runtime.RuntimeManager,
	tenantManager *tenant.TenantManager,
	informer cache.SharedIndexInformer,
	eventRecorder LifecycleEventRecorder,
) *LifecycleManager {
	lm := &LifecycleManager{
		sandboxes:       make(map[string]*SandboxState),
		runtimeManager:  runtimeManager,
		tenantManager:   tenantManager,
		informer:        informer,
		eventRecorder:   eventRecorder,
		maxRetryCount:   5,
		retryBackoff:    1 * time.Second,
		maxRetryBackoff: 5 * time.Minute,
		stopCh:          make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    lm.onSandboxAdd,
			UpdateFunc: lm.onSandboxUpdate,
			DeleteFunc: lm.onSandboxDelete,
		})
	}

	return lm
}

// Start starts the lifecycle manager's background goroutines.
func (lm *LifecycleManager) Start(ctx context.Context) {
	klog.Info("Starting sandbox lifecycle manager")

	// Start timeout checker
	go wait.Until(lm.checkTimeouts, 10*time.Second, lm.stopCh)

	// Start idle checker
	go wait.Until(lm.checkIdleSandboxes, 30*time.Second, lm.stopCh)

	// Start retry processor
	go wait.Until(lm.processRetries, 5*time.Second, lm.stopCh)

	// Start expiration checker
	go wait.Until(lm.checkExpirations, 30*time.Second, lm.stopCh)

	// Start auto-cleanup
	go wait.Until(lm.cleanupCompletedSandboxes, 5*time.Minute, lm.stopCh)

	klog.Info("Sandbox lifecycle manager started")
}

// Stop stops the lifecycle manager.
func (lm *LifecycleManager) Stop() {
	klog.Info("Stopping sandbox lifecycle manager")
	close(lm.stopCh)
}

// CreateSandbox handles the creation of a new sandbox.
func (lm *LifecycleManager) CreateSandbox(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	key := sb.Namespace + "/" + sb.Name

	lm.mu.Lock()
	defer lm.mu.Unlock()

	if _, exists := lm.sandboxes[key]; exists {
		return fmt.Errorf("sandbox %s already exists", key)
	}

	// Validate tenant
	if err := lm.tenantManager.CanCreateSandbox(ctx, sb.Spec.TenantRef.Name, &sb.Spec.Resources); err != nil {
		return fmt.Errorf("tenant validation failed: %w", err)
	}

	// Validate runtime
	if err := lm.tenantManager.ValidateRuntime(sb.Spec.TenantRef.Name, sb.Spec.Runtime); err != nil {
		return fmt.Errorf("runtime validation failed: %w", err)
	}

	// Validate scheduling policy
	if err := lm.tenantManager.ValidateSchedulingPolicy(sb.Spec.TenantRef.Name, sb.Spec.SchedulingPolicy); err != nil {
		return fmt.Errorf("scheduling policy validation failed: %w", err)
	}

	// Create sandbox state
	state := &SandboxState{
		Key:                key,
		Sandbox:            sb,
		CurrentPhase:       sandboxv1alpha1.SandboxPending,
		TargetPhase:        sandboxv1alpha1.SandboxRunning,
		LastTransitionTime: time.Now(),
		CreationTime:       time.Now(),
		PendingActions:     []LifecycleAction{ActionSchedule},
	}

	// Set expiration time if max lifetime is specified
	if sb.Spec.MaxLifetimeSeconds != nil {
		state.ExpirationTime = time.Now().Add(time.Duration(*sb.Spec.MaxLifetimeSeconds) * time.Second)
	}

	lm.sandboxes[key] = state

	klog.Infof("Created sandbox %s (phase: Pending)", key)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(sb.Name, sb.Namespace, "Normal", "SandboxCreated",
			fmt.Sprintf("Sandbox %s created", key))
	}

	return nil
}

// StartSandbox transitions a sandbox to the Running state.
func (lm *LifecycleManager) StartSandbox(ctx context.Context, key string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxStopped && state.CurrentPhase != sandboxv1alpha1.SandboxPaused {
		return fmt.Errorf("cannot start sandbox %s in phase %s", key, state.CurrentPhase)
	}

	if state.CurrentPhase == sandboxv1alpha1.SandboxPaused {
		return lm.resumeSandboxLocked(ctx, state)
	}

	// Transition from Stopped to Creating
	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxCreating); err != nil {
		return err
	}

	state.PendingActions = append(state.PendingActions, ActionCreate)
	return nil
}

// StopSandbox gracefully stops a sandbox.
func (lm *LifecycleManager) StopSandbox(ctx context.Context, key string, gracePeriodSeconds int64) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxRunning && state.CurrentPhase != sandboxv1alpha1.SandboxPaused {
		return fmt.Errorf("cannot stop sandbox %s in phase %s", key, state.CurrentPhase)
	}

	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxStopping); err != nil {
		return err
	}

	state.TargetPhase = sandboxv1alpha1.SandboxStopped
	state.PendingActions = append(state.PendingActions, ActionStop)

	klog.Infof("Stopping sandbox %s (grace period: %ds)", key, gracePeriodSeconds)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Normal", "SandboxStopping", fmt.Sprintf("Sandbox %s is stopping", key))
	}

	return nil
}

// PauseSandbox pauses a running sandbox.
func (lm *LifecycleManager) PauseSandbox(ctx context.Context, key string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxRunning {
		return fmt.Errorf("cannot pause sandbox %s in phase %s", key, state.CurrentPhase)
	}

	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxPausing); err != nil {
		return err
	}

	state.TargetPhase = sandboxv1alpha1.SandboxPaused
	state.PendingActions = append(state.PendingActions, ActionPause)

	klog.Infof("Pausing sandbox %s", key)
	return nil
}

// ResumeSandbox resumes a paused sandbox.
func (lm *LifecycleManager) ResumeSandbox(ctx context.Context, key string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	return lm.resumeSandboxLocked(ctx, state)
}

// resumeSandboxLocked resumes a paused sandbox (caller must hold the lock).
func (lm *LifecycleManager) resumeSandboxLocked(ctx context.Context, state *SandboxState) error {
	if state.CurrentPhase != sandboxv1alpha1.SandboxPaused {
		return fmt.Errorf("cannot resume sandbox %s in phase %s", state.Key, state.CurrentPhase)
	}

	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxResuming); err != nil {
		return err
	}

	state.TargetPhase = sandboxv1alpha1.SandboxRunning
	state.PendingActions = append(state.PendingActions, ActionResume)

	klog.Infof("Resuming sandbox %s", state.Key)
	return nil
}

// DeleteSandbox marks a sandbox for deletion.
func (lm *LifecycleManager) DeleteSandbox(ctx context.Context, key string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	// If already terminating, nothing to do
	if state.CurrentPhase == sandboxv1alpha1.SandboxTerminating {
		return nil
	}

	now := time.Now()
	state.DeletionTimestamp = &now

	// If running, stop first then terminate
	if state.CurrentPhase == sandboxv1alpha1.SandboxRunning || state.CurrentPhase == sandboxv1alpha1.SandboxPaused {
		if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxStopping); err != nil {
			return err
		}
		state.TargetPhase = sandboxv1alpha1.SandboxTerminating
		state.PendingActions = append(state.PendingActions, ActionStop, ActionDelete)
	} else {
		if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxTerminating); err != nil {
			return err
		}
		state.PendingActions = append(state.PendingActions, ActionDelete)
	}

	klog.Infof("Deleting sandbox %s", key)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Normal", "SandboxDeleting", fmt.Sprintf("Sandbox %s is being deleted", key))
	}

	return nil
}

// EvictSandbox evicts a sandbox from its current node.
func (lm *LifecycleManager) EvictSandbox(ctx context.Context, key string, reason string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxRunning && state.CurrentPhase != sandboxv1alpha1.SandboxPaused {
		return fmt.Errorf("cannot evict sandbox %s in phase %s", key, state.CurrentPhase)
	}

	// Release resources from the current node
	if state.NodeName != "" {
		lm.tenantManager.ReleaseResources(state.Sandbox.Spec.TenantRef.Name, state.NodeName, &state.Sandbox.Spec.Resources)
	}

	// Transition to evicted
	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxEvicted); err != nil {
		return err
	}

	state.NodeName = ""
	state.RuntimeHandle = nil

	// Update sandbox status
	state.Sandbox.Status.EvictionInfo = &sandboxv1alpha1.EvictionInfo{
		Reason:   reason,
		Time:     metav1.Now(),
		NodeName: state.NodeName,
	}

	klog.Infof("Evicted sandbox %s: %s", key, reason)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Warning", "SandboxEvicted", fmt.Sprintf("Sandbox %s evicted: %s", key, reason))
	}

	return nil
}

// ScheduleSandbox marks a sandbox as scheduled to a specific node.
func (lm *LifecycleManager) ScheduleSandbox(ctx context.Context, key string, nodeName string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxPending && state.CurrentPhase != sandboxv1alpha1.SandboxScheduling {
		return fmt.Errorf("cannot schedule sandbox %s in phase %s", key, state.CurrentPhase)
	}

	// Allocate resources on the target node
	if err := lm.tenantManager.AllocateResources(state.Sandbox.Spec.TenantRef.Name, nodeName, &state.Sandbox.Spec.Resources); err != nil {
		return fmt.Errorf("failed to allocate resources: %w", err)
	}

	state.ScheduledNode = nodeName
	state.LastScheduledTime = time.Now()

	// Transition to Creating
	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxCreating); err != nil {
		// Rollback resource allocation
		lm.tenantManager.ReleaseResources(state.Sandbox.Spec.TenantRef.Name, nodeName, &state.Sandbox.Spec.Resources)
		return err
	}

	state.PendingActions = append(state.PendingActions, ActionCreate)

	klog.Infof("Scheduled sandbox %s to node %s", key, nodeName)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Normal", "SandboxScheduled", fmt.Sprintf("Sandbox %s scheduled to node %s", key, nodeName))
	}

	return nil
}

// MarkSandboxRunning marks a sandbox as successfully running.
func (lm *LifecycleManager) MarkSandboxRunning(ctx context.Context, key string, handle runtime.RuntimeHandle) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	if state.CurrentPhase != sandboxv1alpha1.SandboxCreating && state.CurrentPhase != sandboxv1alpha1.SandboxResuming {
		return fmt.Errorf("cannot mark sandbox %s as running in phase %s", key, state.CurrentPhase)
	}

	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxRunning); err != nil {
		return err
	}

	state.RuntimeHandle = handle
	state.NodeName = state.ScheduledNode
	state.StartTime = time.Now()

	klog.Infof("Sandbox %s is now running on node %s", key, state.NodeName)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Normal", "SandboxRunning", fmt.Sprintf("Sandbox %s is running", key))
	}

	return nil
}

// MarkSandboxFailed marks a sandbox as failed.
func (lm *LifecycleManager) MarkSandboxFailed(ctx context.Context, key string, reason string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found", key)
	}

	// Release resources if sandbox was running
	if state.NodeName != "" {
		lm.tenantManager.ReleaseResources(state.Sandbox.Spec.TenantRef.Name, state.NodeName, &state.Sandbox.Spec.Resources)
	}

	if err := lm.transitionPhase(state, sandboxv1alpha1.SandboxFailed); err != nil {
		return err
	}

	state.Sandbox.Status.Reason = reason
	state.Sandbox.Status.Message = reason
	state.RetryCount++

	klog.Warningf("Sandbox %s failed: %s (retry count: %d)", key, reason, state.RetryCount)
	if lm.eventRecorder != nil {
		lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
			"Warning", "SandboxFailed", fmt.Sprintf("Sandbox %s failed: %s", key, reason))
	}

	// Schedule retry if within limits
	if state.RetryCount < lm.maxRetryCount {
		state.PendingActions = append(state.PendingActions, ActionRetry)
	}

	return nil
}

// GetSandboxState returns the state of a sandbox.
func (lm *LifecycleManager) GetSandboxState(key string) (*SandboxState, bool) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return nil, false
	}

	// Return a copy
	copy := *state
	return &copy, true
}

// ListSandboxesByPhase returns all sandboxes in a given phase.
func (lm *LifecycleManager) ListSandboxesByPhase(phase sandboxv1alpha1.SandboxPhase) []*SandboxState {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	result := make([]*SandboxState, 0)
	for _, state := range lm.sandboxes {
		if state.CurrentPhase == phase {
			copy := *state
			result = append(result, &copy)
		}
	}
	return result
}

// ListSandboxesByTenant returns all sandboxes for a given tenant.
func (lm *LifecycleManager) ListSandboxesByTenant(tenantName string) []*SandboxState {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	result := make([]*SandboxState, 0)
	for _, state := range lm.sandboxes {
		if state.Sandbox.Spec.TenantRef.Name == tenantName {
			copy := *state
			result = append(result, &copy)
		}
	}
	return result
}

// ListSandboxesByNode returns all sandboxes on a given node.
func (lm *LifecycleManager) ListSandboxesByNode(nodeName string) []*SandboxState {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	result := make([]*SandboxState, 0)
	for _, state := range lm.sandboxes {
		if state.NodeName == nodeName {
			copy := *state
			result = append(result, &copy)
		}
	}
	return result
}

// transitionPhase transitions a sandbox from one phase to another.
func (lm *LifecycleManager) transitionPhase(state *SandboxState, newPhase sandboxv1alpha1.SandboxPhase) error {
	transition := PhaseTransition{From: state.CurrentPhase, To: newPhase}

	if !validTransitions[transition] {
		return fmt.Errorf("invalid phase transition for sandbox %s: %s -> %s",
			state.Key, state.CurrentPhase, newPhase)
	}

	klog.V(4).Infof("Transitioning sandbox %s from %s to %s", state.Key, state.CurrentPhase, newPhase)

	state.CurrentPhase = newPhase
	state.LastTransitionTime = time.Now()

	// Update the sandbox CRD status
	state.Sandbox.Status.Phase = newPhase

	return nil
}

// checkTimeouts checks for sandboxes that have exceeded their timeout.
func (lm *LifecycleManager) checkTimeouts() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now()

	for key, state := range lm.sandboxes {
		// Check creation timeout (5 minutes)
		if state.CurrentPhase == sandboxv1alpha1.SandboxCreating {
			if now.Sub(state.LastTransitionTime) > 5*time.Minute {
				klog.Warningf("Sandbox %s creation timed out", key)
				lm.transitionPhase(state, sandboxv1alpha1.SandboxFailed)
				state.Sandbox.Status.Reason = "CreationTimeout"
				state.Sandbox.Status.Message = "Sandbox creation timed out"
			}
		}

		// Check scheduling timeout (2 minutes)
		if state.CurrentPhase == sandboxv1alpha1.SandboxScheduling {
			if now.Sub(state.LastTransitionTime) > 2*time.Minute {
				klog.Warningf("Sandbox %s scheduling timed out", key)
				lm.transitionPhase(state, sandboxv1alpha1.SandboxFailed)
				state.Sandbox.Status.Reason = "SchedulingTimeout"
				state.Sandbox.Status.Message = "Sandbox scheduling timed out"
			}
		}

		// Check graceful shutdown timeout
		if state.CurrentPhase == sandboxv1alpha1.SandboxStopping {
			gracePeriod := int64(30)
			if state.Sandbox.Spec.GracefulShutdownSeconds != nil {
				gracePeriod = *state.Sandbox.Spec.GracefulShutdownSeconds
			}
			if now.Sub(state.LastTransitionTime) > time.Duration(gracePeriod)*time.Second {
				klog.Warningf("Sandbox %s graceful shutdown timed out, forcing stop", key)
				// Force stop the sandbox
				if state.RuntimeHandle != nil {
					state.RuntimeHandle.ForceStop(context.Background())
				}
				lm.transitionPhase(state, sandboxv1alpha1.SandboxStopped)
			}
		}
	}
}

// checkIdleSandboxes checks for sandboxes that have been idle.
func (lm *LifecycleManager) checkIdleSandboxes() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for key, state := range lm.sandboxes {
		if state.CurrentPhase != sandboxv1alpha1.SandboxRunning {
			continue
		}

		if state.Sandbox.Spec.IdleTimeoutSeconds == nil {
			continue
		}

		idleTimeout := time.Duration(*state.Sandbox.Spec.IdleTimeoutSeconds) * time.Second

		// Check if sandbox has been idle
		if !state.IdleSince.IsZero() && time.Since(state.IdleSince) > idleTimeout {
			klog.Infof("Sandbox %s idle timeout exceeded, pausing", key)
			lm.transitionPhase(state, sandboxv1alpha1.SandboxPausing)
			state.TargetPhase = sandboxv1alpha1.SandboxPaused
			state.PendingActions = append(state.PendingActions, ActionPause)
		}
	}
}

// checkExpirations checks for sandboxes that have exceeded their max lifetime.
func (lm *LifecycleManager) checkExpirations() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	now := time.Now()

	for key, state := range lm.sandboxes {
		if state.CurrentPhase != sandboxv1alpha1.SandboxRunning && state.CurrentPhase != sandboxv1alpha1.SandboxPaused {
			continue
		}

		if state.ExpirationTime.IsZero() {
			continue
		}

		if now.After(state.ExpirationTime) {
			klog.Infof("Sandbox %s max lifetime exceeded, terminating", key)
			lm.transitionPhase(state, sandboxv1alpha1.SandboxStopping)
			state.TargetPhase = sandboxv1alpha1.SandboxTerminating
			state.PendingActions = append(state.PendingActions, ActionStop, ActionDelete)

			if lm.eventRecorder != nil {
				lm.eventRecorder.RecordSandboxEvent(state.Sandbox.Name, state.Sandbox.Namespace,
					"Normal", "SandboxExpired", fmt.Sprintf("Sandbox %s max lifetime exceeded", key))
			}
		}
	}
}

// processRetries processes sandbox retries.
func (lm *LifecycleManager) processRetries() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for key, state := range lm.sandboxes {
		if state.CurrentPhase != sandboxv1alpha1.SandboxFailed {
			continue
		}

		if state.RetryCount >= lm.maxRetryCount {
			continue
		}

		// Calculate backoff
		backoff := lm.retryBackoff * time.Duration(1<<uint(state.RetryCount-1))
		if backoff > lm.maxRetryBackoff {
			backoff = lm.maxRetryBackoff
		}

		// Check if enough time has passed since the last failure
		if time.Since(state.LastTransitionTime) < backoff {
			continue
		}

		klog.Infof("Retrying sandbox %s (attempt %d/%d)", key, state.RetryCount+1, lm.maxRetryCount)

		// Reset to Pending for rescheduling
		lm.transitionPhase(state, sandboxv1alpha1.SandboxPending)
		state.TargetPhase = sandboxv1alpha1.SandboxRunning
		state.PendingActions = append(state.PendingActions, ActionSchedule)
		state.Sandbox.Status.RetryCount = state.RetryCount
	}
}

// cleanupCompletedSandboxes removes completed sandboxes marked for auto-deletion.
func (lm *LifecycleManager) cleanupCompletedSandboxes() {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for key, state := range lm.sandboxes {
		if !state.Sandbox.Spec.AutoDeleteOnCompletion {
			continue
		}

		if state.CurrentPhase == sandboxv1alpha1.SandboxStopped || state.CurrentPhase == sandboxv1alpha1.SandboxFailed {
			if state.DeletionTimestamp != nil {
				continue
			}

			klog.Infof("Auto-deleting completed sandbox %s", key)
			now := time.Now()
			state.DeletionTimestamp = &now
			lm.transitionPhase(state, sandboxv1alpha1.SandboxTerminating)
			state.PendingActions = append(state.PendingActions, ActionDelete)
		}
	}
}

// onSandboxAdd handles sandbox addition events.
func (lm *LifecycleManager) onSandboxAdd(obj interface{}) {
	sb, ok := obj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	ctx := context.Background()
	if err := lm.CreateSandbox(ctx, sb); err != nil {
		klog.Errorf("Failed to create sandbox %s: %v", sb.Name, err)
	}
}

// onSandboxUpdate handles sandbox update events.
func (lm *LifecycleManager) onSandboxUpdate(oldObj, newObj interface{}) {
	// Handle sandbox spec/status updates
	newSB, ok := newObj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	key := newSB.Namespace + "/" + newSB.Name

	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return
	}

	// Update the sandbox object
	state.Sandbox = newSB

	// Check for deletion timestamp
	if newSB.DeletionTimestamp != nil && state.DeletionTimestamp == nil {
		now := time.Now()
		state.DeletionTimestamp = &now
		state.TargetPhase = sandboxv1alpha1.SandboxTerminating
	}
}

// onSandboxDelete handles sandbox deletion events.
func (lm *LifecycleManager) onSandboxDelete(obj interface{}) {
	sb, ok := obj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	key := sb.Namespace + "/" + sb.Name

	lm.mu.Lock()
	defer lm.mu.Unlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return
	}

	// Release resources
	if state.NodeName != "" {
		lm.tenantManager.ReleaseResources(sb.Spec.TenantRef.Name, state.NodeName, &sb.Spec.Resources)
	}

	// Clean up runtime
	if state.RuntimeHandle != nil {
		state.RuntimeHandle.Cleanup(context.Background())
	}

	delete(lm.sandboxes, key)
	klog.Infof("Deleted sandbox %s", key)
}

// GetSandboxCount returns the count of sandboxes in each phase.
func (lm *LifecycleManager) GetSandboxCount() map[sandboxv1alpha1.SandboxPhase]int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	counts := make(map[sandboxv1alpha1.SandboxPhase]int)
	for _, state := range lm.sandboxes {
		counts[state.CurrentPhase]++
	}
	return counts
}

// BuildSandboxStatus builds the full status for a sandbox.
func (lm *LifecycleManager) BuildSandboxStatus(key string) (*sandboxv1alpha1.SandboxStatus, error) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()

	state, exists := lm.sandboxes[key]
	if !exists {
		return nil, fmt.Errorf("sandbox %s not found", key)
	}

	status := &sandboxv1alpha1.SandboxStatus{
		Phase:      state.CurrentPhase,
		NodeName:   state.NodeName,
		RetryCount: state.RetryCount,
		Reason:     state.Sandbox.Status.Reason,
		Message:    state.Sandbox.Status.Message,
	}

	if !state.StartTime.IsZero() {
		startTime := metav1.NewTime(state.StartTime)
		status.StartTime = &startTime
	}

	if !state.LastTransitionTime.IsZero() {
		status.LastScheduledTime = &metav1.Time{Time: state.LastScheduledTime}
	}

	// Build conditions
	status.Conditions = lm.buildSandboxConditions(state)

	return status, nil
}

// buildSandboxConditions builds conditions for a sandbox.
func (lm *LifecycleManager) buildSandboxConditions(state *SandboxState) []sandboxv1alpha1.SandboxCondition {
	now := metav1.Now()
	conditions := []sandboxv1alpha1.SandboxCondition{}

	// Scheduled condition
	scheduled := state.CurrentPhase != sandboxv1alpha1.SandboxPending
	conditions = append(conditions, sandboxv1alpha1.SandboxCondition{
		Type:               sandboxv1alpha1.SandboxConditionScheduled,
		Status:             boolToConditionStatus(scheduled),
		LastTransitionTime: now,
		Reason: func() string {
			if scheduled {
				return "SandboxScheduled"
			}
			return "SandboxNotScheduled"
		}(),
	})

	// Ready condition
	ready := state.CurrentPhase == sandboxv1alpha1.SandboxRunning
	conditions = append(conditions, sandboxv1alpha1.SandboxCondition{
		Type:               sandboxv1alpha1.SandboxConditionReady,
		Status:             boolToConditionStatus(ready),
		LastTransitionTime: now,
		Reason: func() string {
			if ready {
				return "SandboxReady"
			}
			return "SandboxNotReady"
		}(),
	})

	// RuntimeReady condition
	runtimeReady := state.RuntimeHandle != nil && state.RuntimeHandle.IsReady()
	conditions = append(conditions, sandboxv1alpha1.SandboxCondition{
		Type:               sandboxv1alpha1.SandboxConditionRuntimeReady,
		Status:             boolToConditionStatus(runtimeReady),
		LastTransitionTime: now,
		Reason: func() string {
			if runtimeReady {
				return "RuntimeReady"
			}
			return "RuntimeNotReady"
		}(),
	})

	return conditions
}

func boolToConditionStatus(b bool) sandboxv1alpha1.ConditionStatus {
	if b {
		return sandboxv1alpha1.ConditionTrue
	}
	return sandboxv1alpha1.ConditionFalse
}
