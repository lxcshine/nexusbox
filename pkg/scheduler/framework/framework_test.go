/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package framework

import (
	"testing"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewSandboxInfo(t *testing.T) {
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sb",
			Namespace: "default",
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			Priority: sandboxv1alpha1.PriorityNormal,
			Runtime:  sandboxv1alpha1.RuntimeRunc,
		},
	}

	info := NewSandboxInfo(sb)
	if info.Sandbox.Name != "test-sb" {
		t.Errorf("SandboxInfo.Sandbox.Name = %q, want %q", info.Sandbox.Name, "test-sb")
	}
	if info.Attempts != 0 {
		t.Errorf("SandboxInfo.Attempts = %d, want 0", info.Attempts)
	}
}

func TestNewResource(t *testing.T) {
	r := NewResource(&sandboxv1alpha1.ResourceRequirements{
		CPU:    "1",
		Memory: "2Gi",
	})
	if r.MilliCPU != 1000 {
		t.Errorf("MilliCPU = %d, want 1000", r.MilliCPU)
	}
	if r.Memory != 2*1024*1024*1024 {
		t.Errorf("Memory = %d, want 2Gi", r.Memory)
	}
}

func TestResourceAddResource(t *testing.T) {
	r1 := NewResource(&sandboxv1alpha1.ResourceRequirements{CPU: "1", Memory: "1Ki"})
	r2 := NewResource(&sandboxv1alpha1.ResourceRequirements{CPU: "500m", Memory: "512"})
	r1.AddResource(r2)

	if r1.MilliCPU != 1500 {
		t.Errorf("MilliCPU after Add = %d, want 1500", r1.MilliCPU)
	}
	if r1.Memory != 1536 {
		t.Errorf("Memory after Add = %d, want 1536", r1.Memory)
	}
}

func TestResourceSubResource(t *testing.T) {
	r1 := NewResource(&sandboxv1alpha1.ResourceRequirements{CPU: "1", Memory: "1Ki"})
	r2 := NewResource(&sandboxv1alpha1.ResourceRequirements{CPU: "500m", Memory: "512"})
	r1.SubResource(r2)

	if r1.MilliCPU != 500 {
		t.Errorf("MilliCPU after Sub = %d, want 500", r1.MilliCPU)
	}
	if r1.Memory != 512 {
		t.Errorf("Memory after Sub = %d, want 512", r1.Memory)
	}
}

func TestResourceFits(t *testing.T) {
	available := NewResource(&sandboxv1alpha1.ResourceRequirements{CPU: "2", Memory: "4Ki"})

	// Should fit
	if !available.Fits(&sandboxv1alpha1.ResourceRequirements{CPU: "1", Memory: "2Ki"}) {
		t.Error("expected Fits to return true for smaller request")
	}

	// Should not fit (CPU)
	if available.Fits(&sandboxv1alpha1.ResourceRequirements{CPU: "3", Memory: "2Ki"}) {
		t.Error("expected Fits to return false for larger CPU request")
	}

	// Should not fit (Memory)
	if available.Fits(&sandboxv1alpha1.ResourceRequirements{CPU: "1", Memory: "8Ki"}) {
		t.Error("expected Fits to return false for larger Memory request")
	}
}

func TestParseQuantityCPU(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1", 1000},
		{"2", 2000},
		{"500m", 500},
		{"1000m", 1000},
		{"0.5", 500},
	}

	for _, tt := range tests {
		got, err := ParseQuantityCPU(tt.input)
		if err != nil {
			t.Errorf("ParseQuantityCPU(%q) error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseQuantityCPU(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseQuantityMem(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1Ki", 1024},
		{"1Mi", 1024 * 1024},
		{"1Gi", 1024 * 1024 * 1024},
	}

	for _, tt := range tests {
		got, err := ParseQuantityMem(tt.input)
		if err != nil {
			t.Errorf("ParseQuantityMem(%q) error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseQuantityMem(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestNodeInfoAddSandbox(t *testing.T) {
	node := &sandboxv1alpha1.SandboxNode{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
	}
	ni := NewNodeInfo(node)

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb1", Namespace: "default"},
		Spec: sandboxv1alpha1.SandboxSpec{
			Resources: sandboxv1alpha1.ResourceRequirements{
				CPU:    "1",
				Memory: "1Gi",
			},
		},
	}
	sandboxInfo := NewSandboxInfo(sb)

	ni.AddSandbox(sandboxInfo)

	if len(ni.Sandboxes) != 1 {
		t.Errorf("len(Sandboxes) = %d, want 1", len(ni.Sandboxes))
	}
	if ni.SandboxCount() != 1 {
		t.Errorf("SandboxCount() = %d, want 1", ni.SandboxCount())
	}
}

func TestNodeInfoRemoveSandbox(t *testing.T) {
	node := &sandboxv1alpha1.SandboxNode{
		ObjectMeta: metav1.ObjectMeta{Name: "node1"},
	}
	ni := NewNodeInfo(node)

	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb1", Namespace: "default"},
	}
	sandboxInfo := NewSandboxInfo(sb)

	ni.AddSandbox(sandboxInfo)
	ni.RemoveSandbox(sandboxInfo)

	if len(ni.Sandboxes) != 0 {
		t.Errorf("len(Sandboxes) after remove = %d, want 0", len(ni.Sandboxes))
	}
}

func TestCycleState(t *testing.T) {
	cs := NewCycleState()

	// Write and Read
	cs.Write("test-key", &testStateData{value: "hello"})

	data, err := cs.Read("test-key")
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if data.(*testStateData).value != "hello" {
		t.Errorf("Read value = %q, want %q", data.(*testStateData).value, "hello")
	}

	// Delete
	cs.Delete("test-key")
	_, err = cs.Read("test-key")
	if err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

type testStateData struct {
	value string
}

func (d *testStateData) Clone() StateData {
	return &testStateData{value: d.value}
}
