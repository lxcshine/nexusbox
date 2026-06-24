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

package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/cache"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
	"github.com/nexusbox/nexusbox/pkg/scheduler/queue"
)

// ScaleController manages scaling of sandbox resources.
// It monitors the scheduling queue and node capacity to make
// scaling decisions, including preemption and node autoscaling hints.
type ScaleController struct {
	mu sync.RWMutex

	// scheduler is the parent scheduler.
	scheduler *Scheduler

	// cache is the scheduler cache.
	cache *cache.Cache

	// queue is the scheduling queue.
	queue queue.SchedulingQueue

	// config holds the scale controller configuration.
	config *ScaleControllerConfig

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// ScaleControllerConfig holds configuration for the scale controller.
type ScaleControllerConfig struct {
	// CheckInterval is how often to check for scaling needs.
	CheckInterval time.Duration

	// PendingThreshold is the number of pending sandboxes that triggers scaling.
	PendingThreshold int32

	// UnschedulableThreshold is the number of unschedulable sandboxes that triggers scaling.
	UnschedulableThreshold int32

	// CooldownPeriod is the minimum time between scaling actions.
	CooldownPeriod time.Duration

	// MaxNodes is the maximum number of nodes to scale to.
	MaxNodes int32

	// MinNodes is the minimum number of nodes to maintain.
	MinNodes int32

	// EnablePreemption enables preemption for higher priority sandboxes.
	EnablePreemption bool

	// PreemptionWaitTimeout is how long to wait before preempting.
	PreemptionWaitTimeout time.Duration
}

// DefaultScaleControllerConfig returns default configuration.
func DefaultScaleControllerConfig() *ScaleControllerConfig {
	return &ScaleControllerConfig{
		CheckInterval:         30 * time.Second,
		PendingThreshold:      50,
		UnschedulableThreshold: 10,
		CooldownPeriod:        5 * time.Minute,
		MaxNodes:              100,
		MinNodes:              3,
		EnablePreemption:      true,
		PreemptionWaitTimeout: 60 * time.Second,
	}
}

// NewScaleController creates a new ScaleController.
func NewScaleController(sched *Scheduler, cache *cache.Cache, q queue.SchedulingQueue, config *ScaleControllerConfig) *ScaleController {
	if config == nil {
		config = DefaultScaleControllerConfig()
	}
	return &ScaleController{
		scheduler: sched,
		cache:     cache,
		queue:     q,
		config:    config,
		stopCh:    make(chan struct{}),
	}
}

// Start starts the scale controller.
func (sc *ScaleController) Start(ctx context.Context) {
	go sc.run(ctx)
	klog.Info("Scale controller started")
}

// Stop stops the scale controller.
func (sc *ScaleController) Stop() {
	close(sc.stopCh)
	klog.Info("Scale controller stopped")
}

// run is the main loop for the scale controller.
func (sc *ScaleController) run(ctx context.Context) {
	ticker := time.NewTicker(sc.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sc.checkScaling(ctx)
		case <-sc.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// checkScaling checks if scaling is needed.
func (sc *ScaleController) checkScaling(ctx context.Context) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	pendingCount := sc.queue.Len()
	unschedulableCount := sc.queue.UnschedulableLen()
	nodeCount := sc.cache.NodeCount()

	klog.V(4).Infof("Scale check: pending=%d, unschedulable=%d, nodes=%d",
		pendingCount, unschedulableCount, nodeCount)

	// Check if we need to scale up
	if pendingCount > int(sc.config.PendingThreshold) ||
		unschedulableCount > int(sc.config.UnschedulableThreshold) {
		sc.recommendScaleUp(ctx, pendingCount, unschedulableCount, nodeCount)
	}

	// Check if we can scale down
	if nodeCount > int(sc.config.MinNodes) && pendingCount == 0 && unschedulableCount == 0 {
		sc.recommendScaleDown(ctx, nodeCount)
	}

	// Check for preemption opportunities
	if sc.config.EnablePreemption && unschedulableCount > 0 {
		sc.checkPreemption(ctx, unschedulableCount)
	}
}

// recommendScaleUp recommends scaling up the cluster.
func (sc *ScaleController) recommendScaleUp(ctx context.Context, pendingCount, unschedulableCount, nodeCount int) {
	// Calculate how many nodes we need
	// This is a simplified calculation; in production, consider resource types
	neededNodes := (pendingCount + unschedulableCount) / 10 // Assume ~10 sandboxes per node
	if neededNodes < 1 {
		neededNodes = 1
	}

	// Cap at max nodes
	if int32(nodeCount)+int32(neededNodes) > sc.config.MaxNodes {
		neededNodes = int(sc.config.MaxNodes - int32(nodeCount))
	}

	if neededNodes > 0 {
		klog.Infof("Scale-up recommendation: add %d nodes (pending: %d, unschedulable: %d, current: %d)",
			neededNodes, pendingCount, unschedulableCount, nodeCount)

		// In production, this would trigger an autoscaling event
		// or call the cluster autoscaler API
	}
}

// recommendScaleDown recommends scaling down the cluster.
func (sc *ScaleController) recommendScaleDown(ctx context.Context, nodeCount int) {
	// Find idle nodes
	snapshot := sc.cache.Snapshot()
	idleNodes := 0

	for _, nodeState := range snapshot.Nodes {
		if nodeState.SandboxCount == 0 {
			idleNodes++
		}
	}

	// Only recommend scaling down if there are idle nodes
	if idleNodes > 0 && nodeCount-idleNodes >= int(sc.config.MinNodes) {
		klog.Infof("Scale-down recommendation: remove %d idle nodes (current: %d)",
			idleNodes, nodeCount)
	}
}

// checkPreemption checks if preemption can help unschedulable sandboxes.
func (sc *ScaleController) checkPreemption(ctx context.Context, unschedulableCount int) {
	klog.V(4).Infof("Checking preemption for %d unschedulable sandboxes", unschedulableCount)

	// In production, this would:
	// 1. Identify high-priority sandboxes that can't be scheduled
	// 2. Find lower-priority sandboxes that can be preempted
	// 3. Calculate the minimum set of preemptions needed
	// 4. Execute the preemptions
}

// PreemptionCandidate represents a sandbox that could be preempted.
type PreemptionCandidate struct {
	// SandboxName is the name of the sandbox to preempt.
	SandboxName string

	// Namespace is the namespace of the sandbox.
	Namespace string

	// NodeName is the node the sandbox is on.
	NodeName string

	// Priority is the priority of the sandbox.
	Priority int32

	// ResourceRelease is the resource that would be released.
	ResourceRelease *framework.Resource
}

// FindPreemptionCandidates finds sandboxes that can be preempted.
func (sc *ScaleController) FindPreemptionCandidates(sandboxInfo *framework.SandboxInfo) ([]*PreemptionCandidate, error) {
	snapshot := sc.cache.Snapshot()
	var candidates []*PreemptionCandidate

	for _, nodeState := range snapshot.Nodes {
		// Skip nodes that don't support the required runtime
		if sandboxInfo.Sandbox.Spec.Runtime != "" {
			supported := false
			for _, rt := range nodeState.SupportedRuntimes {
				if rt == string(sandboxInfo.Sandbox.Spec.Runtime) {
					supported = true
					break
				}
			}
			if !supported {
				continue
			}
		}

		// Find lower-priority sandboxes on this node
		for _, sb := range nodeState.Sandboxes {
			sbInfo := framework.NewSandboxInfo(sb)
			if sbInfo.Priority < sandboxInfo.Priority {
				candidates = append(candidates, &PreemptionCandidate{
					SandboxName:    sb.Name,
					Namespace:      sb.Namespace,
					NodeName:       nodeState.NodeName,
					Priority:       sbInfo.Priority,
					ResourceRelease: sbInfo.ResourceRequest,
				})
			}
		}
	}

	return candidates, nil
}

// ExecutePreemption preempts the given sandboxes.
func (sc *ScaleController) ExecutePreemption(ctx context.Context, candidates []*PreemptionCandidate) error {
	for _, candidate := range candidates {
		klog.Infof("Preempting sandbox %s/%s on node %s (priority: %d)",
			candidate.Namespace, candidate.SandboxName, candidate.NodeName, candidate.Priority)

		// In production, this would:
		// 1. Evict the sandbox from the node
		// 2. Update the sandbox status
		// 3. Release the resources
		// 4. Re-queue the preempted sandbox
	}

	return nil
}

// TaskReclaimer manages task recycling and cleanup.
type TaskReclaimer struct {
	mu sync.RWMutex

	// cache is the scheduler cache.
	cache *cache.Cache

	// config holds the reclaimer configuration.
	config *ReclaimerConfig

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// ReclaimerConfig holds configuration for the task reclaimer.
type ReclaimerConfig struct {
	// CheckInterval is how often to check for reclaimable tasks.
	CheckInterval time.Duration

	// MaxLifetimeSeconds is the maximum lifetime for a sandbox.
	MaxLifetimeSeconds int64

	// IdleTimeoutSeconds is the idle timeout for a sandbox.
	IdleTimeoutSeconds int64

	// FailedTaskRetentionSeconds is how long to keep failed tasks.
	FailedTaskRetentionSeconds int64

	// EnableAutoReclaim enables automatic task reclamation.
	EnableAutoReclaim bool
}

// DefaultReclaimerConfig returns default configuration.
func DefaultReclaimerConfig() *ReclaimerConfig {
	return &ReclaimerConfig{
		CheckInterval:             60 * time.Second,
		MaxLifetimeSeconds:        86400, // 24 hours
		IdleTimeoutSeconds:        3600,  // 1 hour
		FailedTaskRetentionSeconds: 300,  // 5 minutes
		EnableAutoReclaim:         true,
	}
}

// NewTaskReclaimer creates a new TaskReclaimer.
func NewTaskReclaimer(cache *cache.Cache, config *ReclaimerConfig) *TaskReclaimer {
	if config == nil {
		config = DefaultReclaimerConfig()
	}
	return &TaskReclaimer{
		cache:  cache,
		config: config,
		stopCh: make(chan struct{}),
	}
}

// Start starts the task reclaimer.
func (tr *TaskReclaimer) Start(ctx context.Context) {
	if !tr.config.EnableAutoReclaim {
		klog.Info("Task reclaimer is disabled")
		return
	}

	go tr.run(ctx)
	klog.Info("Task reclaimer started")
}

// Stop stops the task reclaimer.
func (tr *TaskReclaimer) Stop() {
	close(tr.stopCh)
	klog.Info("Task reclaimer stopped")
}

// run is the main loop for the task reclaimer.
func (tr *TaskReclaimer) run(ctx context.Context) {
	ticker := time.NewTicker(tr.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tr.reclaimTasks(ctx)
		case <-tr.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// reclaimTasks checks for and reclaims expired or idle tasks.
func (tr *TaskReclaimer) reclaimTasks(ctx context.Context) {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	snapshot := tr.cache.Snapshot()
	now := time.Now()
	reclaimedCount := 0

	for key, sbState := range snapshot.Sandboxes {
		sb := sbState.Sandbox
		if sb == nil {
			continue
		}

		// Check for expired sandboxes
		if sb.Spec.MaxLifetimeSeconds != nil {
			maxLifetime := time.Duration(*sb.Spec.MaxLifetimeSeconds) * time.Second
			if sbState.Sandbox.Status.StartTime != nil {
				startTime := sb.Status.StartTime.Time
				if now.Sub(startTime) > maxLifetime {
					klog.Infof("Reclaiming expired sandbox %s (lifetime: %v)", key, maxLifetime)
					tr.reclaimSandbox(ctx, sb)
					reclaimedCount++
					continue
				}
			}
		}

		// Check for idle sandboxes
		if sb.Spec.IdleTimeoutSeconds != nil {
			idleTimeout := time.Duration(*sb.Spec.IdleTimeoutSeconds) * time.Second
			// In production, check last activity time
			_ = idleTimeout
		}

		// Check for failed sandboxes past retention
		if sb.Status.Phase == "Failed" {
			if sb.Status.StartTime != nil {
				elapsed := now.Sub(sb.Status.StartTime.Time)
				if elapsed > time.Duration(tr.config.FailedTaskRetentionSeconds)*time.Second {
					klog.Infof("Reclaiming failed sandbox %s (retention exceeded)", key)
					tr.reclaimSandbox(ctx, sb)
					reclaimedCount++
				}
			}
		}
	}

	if reclaimedCount > 0 {
		klog.Infof("Reclaimed %d sandboxes", reclaimedCount)
	}
}

// reclaimSandbox reclaims a single sandbox.
func (tr *TaskReclaimer) reclaimSandbox(ctx context.Context, sb interface{}) {
	// In production, this would:
	// 1. Stop the sandbox runtime
	// 2. Release all resources
	// 3. Update the sandbox status
	// 4. Record the reclamation event
	// 5. Notify the tenant
	_ = fmt.Sprintf("Reclaiming sandbox")
}
