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

package queue

import (
	"container/heap"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
)

// SchedulingQueue is an interface for scheduling queues.
// It orders sandboxes by priority and handles backoff for failed scheduling.
type SchedulingQueue interface {
	// Add adds a sandbox to the queue.
	Add(sandboxInfo *framework.SandboxInfo)
	// AddUnschedulableIfNotPresent adds an unschedulable sandbox back to the queue.
	AddUnschedulableIfNotPresent(sandboxInfo *framework.SandboxInfo)
	// Pop removes and returns the highest priority sandbox.
	Pop() (*framework.SandboxInfo, error)
	// Update updates a sandbox in the queue.
	Update(sandboxInfo *framework.SandboxInfo)
	// Delete removes a sandbox from the queue.
	Delete(key string)
	// Len returns the number of sandboxes in the queue.
	Len() int
	// UnschedulableLen returns the number of unschedulable sandboxes.
	UnschedulableLen() int
	// Close closes the queue.
	Close()
	// Run starts the queue's background goroutines.
	Run()
}

// PriorityQueue implements SchedulingQueue using a priority queue.
// It is inspired by the Kubernetes 1.23.17 scheduler's PriorityQueue.
type PriorityQueue struct {
	mu sync.RWMutex

	// activeQ is the active queue of sandboxes to be scheduled.
	activeQ *priorityQueue

	// unschedulableQ holds sandboxes that failed scheduling.
	unschedulableQ *unschedulableQueue

	// backoffMap maps sandbox key to its backoff duration.
	backoffMap map[string]time.Duration

	// clock is used for time operations.
	clock clock

	// stopCh is used to signal shutdown.
	stopCh chan struct{}

	// maxBackoffDuration is the maximum backoff duration.
	maxBackoffDuration time.Duration

	// baseBackoffDuration is the base backoff duration.
	baseBackoffDuration time.Duration
}

// clock provides time operations.
type clock interface {
	Now() time.Time
}

// realClock implements clock using real time.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// NewPriorityQueue creates a new PriorityQueue.
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		activeQ:             newPriorityQueue(),
		unschedulableQ:      newUnschedulableQueue(),
		backoffMap:          make(map[string]time.Duration),
		clock:               realClock{},
		stopCh:              make(chan struct{}),
		maxBackoffDuration:  10 * time.Minute,
		baseBackoffDuration: 1 * time.Second,
	}
}

// Run starts the queue's background goroutines.
func (q *PriorityQueue) Run() {
	go wait.Until(q.flushUnschedulable, 30*time.Second, q.stopCh)
	go wait.Until(q.flushBackoff, 10*time.Second, q.stopCh)
}

// Add adds a sandbox to the active queue.
func (q *PriorityQueue) Add(sandboxInfo *framework.SandboxInfo) {
	q.mu.Lock()
	defer q.mu.Unlock()

	key := getSandboxKey(sandboxInfo)

	// Remove from unschedulable queue if present
	q.unschedulableQ.Delete(key)

	// Add to active queue
	heap.Push(q.activeQ, &sandboxInfoWrapper{
		SandboxInfo: sandboxInfo,
		priority:    getPriority(sandboxInfo),
		timestamp:   q.clock.Now(),
	})

	klog.V(5).Infof("Added sandbox %s to scheduling queue", key)
}

// AddUnschedulableIfNotPresent adds an unschedulable sandbox back to the queue.
func (q *PriorityQueue) AddUnschedulableIfNotPresent(sandboxInfo *framework.SandboxInfo) {
	q.mu.Lock()
	defer q.mu.Unlock()

	key := getSandboxKey(sandboxInfo)

	// Don't add if already in active queue
	if q.activeQ.Exists(key) {
		return
	}

	// Update backoff
	sandboxInfo.Attempts++
	backoff := q.calculateBackoff(key, sandboxInfo.Attempts)
	q.backoffMap[key] = backoff

	// Add to unschedulable queue
	q.unschedulableQ.Add(sandboxInfo, q.clock.Now().Add(backoff))

	klog.V(5).Infof("Added unschedulable sandbox %s (attempt %d, backoff %v)",
		key, sandboxInfo.Attempts, backoff)
}

// Pop removes and returns the highest priority sandbox.
func (q *PriorityQueue) Pop() (*framework.SandboxInfo, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.activeQ.Len() == 0 {
		return nil, fmt.Errorf("scheduling queue is empty")
	}

	wrapper := heap.Pop(q.activeQ).(*sandboxInfoWrapper)
	return wrapper.SandboxInfo, nil
}

// Update updates a sandbox in the queue.
func (q *PriorityQueue) Update(sandboxInfo *framework.SandboxInfo) {
	q.mu.Lock()
	defer q.mu.Unlock()

	key := getSandboxKey(sandboxInfo)

	// Try to update in active queue
	if q.activeQ.Exists(key) {
		q.activeQ.Update(&sandboxInfoWrapper{
			SandboxInfo: sandboxInfo,
			priority:    getPriority(sandboxInfo),
			timestamp:   q.clock.Now(),
		})
		return
	}

	// Update in unschedulable queue
	q.unschedulableQ.Update(sandboxInfo)
}

// Delete removes a sandbox from the queue.
func (q *PriorityQueue) Delete(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.activeQ.Delete(key)
	q.unschedulableQ.Delete(key)
	delete(q.backoffMap, key)
}

// Len returns the number of sandboxes in the queue.
func (q *PriorityQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.activeQ.Len() + q.unschedulableQ.Len()
}

// UnschedulableLen returns the number of unschedulable sandboxes.
func (q *PriorityQueue) UnschedulableLen() int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.unschedulableQ.Len()
}

// Close closes the queue.
func (q *PriorityQueue) Close() {
	close(q.stopCh)
}

// flushUnschedulable moves sandboxes from the unschedulable queue
// to the active queue when their backoff period has expired.
func (q *PriorityQueue) flushUnschedulable() {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock.Now()
	sandboxInfos := q.unschedulableQ.Flush(now)

	for _, sandboxInfo := range sandboxInfos {
		key := getSandboxKey(sandboxInfo)
		heap.Push(q.activeQ, &sandboxInfoWrapper{
			SandboxInfo: sandboxInfo,
			priority:    getPriority(sandboxInfo),
			timestamp:   now,
		})
		delete(q.backoffMap, key)

		klog.V(5).Infof("Flushed sandbox %s from unschedulable queue", key)
	}
}

// flushBackoff resets backoff for sandboxes that have waited long enough.
func (q *PriorityQueue) flushBackoff() {
	q.mu.Lock()
	defer q.mu.Unlock()

	for key, backoff := range q.backoffMap {
		if backoff > q.maxBackoffDuration {
			delete(q.backoffMap, key)
		}
	}
}

// calculateBackoff calculates the backoff duration for a sandbox.
func (q *PriorityQueue) calculateBackoff(key string, attempts int) time.Duration {
	backoff := q.baseBackoffDuration * time.Duration(1<<uint(attempts-1))
	if backoff > q.maxBackoffDuration {
		backoff = q.maxBackoffDuration
	}
	return backoff
}

// sandboxInfoWrapper wraps SandboxInfo for the priority queue.
type sandboxInfoWrapper struct {
	*framework.SandboxInfo
	priority  int32
	timestamp time.Time
	index     int
}

// priorityQueue implements heap.Interface.
type priorityQueue struct {
	items   []*sandboxInfoWrapper
	itemMap map[string]*sandboxInfoWrapper
}

func newPriorityQueue() *priorityQueue {
	pq := &priorityQueue{
		items:   make([]*sandboxInfoWrapper, 0),
		itemMap: make(map[string]*sandboxInfoWrapper),
	}
	heap.Init(pq)
	return pq
}

func (pq *priorityQueue) Len() int { return len(pq.items) }

func (pq *priorityQueue) Less(i, j int) bool {
	// Higher priority first
	if pq.items[i].priority != pq.items[j].priority {
		return pq.items[i].priority > pq.items[j].priority
	}
	// Earlier timestamp first
	return pq.items[i].timestamp.Before(pq.items[j].timestamp)
}

func (pq *priorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	item := x.(*sandboxInfoWrapper)
	item.index = len(pq.items)
	pq.items = append(pq.items, item)
	key := getSandboxKey(item.SandboxInfo)
	pq.itemMap[key] = item
}

func (pq *priorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	pq.items = old[:n-1]
	key := getSandboxKey(item.SandboxInfo)
	delete(pq.itemMap, key)
	return item
}

func (pq *priorityQueue) Exists(key string) bool {
	_, exists := pq.itemMap[key]
	return exists
}

func (pq *priorityQueue) Delete(key string) {
	item, exists := pq.itemMap[key]
	if !exists {
		return
	}
	heap.Remove(pq, item.index)
	delete(pq.itemMap, key)
}

func (pq *priorityQueue) Update(item *sandboxInfoWrapper) {
	key := getSandboxKey(item.SandboxInfo)
	if existing, exists := pq.itemMap[key]; exists {
		existing.SandboxInfo = item.SandboxInfo
		existing.priority = item.priority
		heap.Fix(pq, existing.index)
	}
}

// unschedulableQueue holds sandboxes that failed scheduling.
type unschedulableQueue struct {
	mu    sync.RWMutex
	items map[string]*unschedulableItem
}

type unschedulableItem struct {
	sandboxInfo *framework.SandboxInfo
	retryAfter  time.Time
}

func newUnschedulableQueue() *unschedulableQueue {
	return &unschedulableQueue{
		items: make(map[string]*unschedulableItem),
	}
}

func (uq *unschedulableQueue) Add(sandboxInfo *framework.SandboxInfo, retryAfter time.Time) {
	uq.mu.Lock()
	defer uq.mu.Unlock()

	key := getSandboxKey(sandboxInfo)
	uq.items[key] = &unschedulableItem{
		sandboxInfo: sandboxInfo,
		retryAfter:  retryAfter,
	}
}

func (uq *unschedulableQueue) Delete(key string) {
	uq.mu.Lock()
	defer uq.mu.Unlock()

	delete(uq.items, key)
}

func (uq *unschedulableQueue) Update(sandboxInfo *framework.SandboxInfo) {
	uq.mu.Lock()
	defer uq.mu.Unlock()

	key := getSandboxKey(sandboxInfo)
	if item, exists := uq.items[key]; exists {
		item.sandboxInfo = sandboxInfo
	}
}

func (uq *unschedulableQueue) Flush(now time.Time) []*framework.SandboxInfo {
	uq.mu.Lock()
	defer uq.mu.Unlock()

	result := make([]*framework.SandboxInfo, 0)
	for key, item := range uq.items {
		if now.After(item.retryAfter) {
			result = append(result, item.sandboxInfo)
			delete(uq.items, key)
		}
	}
	return result
}

func (uq *unschedulableQueue) Len() int {
	uq.mu.RLock()
	defer uq.mu.RUnlock()
	return len(uq.items)
}

// getSandboxKey returns the key for a sandbox.
func getSandboxKey(sandboxInfo *framework.SandboxInfo) string {
	if sandboxInfo.Sandbox == nil {
		return ""
	}
	return sandboxInfo.Sandbox.Namespace + "/" + sandboxInfo.Sandbox.Name
}

// getPriority returns the priority of a sandbox.
func getPriority(sandboxInfo *framework.SandboxInfo) int32 {
	if sandboxInfo.Sandbox == nil {
		return 0
	}

	// Use Spec.Priority as the base priority
	priority := int32(sandboxInfo.Sandbox.Spec.Priority)

	// Boost priority based on tenant tier
	switch sandboxInfo.Sandbox.Spec.TenantRef.Name {
	case "premium":
		priority += 1000
	case "standard":
		priority += 500
	}

	return priority
}

// BatchSchedulingQueue extends PriorityQueue with batch scheduling support.
// It groups sandboxes from the same batch and schedules them together.
type BatchSchedulingQueue struct {
	*PriorityQueue

	mu sync.RWMutex

	// batches maps batch ID to its batch info.
	batches map[string]*BatchInfo
}

// BatchInfo holds information about a batch of sandboxes.
type BatchInfo struct {
	// ID is the batch identifier.
	ID string
	// TenantName is the tenant that owns the batch.
	TenantName string
	// TotalCount is the total number of sandboxes in the batch.
	TotalCount int
	// PendingCount is the number of sandboxes still pending.
	PendingCount int
	// CompletedCount is the number of sandboxes completed.
	CompletedCount int
	// FailedCount is the number of sandboxes failed.
	FailedCount int
	// CreatedAt is the time the batch was created.
	CreatedAt time.Time
	// MinAvailable is the minimum number of sandboxes that must be
	// scheduled together (gang scheduling).
	MinAvailable int
}

// NewBatchSchedulingQueue creates a new BatchSchedulingQueue.
func NewBatchSchedulingQueue() *BatchSchedulingQueue {
	return &BatchSchedulingQueue{
		PriorityQueue: NewPriorityQueue(),
		batches:       make(map[string]*BatchInfo),
	}
}

// AddBatch adds a batch of sandboxes.
func (bq *BatchSchedulingQueue) AddBatch(batchID, tenantName string, sandboxes []*framework.SandboxInfo, minAvailable int) {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	bq.batches[batchID] = &BatchInfo{
		ID:             batchID,
		TenantName:     tenantName,
		TotalCount:     len(sandboxes),
		PendingCount:   len(sandboxes),
		CompletedCount: 0,
		FailedCount:    0,
		CreatedAt:      time.Now(),
		MinAvailable:   minAvailable,
	}

	// Add all sandboxes to the queue
	for _, sandboxInfo := range sandboxes {
		bq.Add(sandboxInfo)
	}

	klog.Infof("Added batch %s with %d sandboxes (minAvailable: %d)",
		batchID, len(sandboxes), minAvailable)
}

// GetBatch returns batch information.
func (bq *BatchSchedulingQueue) GetBatch(batchID string) (*BatchInfo, bool) {
	bq.mu.RLock()
	defer bq.mu.RUnlock()

	batch, exists := bq.batches[batchID]
	if !exists {
		return nil, false
	}

	copy := *batch
	return &copy, true
}

// UpdateBatchStatus updates the status of a batch.
func (bq *BatchSchedulingQueue) UpdateBatchStatus(batchID string, completed, failed int) {
	bq.mu.Lock()
	defer bq.mu.Unlock()

	batch, exists := bq.batches[batchID]
	if !exists {
		return
	}

	batch.CompletedCount += completed
	batch.FailedCount += failed
	batch.PendingCount -= (completed + failed)

	if batch.PendingCount < 0 {
		batch.PendingCount = 0
	}
}

// IsBatchReady checks if a batch is ready for gang scheduling.
func (bq *BatchSchedulingQueue) IsBatchReady(batchID string) bool {
	bq.mu.RLock()
	defer bq.mu.RUnlock()

	batch, exists := bq.batches[batchID]
	if !exists {
		return false
	}

	// For gang scheduling, check if we have enough resources for minAvailable
	available := batch.TotalCount - batch.PendingCount
	return available >= batch.MinAvailable
}

// ListBatches returns all batches.
func (bq *BatchSchedulingQueue) ListBatches() map[string]*BatchInfo {
	bq.mu.RLock()
	defer bq.mu.RUnlock()

	result := make(map[string]*BatchInfo, len(bq.batches))
	for key, batch := range bq.batches {
		copy := *batch
		result[key] = &copy
	}
	return result
}

// sandboxv1alpha1Sandbox is used for type assertion.
var _ = (*sandboxv1alpha1.Sandbox)(nil)
