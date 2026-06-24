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
	"sync/atomic"
	"time"
)

// SchedulerMetrics tracks metrics for the scheduler.
// It provides insight into scheduling performance, success rates,
// and latency characteristics.
type SchedulerMetrics struct {
	// schedulingAttempts is the total number of scheduling attempts.
	schedulingAttempts atomic.Int64

	// schedulingSuccesses is the number of successful schedules.
	schedulingSuccesses atomic.Int64

	// schedulingFailures is the number of failed schedules.
	schedulingFailures atomic.Int64

	// schedulingUnschedulable is the number of unschedulable sandboxes.
	schedulingUnschedulable atomic.Int64

	// preemptionAttempts is the number of preemption attempts.
	preemptionAttempts atomic.Int64

	// preemptionSuccesses is the number of successful preemptions.
	preemptionSuccesses atomic.Int64

	// pendingSandboxes is the number of sandboxes in the scheduling queue.
	pendingSandboxes atomic.Int64

	// activeNodes is the number of active nodes.
	activeNodes atomic.Int64

	// totalSchedulingLatency is the cumulative scheduling latency in nanoseconds.
	totalSchedulingLatency atomic.Int64

	// totalBindingLatency is the cumulative binding latency in nanoseconds.
	totalBindingLatency atomic.Int64

	// schedulingLatencySamples holds latency samples for percentile calculation.
	schedulingLatencySamples []time.Duration

	// bindingLatencySamples holds binding latency samples for percentile calculation.
	bindingLatencySamples []time.Duration

	// batchSchedulingCount is the number of batch scheduling operations.
	batchSchedulingCount atomic.Int64

	// batchSchedulingSuccess is the number of successful batch schedules.
	batchSchedulingSuccess atomic.Int64

	// lastSchedulingTimestamp is the timestamp of the last scheduling attempt.
	lastSchedulingTimestamp atomic.Int64

	// cacheSize is the current size of the scheduler cache.
	cacheSize atomic.Int64
}

// NewSchedulerMetrics creates new scheduler metrics.
func NewSchedulerMetrics() *SchedulerMetrics {
	return &SchedulerMetrics{
		schedulingLatencySamples: make([]time.Duration, 0, 1000),
		bindingLatencySamples:    make([]time.Duration, 0, 1000),
	}
}

// RecordSchedulingAttempt records a scheduling attempt.
func (m *SchedulerMetrics) RecordSchedulingAttempt(result string, latency time.Duration) {
	m.schedulingAttempts.Add(1)
	m.lastSchedulingTimestamp.Store(time.Now().UnixNano())

	switch result {
	case "success":
		m.schedulingSuccesses.Add(1)
	case "failure":
		m.schedulingFailures.Add(1)
	case "unschedulable":
		m.schedulingUnschedulable.Add(1)
	}

	m.totalSchedulingLatency.Add(int64(latency))
	m.schedulingLatencySamples = append(m.schedulingLatencySamples, latency)
	if len(m.schedulingLatencySamples) > 1000 {
		m.schedulingLatencySamples = m.schedulingLatencySamples[1:]
	}
}

// RecordBindingLatency records a binding latency.
func (m *SchedulerMetrics) RecordBindingLatency(latency time.Duration) {
	m.totalBindingLatency.Add(int64(latency))
	m.bindingLatencySamples = append(m.bindingLatencySamples, latency)
	if len(m.bindingLatencySamples) > 1000 {
		m.bindingLatencySamples = m.bindingLatencySamples[1:]
	}
}

// RecordPreemptionAttempt records a preemption attempt.
func (m *SchedulerMetrics) RecordPreemptionAttempt(success bool) {
	m.preemptionAttempts.Add(1)
	if success {
		m.preemptionSuccesses.Add(1)
	}
}

// RecordBatchScheduling records a batch scheduling operation.
func (m *SchedulerMetrics) RecordBatchScheduling(success bool) {
	m.batchSchedulingCount.Add(1)
	if success {
		m.batchSchedulingSuccess.Add(1)
	}
}

// SetPendingSandboxes sets the number of pending sandboxes.
func (m *SchedulerMetrics) SetPendingSandboxes(count int64) {
	m.pendingSandboxes.Store(count)
}

// SetActiveNodes sets the number of active nodes.
func (m *SchedulerMetrics) SetActiveNodes(count int64) {
	m.activeNodes.Store(count)
}

// SetCacheSize sets the cache size.
func (m *SchedulerMetrics) SetCacheSize(size int64) {
	m.cacheSize.Store(size)
}

// GetMetrics returns a snapshot of the current metrics.
func (m *SchedulerMetrics) GetMetrics() map[string]interface{} {
	attempts := m.schedulingAttempts.Load()
	successes := m.schedulingSuccesses.Load()

	var avgLatencyMs float64
	if attempts > 0 {
		avgLatencyMs = float64(m.totalSchedulingLatency.Load()) / float64(attempts) / float64(time.Millisecond)
	}

	var avgBindingMs float64
	bindings := m.schedulingSuccesses.Load()
	if bindings > 0 {
		avgBindingMs = float64(m.totalBindingLatency.Load()) / float64(bindings) / float64(time.Millisecond)
	}

	var successRate float64
	if attempts > 0 {
		successRate = float64(successes) / float64(attempts) * 100
	}

	return map[string]interface{}{
		"totalSchedulingAttempts":  attempts,
		"schedulingSuccesses":      successes,
		"schedulingFailures":       m.schedulingFailures.Load(),
		"schedulingUnschedulable":  m.schedulingUnschedulable.Load(),
		"schedulingSuccessRate":    successRate,
		"avgSchedulingLatencyMs":   avgLatencyMs,
		"avgBindingLatencyMs":      avgBindingMs,
		"preemptionAttempts":       m.preemptionAttempts.Load(),
		"preemptionSuccesses":      m.preemptionSuccesses.Load(),
		"pendingSandboxes":         m.pendingSandboxes.Load(),
		"activeNodes":              m.activeNodes.Load(),
		"batchSchedulingCount":     m.batchSchedulingCount.Load(),
		"batchSchedulingSuccess":   m.batchSchedulingSuccess.Load(),
		"cacheSize":                m.cacheSize.Load(),
		"p50SchedulingLatencyMs":   m.percentile(50),
		"p90SchedulingLatencyMs":   m.percentile(90),
		"p99SchedulingLatencyMs":   m.percentile(99),
	}
}

// percentile calculates the given percentile of scheduling latency.
func (m *SchedulerMetrics) percentile(p int) float64 {
	samples := m.schedulingLatencySamples
	n := len(samples)
	if n == 0 {
		return 0
	}

	// Simple percentile calculation
	index := (p * n) / 100
	if index >= n {
		index = n - 1
	}

	return float64(samples[index]) / float64(time.Millisecond)
}

// Reset resets all metrics.
func (m *SchedulerMetrics) Reset() {
	m.schedulingAttempts.Store(0)
	m.schedulingSuccesses.Store(0)
	m.schedulingFailures.Store(0)
	m.schedulingUnschedulable.Store(0)
	m.preemptionAttempts.Store(0)
	m.preemptionSuccesses.Store(0)
	m.pendingSandboxes.Store(0)
	m.activeNodes.Store(0)
	m.totalSchedulingLatency.Store(0)
	m.totalBindingLatency.Store(0)
	m.batchSchedulingCount.Store(0)
	m.batchSchedulingSuccess.Store(0)
	m.lastSchedulingTimestamp.Store(0)
	m.cacheSize.Store(0)
	m.schedulingLatencySamples = make([]time.Duration, 0, 1000)
	m.bindingLatencySamples = make([]time.Duration, 0, 1000)
}
