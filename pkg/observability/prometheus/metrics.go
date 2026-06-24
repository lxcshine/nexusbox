/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	namespace = "nexusbox"
	subsystem = "sandbox"
)

var (
	// SandboxCreationTotal counts the total number of sandbox creation attempts.
	SandboxCreationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "creation_total",
		Help:      "Total number of sandbox creation attempts",
	}, []string{"tenant", "runtime", "result"})

	// SandboxCreationDuration tracks the duration of sandbox creation.
	SandboxCreationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "creation_duration_seconds",
		Help:      "Duration of sandbox creation in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.1, 2, 15),
	}, []string{"tenant", "runtime"})

	// SandboxRunning tracks the number of currently running sandboxes.
	SandboxRunning = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "running",
		Help:      "Number of currently running sandboxes",
	}, []string{"tenant", "runtime", "node"})

	// SandboxSchedulingAttempts tracks scheduling attempts.
	SchedulingAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "scheduling_attempts_total",
		Help:      "Total number of scheduling attempts",
	}, []string{"result"})

	// SchedulingDuration tracks scheduling latency.
	SchedulingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "scheduling_duration_seconds",
		Help:      "Duration of scheduling in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15),
	}, []string{"result"})

	// SandboxEvictions tracks sandbox evictions.
	Evictions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "evictions_total",
		Help:      "Total number of sandbox evictions",
	}, []string{"tenant", "node", "reason"})

	// NodeActive tracks active nodes.
	NodeActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "node",
		Name:      "active",
		Help:      "Number of active nodes",
	})

	// NodeSandboxCount tracks sandboxes per node.
	NodeSandboxCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "node",
		Name:      "sandbox_count",
		Help:      "Number of sandboxes per node",
	}, []string{"node"})

	// TenantResourceUsage tracks tenant resource usage.
	TenantCPUUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "tenant",
		Name:      "cpu_usage_millicores",
		Help:      "CPU usage per tenant in millicores",
	}, []string{"tenant"})

	TenantMemoryUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "tenant",
		Name:      "memory_usage_bytes",
		Help:      "Memory usage per tenant in bytes",
	}, []string{"tenant"})

	TenantSandboxCount = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "tenant",
		Name:      "sandbox_count",
		Help:      "Number of sandboxes per tenant",
	}, []string{"tenant"})

	// QueueLength tracks scheduling queue length.
	QueueLength = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "scheduler",
		Name:      "queue_length",
		Help:      "Number of sandboxes in the scheduling queue",
	})

	QueueUnschedulableLength = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "scheduler",
		Name:      "queue_unschedulable_length",
		Help:      "Number of unschedulable sandboxes in the queue",
	})

	// RuntimeOperations tracks runtime operations.
	RuntimeOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "runtime",
		Name:      "operations_total",
		Help:      "Total number of runtime operations",
	}, []string{"operation", "runtime", "result"})

	RuntimeOperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "runtime",
		Name:      "operation_duration_seconds",
		Help:      "Duration of runtime operations in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.01, 2, 15),
	}, []string{"operation", "runtime"})

	// APIRequests tracks API server requests.
	APIRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "api",
		Name:      "requests_total",
		Help:      "Total number of API requests",
	}, []string{"method", "path", "status"})

	APIRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "api",
		Name:      "request_duration_seconds",
		Help:      "Duration of API requests in seconds",
		Buckets:   prometheus.ExponentialBuckets(0.001, 2, 15),
	}, []string{"method", "path"})
)
