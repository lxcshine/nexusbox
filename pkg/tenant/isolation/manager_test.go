/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package isolation

import (
	"context"
	"testing"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewTenantIsolationManager(t *testing.T) {
	m := NewTenantIsolationManager()
	if m == nil {
		t.Fatal("NewTenantIsolationManager returned nil")
	}
}

func TestIsNodeAvailableForTenant_Standard(t *testing.T) {
	m := NewTenantIsolationManager()

	// For standard isolation, any non-dedicated node is available
	if !m.IsNodeAvailableForTenant("node1", "tenant1") {
		t.Error("node1 should be available for tenant1 with standard isolation")
	}
}

func TestIsNodeAvailableForTenant_DedicatedNodes(t *testing.T) {
	m := NewTenantIsolationManager()

	// Assign dedicated nodes to tenant1
	m.AssignDedicatedNodes("tenant1", []string{"node1", "node2"})

	// node1 should be available for tenant1
	if !m.IsNodeAvailableForTenant("node1", "tenant1") {
		t.Error("node1 should be available for tenant1 (dedicated)")
	}

	// node1 should NOT be available for tenant2
	if m.IsNodeAvailableForTenant("node1", "tenant2") {
		t.Error("node1 should NOT be available for tenant2 (dedicated to tenant1)")
	}

	// node3 (not dedicated) should be available for tenant2
	if !m.IsNodeAvailableForTenant("node3", "tenant2") {
		t.Error("node3 should be available for tenant2 (not dedicated)")
	}
}

func TestGetTenantVNI(t *testing.T) {
	m := NewTenantIsolationManager()

	// No VNI assigned yet
	if vni := m.GetTenantVNI("tenant1"); vni != 0 {
		t.Errorf("VNI for unassigned tenant = %d, want 0", vni)
	}
}

func TestEnforceStandardIsolation(t *testing.T) {
	m := NewTenantIsolationManager()

	tenant := &sandboxv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant1"},
		Spec: sandboxv1alpha1.TenantSpec{
			IsolationLevel: sandboxv1alpha1.IsolationLevelStandard,
			ResourceQuota: sandboxv1alpha1.TenantResourceQuota{
				MaxInstances: 50,
			},
		},
	}

	err := m.EnforceIsolation(context.Background(), tenant)
	if err != nil {
		t.Fatalf("EnforceIsolation failed: %v", err)
	}

	// Should have a VNI assigned
	if vni := m.GetTenantVNI("tenant1"); vni == 0 {
		t.Error("expected VNI to be assigned for tenant1")
	}
}
