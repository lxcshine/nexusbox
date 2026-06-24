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

package framework

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// Framework manages the set of scheduling plugins and provides
// the interface for running the scheduling cycle.
//
// This design is inspired by the Kubernetes 1.23.17 scheduler framework,
// which uses a plugin-based architecture to allow extensibility.
// The framework defines extension points that plugins can implement:
//   - PreFilter: Pre-filter checks before the filter phase
//   - Filter: Filter out nodes that don't meet requirements
//   - PreScore: Pre-scoring preparation
//   - Score: Score nodes based on various criteria
//   - NormalizeScore: Normalize scores across plugins
//   - Reserve: Reserve/unreserve resources on nodes
//   - Permit: Permit or reject scheduling decisions
//   - PreBind: Pre-bind preparation
//   - Bind: Bind sandbox to node
//   - PostBind: Post-bind cleanup
type Framework interface {
	// SnapshotSharedLister returns the shared lister for node and sandbox info.
	SnapshotSharedLister() SharedLister

	// RunPreFilterPlugins runs all PreFilter plugins.
	RunPreFilterPlugins(ctx context.Context, sandboxInfo *SandboxInfo) *Result

	// RunFilterPlugins runs all Filter plugins for a given node.
	RunFilterPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeInfo *NodeInfo) *Result

	// RunPreScorePlugins runs all PreScore plugins.
	RunPreScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodes []*NodeInfo) *Result

	// RunScorePlugins runs all Score plugins.
	RunScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodes []*NodeInfo) (NodeScoreList, error)

	// RunNormalizeScorePlugins runs all NormalizeScore plugins.
	RunNormalizeScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, scoreList NodeScoreList) error

	// RunReservePluginsReserve runs the Reserve method of Reserve plugins.
	RunReservePluginsReserve(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result

	// RunReservePluginsUnreserve runs the Unreserve method of Reserve plugins.
	RunReservePluginsUnreserve(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string)

	// RunPermitPlugins runs all Permit plugins.
	RunPermitPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result

	// RunPreBindPlugins runs all PreBind plugins.
	RunPreBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result

	// RunBindPlugins runs all Bind plugins.
	RunBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result

	// RunPostBindPlugins runs all PostBind plugins.
	RunPostBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string)

	// ListPlugins returns all registered plugins.
	ListPlugins() []Plugin

	// HasFilterPlugin returns whether a filter plugin with the given name exists.
	HasFilterPlugin(name string) bool

	// HasScorePlugin returns whether a score plugin with the given name exists.
	HasScorePlugin(name string) bool
}

// Plugin is the parent type for all scheduling plugins.
type Plugin interface {
	// Name returns the plugin name.
	Name() string
}

// PreFilterPlugin is implemented by plugins that want to pre-filter sandboxes.
type PreFilterPlugin interface {
	Plugin
	// PreFilter checks if the sandbox can be scheduled at all.
	PreFilter(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo) *Result
	// PreFilterExtensions returns extensions for additional pre-filter processing.
	PreFilterExtensions() PreFilterExtensions
}

// PreFilterExtensions provides additional pre-filter methods.
type PreFilterExtensions interface {
	// AddSandbox is called when a sandbox is added while scheduling.
	AddSandbox(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeInfo *NodeInfo) *Result
	// RemoveSandbox is called when a sandbox is removed while scheduling.
	RemoveSandbox(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeInfo *NodeInfo) *Result
}

// FilterPlugin is implemented by plugins that filter nodes.
type FilterPlugin interface {
	Plugin
	// Filter checks if a node is suitable for the sandbox.
	Filter(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeInfo *NodeInfo) *Result
}

// PreScorePlugin is implemented by plugins that want to prepare for scoring.
type PreScorePlugin interface {
	Plugin
	// PreScore is called before scoring nodes.
	PreScore(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodes []*NodeInfo) *Result
}

// ScorePlugin is implemented by plugins that score nodes.
type ScorePlugin interface {
	Plugin
	// Score scores a node for the sandbox.
	Score(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string) (int64, *Result)
	// ScoreExtensions returns extensions for score normalization.
	ScoreExtensions() ScoreExtensions
}

// ScoreExtensions provides additional score methods.
type ScoreExtensions interface {
	// NormalizeScore normalizes scores across all nodes.
	NormalizeScore(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, scores NodeScoreList) *Result
}

// ReservePlugin is implemented by plugins that reserve resources.
type ReservePlugin interface {
	Plugin
	// Reserve reserves resources for the sandbox on the node.
	Reserve(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string) *Result
	// Unreserve releases reserved resources.
	Unreserve(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string)
}

// PermitPlugin is implemented by plugins that can permit or reject scheduling.
type PermitPlugin interface {
	Plugin
	// Permit checks if the sandbox can be scheduled to the node.
	Permit(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string) (*Result, Time)
}

// PreBindPlugin is implemented by plugins that prepare for binding.
type PreBindPlugin interface {
	Plugin
	// PreBind prepares the sandbox for binding to the node.
	PreBind(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string) *Result
}

// BindPlugin is implemented by plugins that bind sandboxes to nodes.
type BindPlugin interface {
	Plugin
	// Bind binds the sandbox to the node.
	Bind(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string) *Result
}

// PostBindPlugin is implemented by plugins that do post-bind actions.
type PostBindPlugin interface {
	Plugin
	// PostBind is called after the sandbox is bound to the node.
	PostBind(ctx context.Context, state *CycleState, sandboxInfo *SandboxInfo, nodeName string)
}

// Time represents a duration for permit plugin wait.
type Time int64

const (
	// Wait means the plugin wants to wait before making a decision.
	Wait Time = -1
	// Skip means the plugin doesn't want to make a decision.
	Skip Time = 0
)

// Result represents the result of a plugin execution.
type Result struct {
	reasons []string
	err     error
}

// NewResult creates a new Result.
func NewResult(reasons ...string) *Result {
	return &Result{reasons: reasons}
}

// NewResultWithError creates a new Result with an error.
func NewResultWithError(err error) *Result {
	return &Result{err: err}
}

// Success returns whether the result is successful.
func (r *Result) Success() bool {
	return r == nil || (len(r.reasons) == 0 && r.err == nil)
}

// Reasons returns the reasons for failure.
func (r *Result) Reasons() []string {
	if r == nil {
		return nil
	}
	return r.reasons
}

// Error returns the error.
func (r *Result) Error() error {
	if r == nil {
		return nil
	}
	return r.err
}

// AsResult wraps an error as a Result.
func AsResult(err error) *Result {
	return &Result{err: err}
}

// NodeScore represents a score for a node.
type NodeScore struct {
	Name  string
	Score int64
}

// NodeScoreList is a list of node scores.
type NodeScoreList []NodeScore

// SandboxInfo holds information about a sandbox being scheduled.
type SandboxInfo struct {
	// Sandbox is the sandbox CRD object.
	Sandbox *sandboxv1alpha1.Sandbox
	// Attempts is the number of scheduling attempts.
	Attempts int
	// InitialAttemptTimestamp is the time of the first scheduling attempt.
	InitialAttemptTimestamp int64
	// ResourceRequest is the requested resource.
	ResourceRequest *Resource
	// PreferredNode is the preferred node for this sandbox.
	PreferredNode string
	// RequiredNode is the required node for this sandbox.
	RequiredNode string
	// Priority is the scheduling priority.
	Priority int32
	// BatchID is the batch ID for batch scheduling.
	BatchID string
	// TenantName is the tenant that owns this sandbox.
	TenantName string
}

// NodeInfo holds information about a node for scheduling.
type NodeInfo struct {
	mu sync.RWMutex

	// Node is the node object.
	Node *sandboxv1alpha1.SandboxNode

	// Sandboxes are the sandboxes on this node.
	Sandboxes []*SandboxInfo

	// RequestedResource is the total requested resources on this node.
	RequestedResource *Resource

	// AllocatableResource is the total allocatable resources on this node.
	AllocatableResource *Resource

	// AvailableResource is the available resources on this node.
	AvailableResource *Resource
}

// NewNodeInfo creates a new NodeInfo.
func NewNodeInfo(node *sandboxv1alpha1.SandboxNode) *NodeInfo {
	return &NodeInfo{
		Node:                node,
		Sandboxes:           make([]*SandboxInfo, 0),
		RequestedResource:   &Resource{},
		AllocatableResource: &Resource{},
		AvailableResource:   &Resource{},
	}
}

// Name returns the node name.
func (n *NodeInfo) Name() string {
	if n.Node == nil {
		return ""
	}
	return n.Node.Name
}

// AddSandbox adds a sandbox to the node.
func (n *NodeInfo) AddSandbox(sandboxInfo *SandboxInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.Sandboxes = append(n.Sandboxes, sandboxInfo)

	// Update requested resources
	if sandboxInfo.Sandbox != nil {
		n.RequestedResource.Add(&sandboxInfo.Sandbox.Spec.Resources)
		n.AvailableResource.Sub(&sandboxInfo.Sandbox.Spec.Resources)
	}
}

// RemoveSandbox removes a sandbox from the node.
func (n *NodeInfo) RemoveSandbox(sandboxInfo *SandboxInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for i, sb := range n.Sandboxes {
		if sb == sandboxInfo {
			n.Sandboxes = append(n.Sandboxes[:i], n.Sandboxes[i+1:]...)
			break
		}
	}

	// Update requested resources
	if sandboxInfo.Sandbox != nil {
		n.RequestedResource.Sub(&sandboxInfo.Sandbox.Spec.Resources)
		n.AvailableResource.Add(&sandboxInfo.Sandbox.Spec.Resources)
	}
}

// SandboxCount returns the number of sandboxes on the node.
func (n *NodeInfo) SandboxCount() int {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return len(n.Sandboxes)
}

// Resource represents compute resources.
type Resource struct {
	// MilliCPU is the CPU in millicores.
	MilliCPU int64
	// Memory is the memory in bytes.
	Memory int64
	// GPU is the GPU count.
	GPU int64
	// EphemeralStorage is the ephemeral storage in bytes.
	EphemeralStorage int64
}

// Add adds resources from the given requirements.
func (r *Resource) Add(req *sandboxv1alpha1.ResourceRequirements) {
	if req == nil {
		return
	}
	// Parse and add resources
	if req.CPU != "" {
		if cpu, err := ParseQuantityCPU(req.CPU); err == nil {
			r.MilliCPU += cpu
		}
	}
	if req.Memory != "" {
		if mem, err := ParseQuantityMem(req.Memory); err == nil {
			r.Memory += mem
		}
	}
	if req.GPU != "" {
		if gpu, err := ParseQuantityInt(req.GPU); err == nil {
			r.GPU += gpu
		}
	}
}

// Sub subtracts resources from the given requirements.
func (r *Resource) Sub(req *sandboxv1alpha1.ResourceRequirements) {
	if req == nil {
		return
	}
	if req.CPU != "" {
		if cpu, err := ParseQuantityCPU(req.CPU); err == nil {
			r.MilliCPU -= cpu
			if r.MilliCPU < 0 {
				r.MilliCPU = 0
			}
		}
	}
	if req.Memory != "" {
		if mem, err := ParseQuantityMem(req.Memory); err == nil {
			r.Memory -= mem
			if r.Memory < 0 {
				r.Memory = 0
			}
		}
	}
	if req.GPU != "" {
		if gpu, err := ParseQuantityInt(req.GPU); err == nil {
			r.GPU -= gpu
			if r.GPU < 0 {
				r.GPU = 0
			}
		}
	}
}

// Fits checks if the resource can accommodate the given requirements.
func (r *Resource) Fits(req *sandboxv1alpha1.ResourceRequirements) bool {
	if req == nil {
		return true
	}
	if req.CPU != "" {
		if cpu, err := ParseQuantityCPU(req.CPU); err == nil {
			if r.MilliCPU < cpu {
				return false
			}
		}
	}
	if req.Memory != "" {
		if mem, err := ParseQuantityMem(req.Memory); err == nil {
			if r.Memory < mem {
				return false
			}
		}
	}
	if req.GPU != "" {
		if gpu, err := ParseQuantityInt(req.GPU); err == nil {
			if r.GPU < gpu {
				return false
			}
		}
	}
	return true
}

// SharedLister provides shared access to node and sandbox information.
type SharedLister interface {
	NodeInfos() NodeInfoLister
	SandboxInfos() SandboxInfoLister
}

// NodeInfoLister lists NodeInfo objects.
type NodeInfoLister interface {
	List() ([]*NodeInfo, error)
	HaveSandboxsWithAffinityList() ([]*NodeInfo, error)
	HaveSandboxsWithRequiredAntiAffinityList() ([]*NodeInfo, error)
	Get(nodeName string) (*NodeInfo, error)
}

// SandboxInfoLister lists SandboxInfo objects.
type SandboxInfoLister interface {
	List(selector interface{}) ([]*SandboxInfo, error)
	HaveSandboxsWithAffinityList() ([]*SandboxInfo, error)
}

// CycleState holds state for a scheduling cycle.
type CycleState struct {
	mu    sync.RWMutex
	state map[StateKey]StateData
}

// StateKey is the key for CycleState data.
type StateKey string

// StateData is data stored in CycleState.
type StateData interface {
	Clone() StateData
}

// NewCycleState creates a new CycleState.
func NewCycleState() *CycleState {
	return &CycleState{
		state: make(map[StateKey]StateData),
	}
}

// Read reads data from the cycle state.
func (c *CycleState) Read(key StateKey) (StateData, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, exists := c.state[key]
	if !exists {
		return nil, fmt.Errorf("key %s not found in cycle state", key)
	}
	return data, nil
}

// Write writes data to the cycle state.
func (c *CycleState) Write(key StateKey, data StateData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state[key] = data
}

// Delete deletes data from the cycle state.
func (c *CycleState) Delete(key StateKey) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.state, key)
}

// Clone clones the cycle state.
func (c *CycleState) Clone() *CycleState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	clone := &CycleState{
		state: make(map[StateKey]StateData, len(c.state)),
	}

	for key, data := range c.state {
		clone.state[key] = data.Clone()
	}

	return clone
}

// SnapshotSharedListerImpl implements SharedLister.
type SnapshotSharedListerImpl struct {
	nodeInfos    []*NodeInfo
	sandboxInfos []*SandboxInfo
}

// NewSnapshotSharedLister creates a new SnapshotSharedListerImpl.
func NewSnapshotSharedLister(nodeInfos []*NodeInfo, sandboxInfos []*SandboxInfo) SharedLister {
	return &SnapshotSharedListerImpl{
		nodeInfos:    nodeInfos,
		sandboxInfos: sandboxInfos,
	}
}

func (s *SnapshotSharedListerImpl) NodeInfos() NodeInfoLister {
	return &nodeInfoListerImpl{nodeInfos: s.nodeInfos}
}

func (s *SnapshotSharedListerImpl) SandboxInfos() SandboxInfoLister {
	return &sandboxInfoListerImpl{sandboxInfos: s.sandboxInfos}
}

type nodeInfoListerImpl struct {
	nodeInfos []*NodeInfo
}

func (l *nodeInfoListerImpl) List() ([]*NodeInfo, error) {
	return l.nodeInfos, nil
}

func (l *nodeInfoListerImpl) HaveSandboxsWithAffinityList() ([]*NodeInfo, error) {
	result := make([]*NodeInfo, 0)
	for _, ni := range l.nodeInfos {
		if len(ni.Sandboxes) > 0 {
			result = append(result, ni)
		}
	}
	return result, nil
}

func (l *nodeInfoListerImpl) HaveSandboxsWithRequiredAntiAffinityList() ([]*NodeInfo, error) {
	return l.nodeInfos, nil
}

func (l *nodeInfoListerImpl) Get(nodeName string) (*NodeInfo, error) {
	for _, ni := range l.nodeInfos {
		if ni.Name() == nodeName {
			return ni, nil
		}
	}
	return nil, fmt.Errorf("node %s not found", nodeName)
}

type sandboxInfoListerImpl struct {
	sandboxInfos []*SandboxInfo
}

func (l *sandboxInfoListerImpl) List(selector interface{}) ([]*SandboxInfo, error) {
	return l.sandboxInfos, nil
}

func (l *sandboxInfoListerImpl) HaveSandboxsWithAffinityList() ([]*SandboxInfo, error) {
	return l.sandboxInfos, nil
}

// parseQuantity parses a quantity string and returns millicores.
func ParseQuantityCPU(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty quantity")
	}

	// Check for millicore suffix
	if strings.HasSuffix(s, "m") {
		val, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid millicore quantity: %s", s)
		}
		return val, nil
	}

	// Try parsing as float (e.g., "1", "0.5", "2.5")
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid CPU quantity: %s", s)
	}
	return int64(val * 1000), nil
}

// parseQuantityMem parses a quantity string and returns bytes.
func ParseQuantityMem(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty quantity")
	}

	suffixes := []struct {
		suffix     string
		multiplier int64
	}{
		{"Gi", 1024 * 1024 * 1024},
		{"Mi", 1024 * 1024},
		{"Ki", 1024},
	}

	for _, su := range suffixes {
		if strings.HasSuffix(s, su.suffix) {
			val, err := strconv.ParseInt(strings.TrimSuffix(s, su.suffix), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid memory quantity: %s", s)
			}
			return val * su.multiplier, nil
		}
	}

	// Plain number (bytes)
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory quantity: %s", s)
	}
	return val, nil
}

// parseQuantityInt parses a quantity string and returns an integer.
func ParseQuantityInt(s string) (int64, error) {
	var value int64
	_, err := fmt.Sscanf(s, "%d", &value)
	return value, err
}

// frameworkImpl implements the Framework interface.
type frameworkImpl struct {
	mu sync.RWMutex

	// registry holds all registered plugins.
	registry map[string]Plugin

	// preFilterPlugins are the PreFilter plugins.
	preFilterPlugins []PreFilterPlugin
	// filterPlugins are the Filter plugins.
	filterPlugins []FilterPlugin
	// preScorePlugins are the PreScore plugins.
	preScorePlugins []PreScorePlugin
	// scorePlugins are the Score plugins.
	scorePlugins []ScorePlugin
	// reservePlugins are the Reserve plugins.
	reservePlugins []ReservePlugin
	// permitPlugins are the Permit plugins.
	permitPlugins []PermitPlugin
	// preBindPlugins are the PreBind plugins.
	preBindPlugins []PreBindPlugin
	// bindPlugins are the Bind plugins.
	bindPlugins []BindPlugin
	// postBindPlugins are the PostBind plugins.
	postBindPlugins []PostBindPlugin

	// snapshotSharedLister provides node and sandbox information.
	snapshotSharedLister SharedLister

	// snapshotMu protects the snapshot.
	snapshotMu sync.RWMutex
}

// FrameworkConfig holds configuration for the scheduling framework.
type FrameworkConfig struct {
	// Plugins is the list of plugin configurations.
	Plugins *Plugins
	// PluginConfig is the configuration for each plugin.
	PluginConfig []PluginConfig
	// SnapshotSharedLister provides node and sandbox information.
	SnapshotSharedLister SharedLister
}

// Plugins holds the configuration for scheduling plugins.
type Plugins struct {
	PreFilter []PluginSpec
	Filter    []PluginSpec
	PreScore  []PluginSpec
	Score     []PluginSpec
	Reserve   []PluginSpec
	Permit    []PluginSpec
	PreBind   []PluginSpec
	Bind      []PluginSpec
	PostBind  []PluginSpec
}

// PluginSpec specifies a plugin and its weight.
type PluginSpec struct {
	Name   string
	Weight int64
}

// PluginConfig holds configuration for a specific plugin.
type PluginConfig struct {
	Name   string
	Config interface{}
}

// NewFramework creates a new scheduling framework.
func NewFramework(config *FrameworkConfig, plugins ...Plugin) Framework {
	fwk := &frameworkImpl{
		registry:             make(map[string]Plugin),
		snapshotSharedLister: config.SnapshotSharedLister,
	}

	// Register all plugins
	for _, plugin := range plugins {
		fwk.registry[plugin.Name()] = plugin
	}

	// Categorize plugins by extension point
	for _, plugin := range plugins {
		if p, ok := plugin.(PreFilterPlugin); ok {
			fwk.preFilterPlugins = append(fwk.preFilterPlugins, p)
		}
		if p, ok := plugin.(FilterPlugin); ok {
			fwk.filterPlugins = append(fwk.filterPlugins, p)
		}
		if p, ok := plugin.(PreScorePlugin); ok {
			fwk.preScorePlugins = append(fwk.preScorePlugins, p)
		}
		if p, ok := plugin.(ScorePlugin); ok {
			fwk.scorePlugins = append(fwk.scorePlugins, p)
		}
		if p, ok := plugin.(ReservePlugin); ok {
			fwk.reservePlugins = append(fwk.reservePlugins, p)
		}
		if p, ok := plugin.(PermitPlugin); ok {
			fwk.permitPlugins = append(fwk.permitPlugins, p)
		}
		if p, ok := plugin.(PreBindPlugin); ok {
			fwk.preBindPlugins = append(fwk.preBindPlugins, p)
		}
		if p, ok := plugin.(BindPlugin); ok {
			fwk.bindPlugins = append(fwk.bindPlugins, p)
		}
		if p, ok := plugin.(PostBindPlugin); ok {
			fwk.postBindPlugins = append(fwk.postBindPlugins, p)
		}
	}

	klog.Infof("Initialized scheduling framework with %d plugins", len(plugins))
	return fwk
}

func (f *frameworkImpl) SnapshotSharedLister() SharedLister {
	f.snapshotMu.RLock()
	defer f.snapshotMu.RUnlock()
	return f.snapshotSharedLister
}

func (f *frameworkImpl) RunPreFilterPlugins(ctx context.Context, sandboxInfo *SandboxInfo) *Result {
	state := NewCycleState()

	for _, plugin := range f.preFilterPlugins {
		result := plugin.PreFilter(ctx, state, sandboxInfo)
		if !result.Success() {
			klog.V(4).Infof("PreFilter plugin %s rejected sandbox %s: %v",
				plugin.Name(), sandboxInfo.Sandbox.Name, result.Reasons())
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunFilterPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeInfo *NodeInfo) *Result {
	state := NewCycleState()

	for _, plugin := range f.filterPlugins {
		result := plugin.Filter(ctx, state, sandboxInfo, nodeInfo)
		if !result.Success() {
			klog.V(6).Infof("Filter plugin %s rejected node %s for sandbox %s: %v",
				plugin.Name(), nodeInfo.Name(), sandboxInfo.Sandbox.Name, result.Reasons())
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunPreScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodes []*NodeInfo) *Result {
	state := NewCycleState()

	for _, plugin := range f.preScorePlugins {
		result := plugin.PreScore(ctx, state, sandboxInfo, nodes)
		if !result.Success() {
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodes []*NodeInfo) (NodeScoreList, error) {
	state := NewCycleState()
	scoreList := make(NodeScoreList, len(nodes))

	for i, node := range nodes {
		scoreList[i] = NodeScore{Name: node.Name(), Score: 0}
	}

	for _, plugin := range f.scorePlugins {
		var weight int64 = 1
		for i, node := range nodes {
			score, result := plugin.Score(ctx, state, sandboxInfo, node.Name())
			if !result.Success() {
				return nil, fmt.Errorf("score plugin %s failed for node %s: %v",
					plugin.Name(), node.Name(), result.Reasons())
			}
			scoreList[i].Score += score * weight
		}
	}

	return scoreList, nil
}

func (f *frameworkImpl) RunNormalizeScorePlugins(ctx context.Context, sandboxInfo *SandboxInfo, scoreList NodeScoreList) error {
	state := NewCycleState()

	for _, plugin := range f.scorePlugins {
		extensions := plugin.ScoreExtensions()
		if extensions != nil {
			result := extensions.NormalizeScore(ctx, state, sandboxInfo, scoreList)
			if !result.Success() {
				return fmt.Errorf("normalize score plugin %s failed: %v",
					plugin.Name(), result.Reasons())
			}
		}
	}

	return nil
}

func (f *frameworkImpl) RunReservePluginsReserve(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result {
	state := NewCycleState()

	for _, plugin := range f.reservePlugins {
		result := plugin.Reserve(ctx, state, sandboxInfo, nodeName)
		if !result.Success() {
			// Unreserve already reserved plugins
			for i := len(f.reservePlugins) - 1; i >= 0; i-- {
				if f.reservePlugins[i] == plugin {
					break
				}
				f.reservePlugins[i].Unreserve(ctx, state, sandboxInfo, nodeName)
			}
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunReservePluginsUnreserve(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) {
	state := NewCycleState()

	for i := len(f.reservePlugins) - 1; i >= 0; i-- {
		f.reservePlugins[i].Unreserve(ctx, state, sandboxInfo, nodeName)
	}
}

func (f *frameworkImpl) RunPermitPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result {
	state := NewCycleState()

	for _, plugin := range f.permitPlugins {
		result, _ := plugin.Permit(ctx, state, sandboxInfo, nodeName)
		if !result.Success() {
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunPreBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result {
	state := NewCycleState()

	for _, plugin := range f.preBindPlugins {
		result := plugin.PreBind(ctx, state, sandboxInfo, nodeName)
		if !result.Success() {
			return result
		}
	}

	return nil
}

func (f *frameworkImpl) RunBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) *Result {
	state := NewCycleState()

	if len(f.bindPlugins) == 0 {
		return nil
	}

	for _, plugin := range f.bindPlugins {
		result := plugin.Bind(ctx, state, sandboxInfo, nodeName)
		if !result.Success() {
			return result
		}
		// If a bind plugin succeeds, skip the rest
		return nil
	}

	return nil
}

func (f *frameworkImpl) RunPostBindPlugins(ctx context.Context, sandboxInfo *SandboxInfo, nodeName string) {
	state := NewCycleState()

	for _, plugin := range f.postBindPlugins {
		plugin.PostBind(ctx, state, sandboxInfo, nodeName)
	}
}

func (f *frameworkImpl) ListPlugins() []Plugin {
	f.mu.RLock()
	defer f.mu.RUnlock()

	plugins := make([]Plugin, 0, len(f.registry))
	for _, plugin := range f.registry {
		plugins = append(plugins, plugin)
	}
	return plugins
}

func (f *frameworkImpl) HasFilterPlugin(name string) bool {
	for _, plugin := range f.filterPlugins {
		if plugin.Name() == name {
			return true
		}
	}
	return false
}

func (f *frameworkImpl) HasScorePlugin(name string) bool {
	for _, plugin := range f.scorePlugins {
		if plugin.Name() == name {
			return true
		}
	}
	return false
}

// UpdateSnapshot updates the shared lister snapshot.
func (f *frameworkImpl) UpdateSnapshot(listers SharedLister) {
	f.snapshotMu.Lock()
	defer f.snapshotMu.Unlock()
	f.snapshotSharedLister = listers
}

// NewResource creates a new Resource from a ResourceRequirements.
func NewResource(req *sandboxv1alpha1.ResourceRequirements) *Resource {
	r := &Resource{}
	if req != nil {
		if req.CPU != "" {
			if cpu, err := ParseQuantityCPU(req.CPU); err == nil {
				r.MilliCPU = cpu
			}
		}
		if req.Memory != "" {
			if mem, err := ParseQuantityMem(req.Memory); err == nil {
				r.Memory = mem
			}
		}
		if req.GPU != "" {
			if gpu, err := ParseQuantityInt(req.GPU); err == nil {
				r.GPU = gpu
			}
		}
		if req.EphemeralStorage != "" {
			if storage, err := ParseQuantityMem(req.EphemeralStorage); err == nil {
				r.EphemeralStorage = storage
			}
		}
	}
	return r
}

// AddResource adds the given resource to this resource.
func (r *Resource) AddResource(other *Resource) {
	if other == nil {
		return
	}
	r.MilliCPU += other.MilliCPU
	r.Memory += other.Memory
	r.EphemeralStorage += other.EphemeralStorage
	r.GPU += other.GPU
}

// SubResource subtracts the given resource from this resource.
func (r *Resource) SubResource(other *Resource) {
	if other == nil {
		return
	}
	r.MilliCPU -= other.MilliCPU
	r.Memory -= other.Memory
	r.EphemeralStorage -= other.EphemeralStorage
	r.GPU -= other.GPU
}

// FitsResource checks if the resource can accommodate the requested resource.
func (r *Resource) FitsResource(requested *Resource) bool {
	if requested == nil {
		return true
	}
	return r.MilliCPU >= requested.MilliCPU &&
		r.Memory >= requested.Memory &&
		r.EphemeralStorage >= requested.EphemeralStorage &&
		r.GPU >= requested.GPU
}

// CloneResource creates a copy of the resource.
func (r *Resource) CloneResource() *Resource {
	return &Resource{
		MilliCPU:         r.MilliCPU,
		Memory:           r.Memory,
		EphemeralStorage: r.EphemeralStorage,
		GPU:              r.GPU,
	}
}

// NewSandboxInfo creates a new SandboxInfo from a Sandbox object.
func NewSandboxInfo(sb *sandboxv1alpha1.Sandbox) *SandboxInfo {
	info := &SandboxInfo{
		Sandbox:         sb,
		ResourceRequest: NewResource(&sb.Spec.Resources),
		TenantName:      sb.Spec.TenantRef.Name,
		Priority:        int32(sb.Spec.Priority),
	}

	if sb.Spec.SchedulingPolicy != "" {
		info.PreferredNode = string(sb.Spec.SchedulingPolicy)
	}

	return info
}
