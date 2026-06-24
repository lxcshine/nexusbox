/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package queue

import (
	"testing"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestSandboxInfo(name string, priority int32) *framework.SandboxInfo {
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			Priority: sandboxv1alpha1.SandboxPriority(priority),
		},
	}
	return framework.NewSandboxInfo(sb)
}

func TestNewPriorityQueue(t *testing.T) {
	q := NewPriorityQueue()
	if q == nil {
		t.Fatal("NewPriorityQueue returned nil")
	}
}

func TestPriorityQueueAddAndPop(t *testing.T) {
	q := NewPriorityQueue()

	// Start the queue
	go q.Run()

	// Add sandboxes
	sb1 := newTestSandboxInfo("sb1", 50)
	sb2 := newTestSandboxInfo("sb2", 100)
	sb3 := newTestSandboxInfo("sb3", 25)

	q.Add(sb1)
	q.Add(sb2)
	q.Add(sb3)

	// Pop should return highest priority first
	popped, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if popped.Sandbox.Name != "sb2" {
		t.Errorf("First pop = %q, want %q (highest priority)", popped.Sandbox.Name, "sb2")
	}
}

func TestPriorityQueueLen(t *testing.T) {
	q := NewPriorityQueue()

	if q.Len() != 0 {
		t.Errorf("empty queue Len() = %d, want 0", q.Len())
	}

	q.Add(newTestSandboxInfo("sb1", 50))
	q.Add(newTestSandboxInfo("sb2", 50))

	if q.Len() != 2 {
		t.Errorf("queue Len() = %d, want 2", q.Len())
	}
}

func TestPriorityQueueUnschedulableLen(t *testing.T) {
	q := NewPriorityQueue()

	if q.UnschedulableLen() != 0 {
		t.Errorf("empty queue UnschedulableLen() = %d, want 0", q.UnschedulableLen())
	}
}

func TestPriorityQueueAddUnschedulableIfNotPresent(t *testing.T) {
	q := NewPriorityQueue()

	sb := newTestSandboxInfo("sb1", 50)
	q.AddUnschedulableIfNotPresent(sb)

	if q.UnschedulableLen() != 1 {
		t.Errorf("UnschedulableLen() = %d, want 1", q.UnschedulableLen())
	}

	// Adding same sandbox again should not increase count
	q.AddUnschedulableIfNotPresent(sb)
	if q.UnschedulableLen() != 1 {
		t.Errorf("UnschedulableLen() after re-add = %d, want 1", q.UnschedulableLen())
	}
}

func TestPriorityQueueUpdate(t *testing.T) {
	q := NewPriorityQueue()

	sb := newTestSandboxInfo("sb1", 50)
	q.Add(sb)

	// Update with higher priority
	sbUpdated := newTestSandboxInfo("sb1", 100)
	q.Update(sbUpdated)

	go q.Run()

	popped, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if popped.Sandbox.Name != "sb1" {
		t.Errorf("Pop = %q, want %q", popped.Sandbox.Name, "sb1")
	}
}

func TestPriorityQueueDelete(t *testing.T) {
	q := NewPriorityQueue()

	sb := newTestSandboxInfo("sb1", 50)
	q.Add(sb)

	if q.Len() != 1 {
		t.Errorf("Len() = %d, want 1", q.Len())
	}

	q.Delete("default/sb1")

	if q.Len() != 0 {
		t.Errorf("Len() after delete = %d, want 0", q.Len())
	}
}

func TestPriorityQueueClose(t *testing.T) {
	q := NewPriorityQueue()
	go q.Run()

	// Give queue time to start
	time.Sleep(10 * time.Millisecond)

	q.Close()

	// Pop on closed queue should return error
	_, err := q.Pop()
	if err == nil {
		t.Error("Pop on closed queue should return error")
	}
}
