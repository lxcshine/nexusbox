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
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
	"github.com/nexusbox/nexusbox/pkg/scheduler/queue"
)

// Scheduler is the main sandbox scheduler. It watches for unscheduled sandboxes
// and schedules them to appropriate nodes using the scheduling framework.
//
// The scheduling process follows these phases (inspired by Kubernetes 1.23.17 scheduler):
// 1. Queue: Sandboxes enter the scheduling queue
// 2. PreFilter: Pre-filter checks (tenant validation, quota checks)
// 3. Filter: Filter out nodes that don't meet requirements
// 4. PreScore: Pre-scoring preparation
// 5. Score: Score remaining nodes
// 6. NormalizeScore: Normalize scores across plugins
// 7. Select: Select the best node
// 8. Reserve: Reserve resources on the selected node
// 9. Permit: Permit or reject the scheduling decision
// 10. Bind: Bind the sandbox to the node
// 11. PostBind: Post-bind actions
type Scheduler struct {
	// fwk is the scheduling framework.
	fwk framework.Framework

	// schedulerQueue is the queue of sandboxes to be scheduled.
	schedulerQueue queue.SchedulingQueue

	// informer watches for sandbox CRD changes.
	informer cache.SharedIndexInformer

	// stopCh is used to signal shutdown.
	stopCh chan struct{}

	// maxSchedulingAttempts is the maximum number of scheduling attempts.
	maxSchedulingAttempts int32

	// nextStart indicates when to start the next scheduling cycle.
	nextStart time.Time

	// metrics records scheduling metrics.
	metrics *SchedulerMetrics
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	fwk framework.Framework,
	schedulerQueue queue.SchedulingQueue,
	informer cache.SharedIndexInformer,
	maxAttempts int32,
) *Scheduler {
	s := &Scheduler{
		fwk:                   fwk,
		schedulerQueue:        schedulerQueue,
		informer:              informer,
		maxSchedulingAttempts: maxAttempts,
		metrics:               &SchedulerMetrics{},
		stopCh:                make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    s.onSandboxAdd,
			UpdateFunc: s.onSandboxUpdate,
			DeleteFunc: s.onSandboxDelete,
		})
	}

	return s
}

// Start starts the scheduler.
func (s *Scheduler) Start(ctx context.Context) {
	klog.Info("Starting sandbox scheduler")

	// Start scheduling loop
	go wait.Until(s.schedulingCycle, 100*time.Millisecond, s.stopCh)

	// Start metrics collection
	go wait.Until(s.collectMetrics, 30*time.Second, s.stopCh)

	klog.Info("Sandbox scheduler started")
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	klog.Info("Stopping sandbox scheduler")
	close(s.stopCh)
	s.schedulerQueue.Close()
}

// schedulingCycle runs one iteration of the scheduling cycle.
func (s *Scheduler) schedulingCycle() {
	// Get the next sandbox from the queue
	sandboxInfo, err := s.schedulerQueue.Pop()
	if err != nil {
		klog.Errorf("Failed to pop from scheduling queue: %v", err)
		return
	}

	if sandboxInfo == nil {
		return
	}

	sandbox := sandboxInfo.Sandbox
	key := sandbox.Namespace + "/" + sandbox.Name

	klog.V(4).Infof("Scheduling sandbox %s", key)

	startTime := time.Now()

	// Run the scheduling cycle
	result, err := s.scheduleSandbox(context.Background(), sandboxInfo)

	elapsed := time.Since(startTime)

	if err != nil {
		klog.Errorf("Failed to schedule sandbox %s: %v", key, err)
		s.handleSchedulingFailure(sandboxInfo, err)
		return
	}

	if result.EvaluatedNodes == 0 {
		klog.Warningf("No nodes available for sandbox %s", key)
		s.handleSchedulingFailure(sandboxInfo, fmt.Errorf("no nodes available"))
		return
	}

	// Bind the sandbox to the selected node
	if err := s.bindSandbox(context.Background(), sandboxInfo, result.SuggestedHost); err != nil {
		klog.Errorf("Failed to bind sandbox %s to node %s: %v", key, result.SuggestedHost, err)
		s.handleSchedulingFailure(sandboxInfo, err)
		return
	}

	// Update metrics
	s.metrics.RecordSchedulingAttempt("success", elapsed)

	klog.Infof("Scheduled sandbox %s to node %s in %v (evaluated %d nodes, feasible %d nodes)",
		key, result.SuggestedHost, elapsed, result.EvaluatedNodes, result.FeasibleNodes)
}

// ScheduleResult represents the result of a scheduling cycle.
type ScheduleResult struct {
	// SuggestedHost is the suggested node for the sandbox.
	SuggestedHost string
	// EvaluatedNodes is the number of nodes evaluated.
	EvaluatedNodes int
	// FeasibleNodes is the number of feasible nodes.
	FeasibleNodes int
}

// scheduleSandbox runs the scheduling algorithm for a sandbox.
func (s *Scheduler) scheduleSandbox(ctx context.Context, sandboxInfo *framework.SandboxInfo) (result ScheduleResult, err error) {
	// Step 1: Run PreFilter plugins
	preFilterResult := s.fwk.RunPreFilterPlugins(ctx, sandboxInfo)
	if !preFilterResult.Success() {
		return result, fmt.Errorf("PreFilter failed: %v", preFilterResult.Reasons())
	}

	// Step 2: Find feasible nodes (Filter phase)
	feasibleNodes, err := s.findFeasibleNodes(ctx, sandboxInfo)
	if err != nil {
		return result, fmt.Errorf("Filter phase failed: %w", err)
	}

	nodeInfos, _ := s.fwk.SnapshotSharedLister().NodeInfos().List()
	if len(feasibleNodes) == 0 {
		return ScheduleResult{EvaluatedNodes: len(nodeInfos)}, fmt.Errorf("no feasible nodes found")
	}

	// Step 3: Run PreScore plugins
	preScoreResult := s.fwk.RunPreScorePlugins(ctx, sandboxInfo, feasibleNodes)
	if !preScoreResult.Success() {
		return result, fmt.Errorf("PreScore failed: %v", preScoreResult.Reasons())
	}

	// Step 4: Score feasible nodes
	scoreList, err := s.prioritizeNodes(ctx, sandboxInfo, feasibleNodes)
	if err != nil {
		return result, fmt.Errorf("Score phase failed: %w", err)
	}

	// Step 5: Select the best node
	selectedNode, err := s.selectNode(ctx, sandboxInfo, scoreList)
	if err != nil {
		return result, fmt.Errorf("Select phase failed: %w", err)
	}

	// Step 6: Run Reserve plugins
	reserveResult := s.fwk.RunReservePluginsReserve(ctx, sandboxInfo, selectedNode)
	if !reserveResult.Success() {
		return result, fmt.Errorf("Reserve failed: %v", reserveResult.Reasons())
	}

	// Step 7: Run Permit plugins
	permitResult := s.fwk.RunPermitPlugins(ctx, sandboxInfo, selectedNode)
	if !permitResult.Success() {
		// Unreserve
		s.fwk.RunReservePluginsUnreserve(ctx, sandboxInfo, selectedNode)
		return result, fmt.Errorf("Permit failed: %v", permitResult.Reasons())
	}

	return ScheduleResult{
		SuggestedHost:  selectedNode,
		EvaluatedNodes: len(nodeInfos),
		FeasibleNodes:  len(feasibleNodes),
	}, nil
}

// findFeasibleNodes finds all nodes that can accommodate the sandbox.
func (s *Scheduler) findFeasibleNodes(ctx context.Context, sandboxInfo *framework.SandboxInfo) ([]*framework.NodeInfo, error) {
	allNodes, err := s.fwk.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	feasibleNodes := make([]*framework.NodeInfo, 0, len(allNodes))

	for _, nodeInfo := range allNodes {
		// Run Filter plugins for this node
		filterResult := s.fwk.RunFilterPlugins(ctx, sandboxInfo, nodeInfo)
		if filterResult.Success() {
			feasibleNodes = append(feasibleNodes, nodeInfo)
		}
	}

	return feasibleNodes, nil
}

// prioritizeNodes scores feasible nodes and returns a prioritized list.
func (s *Scheduler) prioritizeNodes(ctx context.Context, sandboxInfo *framework.SandboxInfo, feasibleNodes []*framework.NodeInfo) (framework.NodeScoreList, error) {
	// Run Score plugins
	scoreList, err := s.fwk.RunScorePlugins(ctx, sandboxInfo, feasibleNodes)
	if err != nil {
		return nil, err
	}

	// Normalize scores
	if err := s.fwk.RunNormalizeScorePlugins(ctx, sandboxInfo, scoreList); err != nil {
		return nil, fmt.Errorf("NormalizeScore failed: %w", err)
	}

	return scoreList, nil
}

// selectNode selects the best node from the scored list.
func (s *Scheduler) selectNode(ctx context.Context, sandboxInfo *framework.SandboxInfo, scoreList framework.NodeScoreList) (string, error) {
	if len(scoreList) == 0 {
		return "", fmt.Errorf("no nodes to select from")
	}

	// Select the node with the highest score
	bestNode := scoreList[0]
	for _, nodeScore := range scoreList[1:] {
		if nodeScore.Score > bestNode.Score {
			bestNode = nodeScore
		}
	}

	return bestNode.Name, nil
}

// bindSandbox binds a sandbox to a node.
func (s *Scheduler) bindSandbox(ctx context.Context, sandboxInfo *framework.SandboxInfo, nodeName string) error {
	startTime := time.Now()

	// Run PreBind plugins
	preBindResult := s.fwk.RunPreBindPlugins(ctx, sandboxInfo, nodeName)
	if !preBindResult.Success() {
		s.fwk.RunReservePluginsUnreserve(ctx, sandboxInfo, nodeName)
		return fmt.Errorf("PreBind failed: %v", preBindResult.Reasons())
	}

	// Run Bind plugins
	bindResult := s.fwk.RunBindPlugins(ctx, sandboxInfo, nodeName)
	if !bindResult.Success() {
		s.fwk.RunReservePluginsUnreserve(ctx, sandboxInfo, nodeName)
		return fmt.Errorf("Bind failed: %v", bindResult.Reasons())
	}

	// Run PostBind plugins
	s.fwk.RunPostBindPlugins(ctx, sandboxInfo, nodeName)

	elapsed := time.Since(startTime)
	s.metrics.RecordSchedulingAttempt("success", elapsed)

	return nil
}

// handleSchedulingFailure handles a scheduling failure.
func (s *Scheduler) handleSchedulingFailure(sandboxInfo *framework.SandboxInfo, err error) {
	s.metrics.RecordSchedulingAttempt("failure", 0)

	sandbox := sandboxInfo.Sandbox
	key := sandbox.Namespace + "/" + sandbox.Name

	// Check if we should retry
	if int32(sandboxInfo.Attempts) < s.maxSchedulingAttempts {
		// Re-queue with backoff
		s.schedulerQueue.AddUnschedulableIfNotPresent(sandboxInfo)
		klog.V(4).Infof("Re-queued sandbox %s for retry (attempt %d/%d)",
			key, sandboxInfo.Attempts, s.maxSchedulingAttempts)
	} else {
		s.metrics.RecordSchedulingAttempt("unschedulable", 0)

		klog.Warningf("Sandbox %s is unschedulable after %d attempts: %v",
			key, sandboxInfo.Attempts, err)
	}
}

// onSandboxAdd handles sandbox addition events.
func (s *Scheduler) onSandboxAdd(obj interface{}) {
	sb, ok := obj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	// Only schedule sandboxes that are in Pending phase
	if sb.Status.Phase != sandboxv1alpha1.SandboxPending {
		return
	}

	sandboxInfo := &framework.SandboxInfo{
		Sandbox:  sb,
		Attempts: 1,
	}

	s.schedulerQueue.Add(sandboxInfo)
}

// onSandboxUpdate handles sandbox update events.
func (s *Scheduler) onSandboxUpdate(oldObj, newObj interface{}) {
	newSB, ok := newObj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	// If sandbox is pending and not yet scheduled, add to queue
	if newSB.Status.Phase == sandboxv1alpha1.SandboxPending && newSB.Status.NodeName == "" {
		sandboxInfo := &framework.SandboxInfo{
			Sandbox:  newSB,
			Attempts: 1,
		}
		s.schedulerQueue.Add(sandboxInfo)
	}
}

// onSandboxDelete handles sandbox deletion events.
func (s *Scheduler) onSandboxDelete(obj interface{}) {
	// Clean up from queue if present
	sb, ok := obj.(*sandboxv1alpha1.Sandbox)
	if !ok {
		return
	}

	key := sb.Namespace + "/" + sb.Name
	s.schedulerQueue.Delete(key)
}

// collectMetrics collects and logs scheduling metrics.
func (s *Scheduler) collectMetrics() {
	klog.V(4).Infof("Scheduler metrics: attempts=%d, successes=%d, failures=%d, unschedulable=%d",
		s.metrics.schedulingAttempts.Load(),
		s.metrics.schedulingSuccesses.Load(),
		s.metrics.schedulingFailures.Load(),
		s.metrics.schedulingUnschedulable.Load())
}

// GetMetrics returns the current scheduling metrics.
func (s *Scheduler) GetMetrics() *SchedulerMetrics {
	return s.metrics
}
