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

package plugins

import (
	"context"
	"fmt"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
)

// ResourceFit is a filter plugin that checks if a node has enough
// resources to accommodate the sandbox.
type ResourceFit struct{}

// NewResourceFit creates a new ResourceFit plugin.
func NewResourceFit() *ResourceFit {
	return &ResourceFit{}
}

func (p *ResourceFit) Name() string { return "ResourceFit" }

// PreFilter checks if the sandbox has valid resource requirements.
func (p *ResourceFit) PreFilter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo) *framework.Result {
	sb := sandboxInfo.Sandbox

	if sb.Spec.Resources.CPU == "" && sb.Spec.Resources.Memory == "" {
		return framework.NewResult("sandbox has no resource requirements")
	}

	// Validate resource quantities
	if sb.Spec.Resources.CPU != "" {
		if _, err := framework.ParseQuantityCPU(sb.Spec.Resources.CPU); err != nil {
			return framework.NewResult(fmt.Sprintf("invalid CPU request %q: %v", sb.Spec.Resources.CPU, err))
		}
	}

	if sb.Spec.Resources.Memory != "" {
		if _, err := framework.ParseQuantityMem(sb.Spec.Resources.Memory); err != nil {
			return framework.NewResult(fmt.Sprintf("invalid memory request %q: %v", sb.Spec.Resources.Memory, err))
		}
	}

	return nil
}

func (p *ResourceFit) PreFilterExtensions() framework.PreFilterExtensions { return nil }

// Filter checks if the node has enough resources for the sandbox.
func (p *ResourceFit) Filter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeInfo *framework.NodeInfo) *framework.Result {
	available := nodeInfo.AvailableResource

	if !available.Fits(&sandboxInfo.Sandbox.Spec.Resources) {
		return framework.NewResult(fmt.Sprintf("insufficient resources on node %s: CPU %dm, Memory %d",
			nodeInfo.Name(), available.MilliCPU, available.Memory))
	}

	return nil
}

// NodeResourcesFit is a score plugin that scores nodes based on resource utilization.
// It favors nodes with more available resources (least requested first).
type NodeResourcesFit struct{}

// NewNodeResourcesFit creates a new NodeResourcesFit plugin.
func NewNodeResourcesFit() *NodeResourcesFit {
	return &NodeResourcesFit{}
}

func (p *NodeResourcesFit) Name() string { return "NodeResourcesFit" }

// Score scores a node based on resource availability.
func (p *NodeResourcesFit) Score(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) (int64, *framework.Result) {
	// This would normally look up the node from the state
	// For now, return a default score
	return 50, nil
}

func (p *NodeResourcesFit) ScoreExtensions() framework.ScoreExtensions { return nil }

// TenantAffinity is a filter plugin that enforces tenant-level
// node affinity and anti-affinity rules.
type TenantAffinity struct{}

// NewTenantAffinity creates a new TenantAffinity plugin.
func NewTenantAffinity() *TenantAffinity {
	return &TenantAffinity{}
}

func (p *TenantAffinity) Name() string { return "TenantAffinity" }

// Filter checks if the node satisfies the tenant's affinity rules.
func (p *TenantAffinity) Filter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeInfo *framework.NodeInfo) *framework.Result {
	sb := sandboxInfo.Sandbox

	// Check tenant node affinity
	if sb.Spec.NodeAffinity != nil && sb.Spec.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		nodeLabels := getNodeLabels(nodeInfo)
		selector := sb.Spec.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		matched := false

		for _, term := range selector.NodeSelectorTerms {
			if matchNodeSelectorTerm(term, nodeLabels) {
				matched = true
				break
			}
		}

		if !matched {
			return framework.NewResult(fmt.Sprintf("node %s does not match tenant affinity rules", nodeInfo.Name()))
		}
	}

	// Check tenant node anti-affinity (using NodeSelector for anti-affinity)
	if sb.Spec.SchedulingPolicy == sandboxv1alpha1.ScheduleTenantAntiAffinity {
		nodeLabels := getNodeLabels(nodeInfo)
		if tenantLabel, exists := nodeLabels["nexusbox.io/tenant"]; exists && tenantLabel == sb.Spec.TenantRef.Name {
			return framework.NewResult(fmt.Sprintf("node %s matches tenant anti-affinity rules", nodeInfo.Name()))
		}
	}

	return nil
}

// matchNodeSelectorTerm checks if a node matches the given selector term.
func matchNodeSelectorTerm(term sandboxv1alpha1.NodeSelectorTerm, nodeLabels map[string]string) bool {
	for _, expr := range term.MatchExpressions {
		nodeValue, exists := nodeLabels[expr.Key]
		switch expr.Operator {
		case sandboxv1alpha1.NodeSelectorOpIn:
			if !exists {
				return false
			}
			found := false
			for _, v := range expr.Values {
				if v == nodeValue {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case sandboxv1alpha1.NodeSelectorOpNotIn:
			if exists {
				for _, v := range expr.Values {
					if v == nodeValue {
						return false
					}
				}
			}
		case sandboxv1alpha1.NodeSelectorOpExists:
			if !exists {
				return false
			}
		case sandboxv1alpha1.NodeSelectorOpDoesNotExist:
			if exists {
				return false
			}
		}
	}
	return true
}

// getNodeLabels extracts labels from a NodeInfo.
func getNodeLabels(nodeInfo *framework.NodeInfo) map[string]string {
	if nodeInfo.Node == nil {
		return map[string]string{}
	}
	return nodeInfo.Node.Labels
}

// TenantIsolation is a filter plugin that enforces tenant isolation policies.
// It ensures sandboxes from different isolation levels are properly separated.
type TenantIsolation struct{}

// NewTenantIsolation creates a new TenantIsolation plugin.
func NewTenantIsolation() *TenantIsolation {
	return &TenantIsolation{}
}

func (p *TenantIsolation) Name() string { return "TenantIsolation" }

// Filter checks if the node satisfies the tenant's isolation requirements.
func (p *TenantIsolation) Filter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeInfo *framework.NodeInfo) *framework.Result {
	sb := sandboxInfo.Sandbox

	// Check if the tenant requires dedicated nodes (using NodeSelector for isolation)
	if sb.Spec.SchedulingPolicy == sandboxv1alpha1.ScheduleTenantAffinity {
		// Check if the node is dedicated to this tenant
		nodeLabels := getNodeLabels(nodeInfo)
		if dedicatedTenant, exists := nodeLabels["nexusbox.io/dedicated-tenant"]; exists {
			if dedicatedTenant != sb.Spec.TenantRef.Name {
				return framework.NewResult(fmt.Sprintf("node %s is dedicated to tenant %s, not %s",
					nodeInfo.Name(), dedicatedTenant, sb.Spec.TenantRef.Name))
			}
		}
	}

	return nil
}

// BatchScheduling is a permit plugin that implements gang scheduling.
// It ensures that all sandboxes in a batch are scheduled together,
// or none are scheduled at all.
type BatchScheduling struct {
	batchTracker BatchTracker
}

// BatchTracker tracks batch scheduling state.
type BatchTracker interface {
	IsBatchReady(batchID string) bool
	GetBatchInfo(batchID string) (*BatchInfo, error)
}

// BatchInfo holds information about a batch.
type BatchInfo struct {
	ID             string
	TotalCount     int
	ScheduledCount int
	MinAvailable   int
}

// NewBatchScheduling creates a new BatchScheduling plugin.
func NewBatchScheduling(tracker BatchTracker) *BatchScheduling {
	return &BatchScheduling{
		batchTracker: tracker,
	}
}

func (p *BatchScheduling) Name() string { return "BatchScheduling" }

// Permit checks if the batch is ready for scheduling.
func (p *BatchScheduling) Permit(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) (*framework.Result, framework.Time) {
	sb := sandboxInfo.Sandbox

	// Check if this sandbox belongs to a batch
	if sb.Spec.SchedulingPolicy == "" {
		// Not a batch sandbox, allow immediately
		return nil, framework.Skip
	}

	batchID := string(sb.Spec.SchedulingPolicy)

	if p.batchTracker == nil {
		return nil, framework.Skip
	}

	// Check if the batch is ready
	if p.batchTracker.IsBatchReady(batchID) {
		return nil, framework.Skip
	}

	// Batch is not ready yet, wait
	klog.V(4).Infof("Batch %s is not ready yet, waiting for sandbox %s", batchID, sb.Name)
	return nil, framework.Wait
}

// PrioritySort is a pre-filter plugin that validates and sorts by priority.
type PrioritySort struct{}

// NewPrioritySort creates a new PrioritySort plugin.
func NewPrioritySort() *PrioritySort {
	return &PrioritySort{}
}

func (p *PrioritySort) Name() string { return "PrioritySort" }

// PreFilter validates the sandbox's priority.
func (p *PrioritySort) PreFilter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo) *framework.Result {
	sb := sandboxInfo.Sandbox

	if sb.Spec.Priority < 0 {
		return framework.NewResult(fmt.Sprintf("invalid priority %d: must be non-negative", sb.Spec.Priority))
	}

	return nil
}

func (p *PrioritySort) PreFilterExtensions() framework.PreFilterExtensions { return nil }

// RuntimeCompatibility is a filter plugin that checks if a node supports
// the required sandbox runtime type.
type RuntimeCompatibility struct{}

// NewRuntimeCompatibility creates a new RuntimeCompatibility plugin.
func NewRuntimeCompatibility() *RuntimeCompatibility {
	return &RuntimeCompatibility{}
}

func (p *RuntimeCompatibility) Name() string { return "RuntimeCompatibility" }

// Filter checks if the node supports the required runtime.
func (p *RuntimeCompatibility) Filter(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeInfo *framework.NodeInfo) *framework.Result {
	sb := sandboxInfo.Sandbox
	nodeLabels := getNodeLabels(nodeInfo)

	switch sb.Spec.Runtime {
	case sandboxv1alpha1.RuntimeKataContainers:
		// Check if the node supports Kata Containers
		if val, exists := nodeLabels["nexusbox.io/runtime-kata"]; !exists || val != "true" {
			return framework.NewResult(fmt.Sprintf("node %s does not support Kata Containers runtime", nodeInfo.Name()))
		}
	case sandboxv1alpha1.RuntimeGVisor:
		// Check if the node supports gVisor
		if val, exists := nodeLabels["nexusbox.io/runtime-gvisor"]; !exists || val != "true" {
			return framework.NewResult(fmt.Sprintf("node %s does not support gVisor runtime", nodeInfo.Name()))
		}
	case sandboxv1alpha1.RuntimeRunc:
		// runc is supported on all nodes by default
	default:
		return framework.NewResult(fmt.Sprintf("unknown runtime type: %s", sb.Spec.Runtime))
	}

	return nil
}

// NodeResourcesBalancedAllocation is a score plugin that favors nodes
// with balanced resource utilization.
type NodeResourcesBalancedAllocation struct{}

// NewNodeResourcesBalancedAllocation creates a new NodeResourcesBalancedAllocation plugin.
func NewNodeResourcesBalancedAllocation() *NodeResourcesBalancedAllocation {
	return &NodeResourcesBalancedAllocation{}
}

func (p *NodeResourcesBalancedAllocation) Name() string { return "NodeResourcesBalancedAllocation" }

// Score scores a node based on balanced resource allocation.
func (p *NodeResourcesBalancedAllocation) Score(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) (int64, *framework.Result) {
	// In production, calculate the variance of resource utilization
	// Lower variance = more balanced = higher score
	return 50, nil
}

func (p *NodeResourcesBalancedAllocation) ScoreExtensions() framework.ScoreExtensions { return nil }

// ImageLocality is a score plugin that favors nodes that already have
// the sandbox image cached.
type ImageLocality struct{}

// NewImageLocality creates a new ImageLocality plugin.
func NewImageLocality() *ImageLocality {
	return &ImageLocality{}
}

func (p *ImageLocality) Name() string { return "ImageLocality" }

// Score scores a node based on image locality.
func (p *ImageLocality) Score(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) (int64, *framework.Result) {
	sb := sandboxInfo.Sandbox

	// In production, check if the node has the image cached
	// Nodes with cached images get higher scores
	if sb.Spec.Image != "" {
		klog.V(6).Infof("Scoring node %s for image locality of %s", nodeName, sb.Spec.Image)
	}

	return 50, nil
}

func (p *ImageLocality) ScoreExtensions() framework.ScoreExtensions { return nil }

// DefaultBinder is a bind plugin that performs the default binding.
type DefaultBinder struct{}

// NewDefaultBinder creates a new DefaultBinder plugin.
func NewDefaultBinder() *DefaultBinder {
	return &DefaultBinder{}
}

func (p *DefaultBinder) Name() string { return "DefaultBinder" }

// Bind binds the sandbox to the node.
func (p *DefaultBinder) Bind(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) *framework.Result {
	klog.Infof("Binding sandbox %s to node %s", sandboxInfo.Sandbox.Name, nodeName)

	// In production, this would update the sandbox CRD's nodeName
	// and trigger the sandbox controller to create the runtime

	return nil
}

// DefaultPreBinder is a pre-bind plugin that performs pre-bind checks.
type DefaultPreBinder struct{}

// NewDefaultPreBinder creates a new DefaultPreBinder plugin.
func NewDefaultPreBinder() *DefaultPreBinder {
	return &DefaultPreBinder{}
}

func (p *DefaultPreBinder) Name() string { return "DefaultPreBinder" }

// PreBind performs pre-bind checks.
func (p *DefaultPreBinder) PreBind(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) *framework.Result {
	klog.V(4).Infof("Pre-binding sandbox %s to node %s", sandboxInfo.Sandbox.Name, nodeName)

	// In production, this would:
	// 1. Verify the node is still available
	// 2. Create any required volumes
	// 3. Set up network configuration

	return nil
}

// DefaultReserve is a reserve plugin that reserves resources on nodes.
type DefaultReserve struct{}

// NewDefaultReserve creates a new DefaultReserve plugin.
func NewDefaultReserve() *DefaultReserve {
	return &DefaultReserve{}
}

func (p *DefaultReserve) Name() string { return "DefaultReserve" }

// Reserve reserves resources on the node for the sandbox.
func (p *DefaultReserve) Reserve(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) *framework.Result {
	klog.V(4).Infof("Reserving resources on node %s for sandbox %s", nodeName, sandboxInfo.Sandbox.Name)

	// In production, this would update the node's resource tracking
	// to reflect the reserved resources

	return nil
}

// Unreserve releases reserved resources on the node.
func (p *DefaultReserve) Unreserve(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) {
	klog.V(4).Infof("Unreserving resources on node %s for sandbox %s", nodeName, sandboxInfo.Sandbox.Name)

	// In production, this would release the reserved resources
}

// DefaultPostBinder is a post-bind plugin that performs post-bind actions.
type DefaultPostBinder struct{}

// NewDefaultPostBinder creates a new DefaultPostBinder plugin.
func NewDefaultPostBinder() *DefaultPostBinder {
	return &DefaultPostBinder{}
}

func (p *DefaultPostBinder) Name() string { return "DefaultPostBinder" }

// PostBind performs post-bind actions.
func (p *DefaultPostBinder) PostBind(ctx context.Context, state *framework.CycleState, sandboxInfo *framework.SandboxInfo, nodeName string) {
	klog.V(4).Infof("Post-bind for sandbox %s on node %s", sandboxInfo.Sandbox.Name, nodeName)

	// In production, this would:
	// 1. Record metrics
	// 2. Send events
	// 3. Update cache
}

// ParseQuantityCPU is exported for plugin use.
var ParseQuantityCPU = parseQuantityCPU

// ParseQuantityMem is exported for plugin use.
var ParseQuantityMem = parseQuantityMem

func parseQuantityCPU(s string) (int64, error) {
	var value int64
	var unit string
	_, err := fmt.Sscanf(s, "%d%s", &value, &unit)
	if err != nil {
		return 0, err
	}
	switch unit {
	case "m":
		return value, nil
	case "":
		return value * 1000, nil
	default:
		return value, nil
	}
}

func parseQuantityMem(s string) (int64, error) {
	var value int64
	var unit string
	_, err := fmt.Sscanf(s, "%d%s", &value, &unit)
	if err != nil {
		return 0, err
	}
	switch unit {
	case "Ki":
		return value * 1024, nil
	case "Mi":
		return value * 1024 * 1024, nil
	case "Gi":
		return value * 1024 * 1024 * 1024, nil
	case "":
		return value, nil
	default:
		return value, nil
	}
}
