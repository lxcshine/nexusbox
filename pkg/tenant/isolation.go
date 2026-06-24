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

package tenant

import (
	"fmt"
	"sync"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// IsolationEnforcer enforces multi-tenant isolation policies.
// It ensures that sandboxes from different tenants are properly isolated
// at the network, compute, and storage layers.
type IsolationEnforcer struct {
	mu sync.RWMutex
	// policies maps tenant name to its isolation policy.
	policies map[string]*TenantIsolationPolicy
	// networkIsolation tracks network isolation rules between tenants.
	networkIsolation *NetworkIsolationManager
	// nodeIsolation tracks node-level isolation assignments.
	nodeIsolation *NodeIsolationManager
}

// TenantIsolationPolicy defines the isolation policy for a tenant.
type TenantIsolationPolicy struct {
	// TenantName is the name of the tenant.
	TenantName string
	// IsolationLevel is the configured isolation level.
	IsolationLevel sandboxv1alpha1.TenantIsolationLevel
	// NetworkPolicy defines network isolation rules.
	NetworkPolicy *sandboxv1alpha1.TenantNetworkPolicy
	// DedicatedNodes lists nodes dedicated to this tenant.
	DedicatedNodes []string
	// PreferredNodes lists nodes preferred for this tenant.
	PreferredNodes []string
	// ForbiddenNodes lists nodes forbidden for this tenant.
	ForbiddenNodes []string
	// ResourceOvercommitPercent is the allowed resource overcommit percentage.
	ResourceOvercommitPercent int32
}

// NetworkIsolationManager manages network isolation between tenants.
type NetworkIsolationManager struct {
	mu sync.RWMutex
	// tenantNetworks maps tenant name to its network identifier.
	tenantNetworks map[string]string
	// interTenantRules defines rules for inter-tenant communication.
	interTenantRules map[string]map[string]bool // fromTenant -> toTenant -> allowed
}

// NodeIsolationManager manages node-level isolation for tenants.
type NodeIsolationManager struct {
	mu sync.RWMutex
	// nodeAssignments maps node name to the tenant that owns it.
	nodeAssignments map[string]string
	// tenantNodes maps tenant name to its assigned nodes.
	tenantNodes map[string][]string
}

// NewIsolationEnforcer creates a new IsolationEnforcer.
func NewIsolationEnforcer() *IsolationEnforcer {
	return &IsolationEnforcer{
		policies: make(map[string]*TenantIsolationPolicy),
		networkIsolation: &NetworkIsolationManager{
			tenantNetworks:   make(map[string]string),
			interTenantRules: make(map[string]map[string]bool),
		},
		nodeIsolation: &NodeIsolationManager{
			nodeAssignments: make(map[string]string),
			tenantNodes:     make(map[string][]string),
		},
	}
}

// RegisterIsolationPolicy registers an isolation policy for a tenant.
func (ie *IsolationEnforcer) RegisterIsolationPolicy(tenantName string, spec sandboxv1alpha1.TenantSpec) error {
	ie.mu.Lock()
	defer ie.mu.Unlock()

	policy := &TenantIsolationPolicy{
		TenantName:      tenantName,
		IsolationLevel:  spec.IsolationLevel,
		NetworkPolicy:   spec.NetworkPolicy,
		DedicatedNodes:  []string{},
		PreferredNodes:  []string{},
		ForbiddenNodes:  []string{},
	}

	// Configure based on isolation level
	switch spec.IsolationLevel {
	case sandboxv1alpha1.IsolationLevelMaximum:
		// Maximum isolation: dedicated nodes, no overcommit
		policy.ResourceOvercommitPercent = 0
	case sandboxv1alpha1.IsolationLevelEnhanced:
		// Enhanced isolation: preferred nodes, limited overcommit
		policy.ResourceOvercommitPercent = 50
	case sandboxv1alpha1.IsolationLevelStandard:
		// Standard isolation: shared nodes, normal overcommit
		policy.ResourceOvercommitPercent = 100
	}

	ie.policies[tenantName] = policy

	// Register network isolation
	if spec.NetworkPolicy != nil {
		ie.networkIsolation.RegisterTenantNetwork(tenantName, spec.NetworkPolicy)
	}

	klog.Infof("Registered isolation policy for tenant %s (level: %s)", tenantName, spec.IsolationLevel)
	return nil
}

// UnregisterIsolationPolicy removes an isolation policy for a tenant.
func (ie *IsolationEnforcer) UnregisterIsolationPolicy(tenantName string) {
	ie.mu.Lock()
	defer ie.mu.Unlock()

	delete(ie.policies, tenantName)
	ie.networkIsolation.UnregisterTenantNetwork(tenantName)
	ie.nodeIsolation.UnregisterTenantNodes(tenantName)
}

// IsNodeAllowed checks if a tenant is allowed to schedule on a specific node.
func (ie *IsolationEnforcer) IsNodeAllowed(tenantName, nodeName string) bool {
	ie.mu.RLock()
	defer ie.mu.RUnlock()

	policy, exists := ie.policies[tenantName]
	if !exists {
		// No policy means standard isolation, all nodes allowed
		return true
	}

	// Check forbidden nodes
	for _, node := range policy.ForbiddenNodes {
		if node == nodeName {
			return false
		}
	}

	// Check dedicated nodes for other tenants
	ie.nodeIsolation.mu.RLock()
	defer ie.nodeIsolation.mu.RUnlock()

	if assignedTenant, assigned := ie.nodeIsolation.nodeAssignments[nodeName]; assigned {
		if assignedTenant != tenantName {
			// Node is dedicated to another tenant
			return false
		}
	}

	return true
}

// IsNetworkCommunicationAllowed checks if network communication between two tenants is allowed.
func (ie *IsolationEnforcer) IsNetworkCommunicationAllowed(fromTenant, toTenant string) bool {
	ie.networkIsolation.mu.RLock()
	defer ie.networkIsolation.mu.RUnlock()

	// Same tenant communication is always allowed
	if fromTenant == toTenant {
		return true
	}

	// Check inter-tenant rules
	if rules, exists := ie.networkIsolation.interTenantRules[fromTenant]; exists {
		if allowed, exists := rules[toTenant]; exists {
			return allowed
		}
	}

	// Check from-tenant's policy
	ie.mu.RLock()
	defer ie.mu.RUnlock()

	policy, exists := ie.policies[fromTenant]
	if !exists || policy.NetworkPolicy == nil {
		// Default: deny inter-tenant communication
		return false
	}

	return policy.NetworkPolicy.AllowInterTenantCommunication
}

// AssignDedicatedNode assigns a dedicated node to a tenant.
func (ie *IsolationEnforcer) AssignDedicatedNode(tenantName, nodeName string) error {
	ie.nodeIsolation.mu.Lock()
	defer ie.nodeIsolation.mu.Unlock()

	// Check if node is already assigned
	if assignedTenant, assigned := ie.nodeIsolation.nodeAssignments[nodeName]; assigned {
		if assignedTenant != tenantName {
			return fmt.Errorf("node %s is already dedicated to tenant %s", nodeName, assignedTenant)
		}
		return nil // Already assigned to this tenant
	}

	ie.nodeIsolation.nodeAssignments[nodeName] = tenantName
	ie.nodeIsolation.tenantNodes[tenantName] = append(ie.nodeIsolation.tenantNodes[tenantName], nodeName)

	// Update the policy
	ie.mu.Lock()
	defer ie.mu.Unlock()

	if policy, exists := ie.policies[tenantName]; exists {
		policy.DedicatedNodes = append(policy.DedicatedNodes, nodeName)
	}

	klog.Infof("Assigned dedicated node %s to tenant %s", nodeName, tenantName)
	return nil
}

// ReleaseDedicatedNode releases a dedicated node from a tenant.
func (ie *IsolationEnforcer) ReleaseDedicatedNode(tenantName, nodeName string) error {
	ie.nodeIsolation.mu.Lock()
	defer ie.nodeIsolation.mu.Unlock()

	assignedTenant, assigned := ie.nodeIsolation.nodeAssignments[nodeName]
	if !assigned || assignedTenant != tenantName {
		return fmt.Errorf("node %s is not dedicated to tenant %s", nodeName, tenantName)
	}

	delete(ie.nodeIsolation.nodeAssignments, nodeName)

	// Remove from tenant's node list
	nodes := ie.nodeIsolation.tenantNodes[tenantName]
	for i, node := range nodes {
		if node == nodeName {
			ie.nodeIsolation.tenantNodes[tenantName] = append(nodes[:i], nodes[i+1:]...)
			break
		}
	}

	// Update the policy
	ie.mu.Lock()
	defer ie.mu.Unlock()

	if policy, exists := ie.policies[tenantName]; exists {
		for i, node := range policy.DedicatedNodes {
			if node == nodeName {
				policy.DedicatedNodes = append(policy.DedicatedNodes[:i], policy.DedicatedNodes[i+1:]...)
				break
			}
		}
	}

	klog.Infof("Released dedicated node %s from tenant %s", nodeName, tenantName)
	return nil
}

// GetIsolationPolicy returns the isolation policy for a tenant.
func (ie *IsolationEnforcer) GetIsolationPolicy(tenantName string) (*TenantIsolationPolicy, bool) {
	ie.mu.RLock()
	defer ie.mu.RUnlock()

	policy, exists := ie.policies[tenantName]
	if !exists {
		return nil, false
	}

	// Return a copy
	copy := *policy
	return &copy, true
}

// GetDedicatedNodes returns the dedicated nodes for a tenant.
func (ie *IsolationEnforcer) GetDedicatedNodes(tenantName string) []string {
	ie.nodeIsolation.mu.RLock()
	defer ie.nodeIsolation.mu.RUnlock()

	nodes, exists := ie.nodeIsolation.tenantNodes[tenantName]
	if !exists {
		return nil
	}

	result := make([]string, len(nodes))
	copy(result, nodes)
	return result
}

// --- NetworkIsolationManager methods ---

// RegisterTenantNetwork registers network isolation for a tenant.
func (nim *NetworkIsolationManager) RegisterTenantNetwork(tenantName string, policy *sandboxv1alpha1.TenantNetworkPolicy) {
	nim.mu.Lock()
	defer nim.mu.Unlock()

	// Assign a unique network identifier
	nim.tenantNetworks[tenantName] = fmt.Sprintf("tenant-%s-net", tenantName)

	// Set up inter-tenant communication rules
	if policy != nil && policy.AllowInterTenantCommunication {
		// Allow communication with all tenants
		if nim.interTenantRules[tenantName] == nil {
			nim.interTenantRules[tenantName] = make(map[string]bool)
		}
		// Mark as allowing all
		nim.interTenantRules[tenantName]["*"] = true
	}

	// Set up specific allowed ingress tenants
	if policy != nil && len(policy.AllowedIngressFromTenants) > 0 {
		if nim.interTenantRules[tenantName] == nil {
			nim.interTenantRules[tenantName] = make(map[string]bool)
		}
		for _, fromTenant := range policy.AllowedIngressFromTenants {
			if nim.interTenantRules[fromTenant] == nil {
				nim.interTenantRules[fromTenant] = make(map[string]bool)
			}
			nim.interTenantRules[fromTenant][tenantName] = true
		}
	}

	klog.V(4).Infof("Registered network isolation for tenant %s", tenantName)
}

// UnregisterTenantNetwork removes network isolation for a tenant.
func (nim *NetworkIsolationManager) UnregisterTenantNetwork(tenantName string) {
	nim.mu.Lock()
	defer nim.mu.Unlock()

	delete(nim.tenantNetworks, tenantName)
	delete(nim.interTenantRules, tenantName)

	// Remove references from other tenants' rules
	for fromTenant, rules := range nim.interTenantRules {
		delete(rules, tenantName)
		if len(rules) == 0 {
			delete(nim.interTenantRules, fromTenant)
		}
	}
}

// --- NodeIsolationManager methods ---

// UnregisterTenantNodes removes all node assignments for a tenant.
func (nim *NodeIsolationManager) UnregisterTenantNodes(tenantName string) {
	nim.mu.Lock()
	defer nim.mu.Unlock()

	nodes, exists := nim.tenantNodes[tenantName]
	if !exists {
		return
	}

	for _, node := range nodes {
		delete(nim.nodeAssignments, node)
	}
	delete(nim.tenantNodes, tenantName)
}

// ValidateSandboxPlacement validates that a sandbox can be placed on a node
// according to the tenant's isolation policy.
func (ie *IsolationEnforcer) ValidateSandboxPlacement(tenantName, nodeName string, runtime sandboxv1alpha1.SandboxRuntimeType) error {
	ie.mu.RLock()
	defer ie.mu.RUnlock()

	policy, exists := ie.policies[tenantName]
	if !exists {
		return nil // No policy means no restrictions
	}

	// Check if node is allowed
	if !ie.IsNodeAllowed(tenantName, nodeName) {
		return fmt.Errorf("tenant %s is not allowed to schedule on node %s due to isolation policy",
			tenantName, nodeName)
	}

	// For maximum isolation, only allow dedicated nodes
	if policy.IsolationLevel == sandboxv1alpha1.IsolationLevelMaximum {
		isDedicated := false
		for _, node := range policy.DedicatedNodes {
			if node == nodeName {
				isDedicated = true
				break
			}
		}
		if !isDedicated {
			return fmt.Errorf("tenant %s with Maximum isolation requires dedicated nodes", tenantName)
		}
	}

	// For Kata Containers runtime, validate node support
	if runtime == sandboxv1alpha1.RuntimeKataContainers {
		// This would be validated against node labels in production
		klog.V(4).Infof("Validating Kata Containers support on node %s for tenant %s", nodeName, tenantName)
	}

	return nil
}
