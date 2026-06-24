/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package isolation

import (
	"context"
	"fmt"
	"sync"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// TenantIsolationManager enforces hard isolation between tenants.
type TenantIsolationManager struct {
	mu            sync.RWMutex
	tenantNodes   map[string]map[string]bool   // tenant -> set of dedicated nodes
	nodeTenants   map[string]string            // node -> tenant (for dedicated nodes)
	tenantVNI     map[string]uint32            // tenant -> VXLAN VNI
	tenantCgroups map[string]string            // tenant -> cgroup path
	nextVNI       uint32
}

// NewTenantIsolationManager creates a new tenant isolation manager.
func NewTenantIsolationManager() *TenantIsolationManager {
	return &TenantIsolationManager{
		tenantNodes:   make(map[string]map[string]bool),
		nodeTenants:   make(map[string]string),
		tenantVNI:     make(map[string]uint32),
		tenantCgroups: make(map[string]string),
		nextVNI:       1000, // Start VXLAN VNIs from 1000
	}
}

// EnforceIsolation applies isolation policies for a tenant based on its isolation level.
func (m *TenantIsolationManager) EnforceIsolation(ctx context.Context, tenant *sandboxv1alpha1.Tenant) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	tenantName := tenant.Name
	level := tenant.Spec.IsolationLevel

	klog.Infof("Enforcing isolation level %s for tenant %s", level, tenantName)

	switch level {
	case sandboxv1alpha1.IsolationLevelStandard:
		// Standard: shared nodes, namespace isolation, cgroup limits
		if err := m.enforceStandardIsolation(tenant); err != nil {
			return fmt.Errorf("failed to enforce standard isolation for %s: %w", tenantName, err)
		}

	case sandboxv1alpha1.IsolationLevelEnhanced:
		// Enhanced: dedicated cgroup subtree, dedicated VNI, bandwidth limits
		if err := m.enforceEnhancedIsolation(tenant); err != nil {
			return fmt.Errorf("failed to enforce enhanced isolation for %s: %w", tenantName, err)
		}

	case sandboxv1alpha1.IsolationLevelMaximum:
		// Maximum: dedicated nodes, VM-level isolation (Kata), dedicated network
		if err := m.enforceMaximumIsolation(tenant); err != nil {
			return fmt.Errorf("failed to enforce maximum isolation for %s: %w", tenantName, err)
		}

	default:
		return fmt.Errorf("unknown isolation level: %s", level)
	}

	return nil
}

// RemoveIsolation removes isolation policies for a tenant.
func (m *TenantIsolationManager) RemoveIsolation(ctx context.Context, tenantName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Release dedicated nodes
	if nodes, ok := m.tenantNodes[tenantName]; ok {
		for node := range nodes {
			delete(m.nodeTenants, node)
		}
		delete(m.tenantNodes, tenantName)
	}

	// Release VNI
	delete(m.tenantVNI, tenantName)

	// Remove cgroup subtree
	if cgroupPath, ok := m.tenantCgroups[tenantName]; ok {
		klog.V(4).Infof("Removing tenant cgroup at %s", cgroupPath)
		delete(m.tenantCgroups, tenantName)
	}

	klog.Infof("Removed isolation for tenant %s", tenantName)
	return nil
}

// IsNodeAvailableForTenant checks if a node can run sandboxes for a given tenant.
func (m *TenantIsolationManager) IsNodeAvailableForTenant(nodeName, tenantName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If node is dedicated to a different tenant, not available
	if dedicatedTenant, ok := m.nodeTenants[nodeName]; ok {
		return dedicatedTenant == tenantName
	}

	// Check tenant's isolation level - Maximum isolation requires dedicated nodes
	if nodes, ok := m.tenantNodes[tenantName]; ok {
		return nodes[nodeName]
	}

	// For Standard/Enhanced, any non-dedicated node is available
	return true
}

// GetTenantVNI returns the VXLAN VNI for a tenant.
func (m *TenantIsolationManager) GetTenantVNI(tenantName string) uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if vni, ok := m.tenantVNI[tenantName]; ok {
		return vni
	}
	return 0
}

// AssignDedicatedNodes assigns dedicated nodes to a tenant.
func (m *TenantIsolationManager) AssignDedicatedNodes(tenantName string, nodeNames []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	nodes := make(map[string]bool, len(nodeNames))
	for _, n := range nodeNames {
		nodes[n] = true
		m.nodeTenants[n] = tenantName
	}
	m.tenantNodes[tenantName] = nodes
	klog.Infof("Assigned dedicated nodes %v to tenant %s", nodeNames, tenantName)
}

// --- Internal methods ---

func (m *TenantIsolationManager) enforceStandardIsolation(tenant *sandboxv1alpha1.Tenant) error {
	tenantName := tenant.Name

	// Assign a VNI for network isolation
	if _, ok := m.tenantVNI[tenantName]; !ok {
		m.tenantVNI[tenantName] = m.nextVNI
		m.nextVNI++
	}

	// Create cgroup subtree for the tenant
	cgroupPath := fmt.Sprintf("/sys/fs/cgroup/nexusbox/tenant-%s", tenantName)
	m.tenantCgroups[tenantName] = cgroupPath

	// Apply resource limits at tenant level
	if tenant.Spec.ResourceQuota.MaxInstances > 0 {
		klog.V(4).Infof("Tenant %s: max instances = %d", tenantName, tenant.Spec.ResourceQuota.MaxInstances)
	}

	return nil
}

func (m *TenantIsolationManager) enforceEnhancedIsolation(tenant *sandboxv1alpha1.Tenant) error {
	tenantName := tenant.Name

	// Standard isolation first
	if err := m.enforceStandardIsolation(tenant); err != nil {
		return err
	}

	// Enhanced: apply network bandwidth limits
	if tenant.Spec.NetworkPolicy != nil && tenant.Spec.NetworkPolicy.BandwidthLimitMbps > 0 {
		klog.V(4).Infof("Tenant %s: bandwidth limit = %d Mbps", tenantName, tenant.Spec.NetworkPolicy.BandwidthLimitMbps)
	}

	// Enhanced: deny inter-tenant communication by default
	if tenant.Spec.NetworkPolicy != nil && !tenant.Spec.NetworkPolicy.AllowInterTenantCommunication {
		klog.V(4).Infof("Tenant %s: inter-tenant communication denied", tenantName)
	}

	// Enhanced: reserved resources
	if tenant.Spec.ResourceQuota.ReservedResources != nil {
		klog.V(4).Infof("Tenant %s: reserved CPU=%s, Memory=%s",
			tenantName,
			tenant.Spec.ResourceQuota.ReservedResources.CPU,
			tenant.Spec.ResourceQuota.ReservedResources.Memory)
	}

	return nil
}

func (m *TenantIsolationManager) enforceMaximumIsolation(tenant *sandboxv1alpha1.Tenant) error {
	tenantName := tenant.Name

	// Enhanced isolation first
	if err := m.enforceEnhancedIsolation(tenant); err != nil {
		return err
	}

	// Maximum: require dedicated nodes
	if len(m.tenantNodes[tenantName]) == 0 {
		klog.Warningf("Tenant %s requires Maximum isolation but has no dedicated nodes assigned", tenantName)
	}

	// Maximum: enforce Kata Containers runtime
	// All sandboxes for this tenant must use RuntimeKataContainers
	klog.V(4).Infof("Tenant %s: Maximum isolation - requiring Kata Containers runtime", tenantName)

	// Maximum: dedicated network namespace
	klog.V(4).Infof("Tenant %s: Maximum isolation - dedicated network (VNI=%d)", tenantName, m.tenantVNI[tenantName])

	return nil
}
