package cache

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// Cache stores sandbox and tenant objects for fast access.
// It provides a local cache of the cluster state that can be
// used by the scheduler and controllers without querying the
// API server on every operation.
//
// The cache design is inspired by the Kubernetes 1.23.17 scheduler cache,
// which maintains a snapshot of the cluster state for scheduling decisions.
type Cache struct {
	mu sync.RWMutex

	// sandboxes maps sandbox key (namespace/name) to its cached state.
	sandboxes map[string]*SandboxState

	// nodes maps node name to its cached state.
	nodes map[string]*NodeState

	// tenants maps tenant name to its cached state.
	tenants map[string]*TenantState

	// assumedSandboxes tracks sandboxes that are assumed to be bound
	// to a node but haven't been confirmed yet. This is used during
	// the scheduling cycle to prevent race conditions.
	assumedSandboxes map[string]*AssumedSandboxState

	// podStates maps sandbox key to its sandbox state for quick lookup.
	// This is a duplicate reference for compatibility with the scheduler.
	podStates map[string]*SandboxState

	// timestamp is the last time the cache was updated.
	timestamp time.Time
}

// SandboxState holds cached state for a sandbox.
type SandboxState struct {
	// Sandbox is the sandbox object.
	Sandbox *v1alpha1.Sandbox

	// NodeName is the node the sandbox is bound to.
	NodeName string

	// IsAssumed indicates whether the sandbox binding is assumed (not yet confirmed).
	IsAssumed bool

	// LastUpdated is the last time this state was updated.
	LastUpdated time.Time
}

// NodeState holds cached state for a node.
type NodeState struct {
	// NodeName is the name of the node.
	NodeName string

	// AvailableResource is the available resource on the node.
	AvailableResource *v1alpha1.ResourceRequirements

	// TotalResource is the total resource capacity of the node.
	TotalResource *v1alpha1.ResourceRequirements

	// Sandboxes are the sandboxes currently on this node.
	Sandboxes map[string]*v1alpha1.Sandbox

	// SandboxCount is the number of sandboxes on this node.
	SandboxCount int

	// Conditions are the node conditions.
	Conditions []v1alpha1.SandboxCondition

	// Labels are the node labels.
	Labels map[string]string

	// SupportedRuntimes are the runtimes supported by this node.
	SupportedRuntimes []string

	// LastUpdated is the last time this state was updated.
	LastUpdated time.Time
}

// TenantState holds cached state for a tenant.
type TenantState struct {
	// Tenant is the tenant object.
	Tenant *v1alpha1.Tenant

	// ActiveSandboxCount is the number of active sandboxes for this tenant.
	ActiveSandboxCount int

	// ResourceUsage is the current resource usage for this tenant.
	ResourceUsage *v1alpha1.ResourceRequirements

	// LastUpdated is the last time this state was updated.
	LastUpdated time.Time
}

// AssumedSandboxState holds state for an assumed sandbox binding.
type AssumedSandboxState struct {
	// Sandbox is the sandbox object.
	Sandbox *v1alpha1.Sandbox

	// NodeName is the assumed node for this sandbox.
	NodeName string

	// AssumedAt is when the assumption was made.
	AssumedAt time.Time
}

// NewCache creates a new Cache.
func NewCache() *Cache {
	return &Cache{
		sandboxes:        make(map[string]*SandboxState),
		nodes:            make(map[string]*NodeState),
		tenants:          make(map[string]*TenantState),
		assumedSandboxes: make(map[string]*AssumedSandboxState),
		podStates:        make(map[string]*SandboxState),
		timestamp:        time.Now(),
	}
}

// AddSandbox adds a sandbox to the cache.
func (c *Cache) AddSandbox(sb *v1alpha1.Sandbox) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)
	if _, exists := c.sandboxes[key]; exists {
		return fmt.Errorf("sandbox %s already exists in cache", key)
	}

	state := &SandboxState{
		Sandbox:     sb,
		NodeName:    sb.Status.NodeName,
		IsAssumed:   false,
		LastUpdated: time.Now(),
	}

	c.sandboxes[key] = state
	c.podStates[key] = state

	// Update node state if sandbox is bound
	if sb.Status.NodeName != "" {
		if nodeState, exists := c.nodes[sb.Status.NodeName]; exists {
			nodeState.Sandboxes[key] = sb
			nodeState.SandboxCount++
			nodeState.LastUpdated = time.Now()
		}
	}

	// Update tenant state
	if sb.Spec.TenantRef.Name != "" {
		if tenantState, exists := c.tenants[sb.Spec.TenantRef.Name]; exists {
			tenantState.ActiveSandboxCount++
			tenantState.LastUpdated = time.Now()
		}
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Added sandbox %s to cache", key)
	return nil
}

// UpdateSandbox updates a sandbox in the cache.
func (c *Cache) UpdateSandbox(sb *v1alpha1.Sandbox) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)
	oldState, exists := c.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found in cache", key)
	}

	// Remove from old node if node changed
	if oldState.NodeName != "" && oldState.NodeName != sb.Status.NodeName {
		if nodeState, exists := c.nodes[oldState.NodeName]; exists {
			delete(nodeState.Sandboxes, key)
			nodeState.SandboxCount--
			nodeState.LastUpdated = time.Now()
		}
	}

	// Update state
	oldState.Sandbox = sb
	oldState.NodeName = sb.Status.NodeName
	oldState.IsAssumed = false
	oldState.LastUpdated = time.Now()

	// Add to new node
	if sb.Status.NodeName != "" {
		if nodeState, exists := c.nodes[sb.Status.NodeName]; exists {
			nodeState.Sandboxes[key] = sb
			nodeState.SandboxCount++
			nodeState.LastUpdated = time.Now()
		}
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Updated sandbox %s in cache", key)
	return nil
}

// DeleteSandbox removes a sandbox from the cache.
func (c *Cache) DeleteSandbox(sb *v1alpha1.Sandbox) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)
	state, exists := c.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found in cache", key)
	}

	// Remove from node
	if state.NodeName != "" {
		if nodeState, exists := c.nodes[state.NodeName]; exists {
			delete(nodeState.Sandboxes, key)
			nodeState.SandboxCount--
			nodeState.LastUpdated = time.Now()
		}
	}

	// Update tenant state
	if sb.Spec.TenantRef.Name != "" {
		if tenantState, exists := c.tenants[sb.Spec.TenantRef.Name]; exists {
			tenantState.ActiveSandboxCount--
			tenantState.LastUpdated = time.Now()
		}
	}

	delete(c.sandboxes, key)
	delete(c.podStates, key)
	delete(c.assumedSandboxes, key)

	c.timestamp = time.Now()
	klog.V(4).Infof("Deleted sandbox %s from cache", key)
	return nil
}

// AssumeSandbox assumes a sandbox is bound to a node.
// This is used during the scheduling cycle to prevent race conditions.
// The assumption must be confirmed with ConfirmAssumeSandbox or
// reverted with ForgetSandbox.
func (c *Cache) AssumeSandbox(sb *v1alpha1.Sandbox, nodeName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)

	// Check if already assumed
	if _, exists := c.assumedSandboxes[key]; exists {
		return fmt.Errorf("sandbox %s is already assumed", key)
	}

	// Create assumed state
	assumedState := &AssumedSandboxState{
		Sandbox:   sb,
		NodeName:  nodeName,
		AssumedAt: time.Now(),
	}
	c.assumedSandboxes[key] = assumedState

	// Update sandbox state
	if state, exists := c.sandboxes[key]; exists {
		state.NodeName = nodeName
		state.IsAssumed = true
		state.LastUpdated = time.Now()
	}

	// Update node resources (optimistically)
	if nodeState, exists := c.nodes[nodeName]; exists {
		nodeState.Sandboxes[key] = sb
		nodeState.SandboxCount++
		nodeState.LastUpdated = time.Now()
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Assumed sandbox %s on node %s", key, nodeName)
	return nil
}

// ConfirmAssumeSandbox confirms an assumed sandbox binding.
func (c *Cache) ConfirmAssumeSandbox(sb *v1alpha1.Sandbox) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)

	// Remove from assumed
	delete(c.assumedSandboxes, key)

	// Update sandbox state
	if state, exists := c.sandboxes[key]; exists {
		state.IsAssumed = false
		state.LastUpdated = time.Now()
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Confirmed sandbox %s assumption", key)
	return nil
}

// ForgetSandbox reverts an assumed sandbox binding.
func (c *Cache) ForgetSandbox(sb *v1alpha1.Sandbox) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := sandboxKey(sb)

	assumedState, exists := c.assumedSandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s is not assumed", key)
	}

	// Remove from assumed
	delete(c.assumedSandboxes, key)

	// Revert node resources
	if nodeState, exists := c.nodes[assumedState.NodeName]; exists {
		delete(nodeState.Sandboxes, key)
		nodeState.SandboxCount--
		nodeState.LastUpdated = time.Now()
	}

	// Revert sandbox state
	if state, exists := c.sandboxes[key]; exists {
		state.NodeName = ""
		state.IsAssumed = false
		state.LastUpdated = time.Now()
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Forgot sandbox %s assumption", key)
	return nil
}

// AddNode adds a node to the cache.
func (c *Cache) AddNode(nodeName string, totalResource *v1alpha1.ResourceRequirements, labels map[string]string, runtimes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodes[nodeName] = &NodeState{
		NodeName:          nodeName,
		AvailableResource: totalResource,
		TotalResource:     totalResource,
		Sandboxes:         make(map[string]*v1alpha1.Sandbox),
		SandboxCount:      0,
		Labels:            labels,
		SupportedRuntimes: runtimes,
		LastUpdated:       time.Now(),
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Added node %s to cache", nodeName)
}

// UpdateNode updates a node in the cache.
func (c *Cache) UpdateNode(nodeName string, availableResource *v1alpha1.ResourceRequirements, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if nodeState, exists := c.nodes[nodeName]; exists {
		nodeState.AvailableResource = availableResource
		nodeState.Labels = labels
		nodeState.LastUpdated = time.Now()
	}

	c.timestamp = time.Now()
}

// RemoveNode removes a node from the cache.
func (c *Cache) RemoveNode(nodeName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.nodes, nodeName)
	c.timestamp = time.Now()
	klog.V(4).Infof("Removed node %s from cache", nodeName)
}

// AddTenant adds a tenant to the cache.
func (c *Cache) AddTenant(t *v1alpha1.Tenant) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tenants[t.Name] = &TenantState{
		Tenant:            t,
		ActiveSandboxCount: 0,
		ResourceUsage:     &v1alpha1.ResourceRequirements{},
		LastUpdated:       time.Now(),
	}

	c.timestamp = time.Now()
	klog.V(4).Infof("Added tenant %s to cache", t.Name)
}

// UpdateTenant updates a tenant in the cache.
func (c *Cache) UpdateTenant(t *v1alpha1.Tenant) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if tenantState, exists := c.tenants[t.Name]; exists {
		tenantState.Tenant = t
		tenantState.LastUpdated = time.Now()
	}

	c.timestamp = time.Now()
}

// RemoveTenant removes a tenant from the cache.
func (c *Cache) RemoveTenant(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.tenants, name)
	c.timestamp = time.Now()
}

// Snapshot creates a snapshot of the current cache state.
// The snapshot is used by the scheduler for making scheduling decisions.
func (c *Cache) Snapshot() *CacheSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := &CacheSnapshot{
		Nodes:    make(map[string]*NodeState),
		Sandboxes: make(map[string]*SandboxState),
		Tenants:  make(map[string]*TenantState),
	}

	// Deep copy nodes
	for name, nodeState := range c.nodes {
		nodeCopy := *nodeState
		nodeCopy.Sandboxes = make(map[string]*v1alpha1.Sandbox)
		for k, v := range nodeState.Sandboxes {
			sbCopy := v.DeepCopy()
			nodeCopy.Sandboxes[k] = sbCopy
		}
		snapshot.Nodes[name] = &nodeCopy
	}

	// Deep copy sandboxes
	for key, sbState := range c.sandboxes {
		sbCopy := *sbState
		sbCopy.Sandbox = sbState.Sandbox.DeepCopy()
		snapshot.Sandboxes[key] = &sbCopy
	}

	// Deep copy tenants
	for name, tenantState := range c.tenants {
		tenantCopy := *tenantState
		tenantCopy.Tenant = tenantState.Tenant.DeepCopy()
		snapshot.Tenants[name] = &tenantCopy
	}

	snapshot.Timestamp = c.timestamp
	return snapshot
}

// GetSandbox retrieves a sandbox from the cache.
func (c *Cache) GetSandbox(namespace, name string) (*v1alpha1.Sandbox, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := namespace + "/" + name
	state, exists := c.sandboxes[key]
	if !exists {
		return nil, false
	}
	return state.Sandbox, true
}

// GetNode retrieves a node from the cache.
func (c *Cache) GetNode(name string) (*NodeState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, exists := c.nodes[name]
	if !exists {
		return nil, false
	}
	return state, true
}

// GetTenant retrieves a tenant from the cache.
func (c *Cache) GetTenant(name string) (*TenantState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, exists := c.tenants[name]
	if !exists {
		return nil, false
	}
	return state, true
}

// NodeCount returns the number of nodes in the cache.
func (c *Cache) NodeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

// SandboxCount returns the number of sandboxes in the cache.
func (c *Cache) SandboxCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.sandboxes)
}

// CacheSnapshot is a point-in-time snapshot of the cache.
type CacheSnapshot struct {
	Nodes     map[string]*NodeState
	Sandboxes map[string]*SandboxState
	Tenants   map[string]*TenantState
	Timestamp time.Time
}

// sandboxKey generates a cache key for a sandbox.
func sandboxKey(sb *v1alpha1.Sandbox) string {
	return sb.Namespace + "/" + sb.Name
}
