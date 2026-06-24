package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// MetricsCollector collects and exposes metrics for the sandbox system.
// It provides a comprehensive view of system health, performance,
// and resource utilization for observability.
type MetricsCollector struct {
	mu sync.RWMutex

	// counters holds counter metrics.
	counters map[string]*Counter

	// gauges holds gauge metrics.
	gauges map[string]*Gauge

	// histograms holds histogram metrics.
	histograms map[string]*Histogram

	// summaries holds summary metrics.
	summaries map[string]*Summary

	// eventRecorder records events for analysis.
	eventRecorder EventMetricsRecorder

	// costTracker tracks costs for billing and optimization.
	costTracker *CostTracker

	// config holds metrics configuration.
	config *MetricsConfig
}

// MetricsConfig holds configuration for metrics collection.
type MetricsConfig struct {
	// Enabled indicates whether metrics collection is enabled.
	Enabled bool
	// CollectionInterval is how often to collect metrics.
	CollectionInterval time.Duration
	// RetentionPeriod is how long to retain metrics data.
	RetentionPeriod time.Duration
	// ExportInterval is how often to export metrics.
	ExportInterval time.Duration
}

// DefaultMetricsConfig returns default metrics configuration.
func DefaultMetricsConfig() *MetricsConfig {
	return &MetricsConfig{
		Enabled:            true,
		CollectionInterval: 15 * time.Second,
		RetentionPeriod:    24 * time.Hour,
		ExportInterval:     60 * time.Second,
	}
}

// Counter is a monotonically increasing value.
type Counter struct {
	mu       sync.RWMutex
	name     string
	help     string
	labels   map[string]string
	value    float64
	created  time.Time
	modified time.Time
}

// NewCounter creates a new Counter.
func NewCounter(name, help string) *Counter {
	return &Counter{
		name:     name,
		help:     help,
		labels:   make(map[string]string),
		value:    0,
		created:  time.Now(),
		modified: time.Now(),
	}
}

// Inc increments the counter by 1.
func (c *Counter) Inc() {
	c.Add(1)
}

// Add adds a value to the counter.
func (c *Counter) Add(value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if value < 0 {
		klog.Warningf("Counter %s received negative value %f", c.name, value)
		return
	}

	c.value += value
	c.modified = time.Now()
}

// Value returns the current value of the counter.
func (c *Counter) Value() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.value
}

// Reset resets the counter to zero.
func (c *Counter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.value = 0
	c.modified = time.Now()
}

// Gauge is a metric that can go up or down.
type Gauge struct {
	mu       sync.RWMutex
	name     string
	help     string
	labels   map[string]string
	value    float64
	created  time.Time
	modified time.Time
}

// NewGauge creates a new Gauge.
func NewGauge(name, help string) *Gauge {
	return &Gauge{
		name:     name,
		help:     help,
		labels:   make(map[string]string),
		value:    0,
		created:  time.Now(),
		modified: time.Now(),
	}
}

// Set sets the gauge value.
func (g *Gauge) Set(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.value = value
	g.modified = time.Now()
}

// Inc increments the gauge by 1.
func (g *Gauge) Inc() {
	g.Add(1)
}

// Dec decrements the gauge by 1.
func (g *Gauge) Dec() {
	g.Sub(1)
}

// Add adds a value to the gauge.
func (g *Gauge) Add(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.value += value
	g.modified = time.Now()
}

// Sub subtracts a value from the gauge.
func (g *Gauge) Sub(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.value -= value
	g.modified = time.Now()
}

// Value returns the current value of the gauge.
func (g *Gauge) Value() float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.value
}

// Histogram tracks the distribution of values.
type Histogram struct {
	mu         sync.RWMutex
	name       string
	help       string
	buckets    []float64
	bucketCounts []uint64
	sum        float64
	count      uint64
	created    time.Time
	modified   time.Time
}

// NewHistogram creates a new Histogram with default buckets.
func NewHistogram(name, help string) *Histogram {
	return &Histogram{
		name:    name,
		help:    help,
		buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		bucketCounts: make([]uint64, 12), // len(buckets) + 1 for +Inf
		sum:      0,
		count:    0,
		created:  time.Now(),
		modified: time.Now(),
	}
}

// Observe records a value in the histogram.
func (h *Histogram) Observe(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sum += value
	h.count++

	// Find the right bucket
	for i, bucket := range h.buckets {
		if value <= bucket {
			h.bucketCounts[i]++
			return
		}
	}
	// Value exceeds all buckets, put in +Inf bucket
	h.bucketCounts[len(h.buckets)]++
}

// Summary tracks quantiles of observed values.
type Summary struct {
	mu          sync.RWMutex
	name        string
	help        string
	objectives  map[float64]float64 // quantile -> max error
	values      []float64
	count       uint64
	sum         float64
	created     time.Time
	modified    time.Time
	maxAge      time.Duration
	ageBuckets  int
}

// NewSummary creates a new Summary with default objectives.
func NewSummary(name, help string) *Summary {
	return &Summary{
		name:       name,
		help:       help,
		objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
		values:     make([]float64, 0),
		count:      0,
		sum:        0,
		created:    time.Now(),
		modified:   time.Now(),
		maxAge:     10 * time.Minute,
		ageBuckets: 5,
	}
}

// Observe records a value in the summary.
func (s *Summary) Observe(value float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.values = append(s.values, value)
	s.count++
	s.sum += value
	s.modified = time.Now()
}

// NewMetricsCollector creates a new MetricsCollector.
func NewMetricsCollector(config *MetricsConfig) *MetricsCollector {
	if config == nil {
		config = DefaultMetricsConfig()
	}

	mc := &MetricsCollector{
		counters:   make(map[string]*Counter),
		gauges:     make(map[string]*Gauge),
		histograms: make(map[string]*Histogram),
		summaries:  make(map[string]*Summary),
		config:     config,
	}

	mc.initSystemMetrics()

	return mc
}

// initSystemMetrics initializes default system metrics.
func (mc *MetricsCollector) initSystemMetrics() {
	// Sandbox lifecycle metrics
	mc.RegisterCounter("sandbox_created_total", "Total number of sandboxes created")
	mc.RegisterCounter("sandbox_deleted_total", "Total number of sandboxes deleted")
	mc.RegisterCounter("sandbox_scheduled_total", "Total number of sandboxes scheduled")
	mc.RegisterCounter("sandbox_failed_total", "Total number of sandboxes failed")
	mc.RegisterCounter("sandbox_evicted_total", "Total number of sandboxes evicted")

	// Scheduling metrics
	mc.RegisterHistogram("scheduler_scheduling_duration_seconds", "Scheduling duration in seconds")
	mc.RegisterHistogram("scheduler_binding_duration_seconds", "Binding duration in seconds")
	mc.RegisterGauge("scheduler_pending_sandboxes", "Number of pending sandboxes")
	mc.RegisterGauge("scheduler_active_nodes", "Number of active nodes")

	// Runtime metrics
	mc.RegisterGauge("runtime_active_sandboxes", "Number of active runtimes")
	mc.RegisterGauge("runtime_pool_size", "Size of runtime pool")

	// Tenant metrics
	mc.RegisterGauge("tenant_count", "Number of active tenants")
	mc.RegisterGauge("tenant_quota_usage_percent", "Tenant quota usage percentage")

	// Agent metrics
	mc.RegisterGauge("agent_node_count", "Number of connected agents")
	mc.RegisterCounter("agent_heartbeat_success_total", "Total successful heartbeats")
	mc.RegisterCounter("agent_heartbeat_failure_total", "Total failed heartbeats")

	// Resource metrics
	mc.RegisterGauge("node_cpu_capacity_millicores", "Node CPU capacity in millicores")
	mc.RegisterGauge("node_memory_capacity_bytes", "Node memory capacity in bytes")
	mc.RegisterGauge("node_cpu_used_millicores", "Used CPU in millicores")
	mc.RegisterGauge("node_memory_used_bytes", "Used memory in bytes")

	// Error metrics
	mc.RegisterCounter("errors_total", "Total number of errors")
}

// RegisterCounter registers a new counter metric.
func (mc *MetricsCollector) RegisterCounter(name, help string) *Counter {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	counter := NewCounter(name, help)
	mc.counters[name] = counter
	return counter
}

// RegisterGauge registers a new gauge metric.
func (mc *MetricsCollector) RegisterGauge(name, help string) *Gauge {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	gauge := NewGauge(name, help)
	mc.gauges[name] = gauge
	return gauge
}

// RegisterHistogram registers a new histogram metric.
func (mc *MetricsCollector) RegisterHistogram(name, help string) *Histogram {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	histogram := NewHistogram(name, help)
	mc.histograms[name] = histogram
	return histogram
}

// RegisterSummary registers a new summary metric.
func (mc *MetricsCollector) RegisterSummary(name, help string) *Summary {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	summary := NewSummary(name, help)
	mc.summaries[name] = summary
	return summary
}

// GetCounter gets a counter by name.
func (mc *MetricsCollector) GetCounter(name string) (*Counter, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	counter, exists := mc.counters[name]
	return counter, exists
}

// GetGauge gets a gauge by name.
func (mc *MetricsCollector) GetGauge(name string) (*Gauge, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	gauge, exists := mc.gauges[name]
	return gauge, exists
}

// GetHistogram gets a histogram by name.
func (mc *MetricsCollector) GetHistogram(name string) (*Histogram, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	histogram, exists := mc.histograms[name]
	return histogram, exists
}

// GetSummary gets a summary by name.
func (mc *MetricsCollector) GetSummary(name string) (*Summary, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	summary, exists := mc.summaries[name]
	return summary, exists
}

// RecordSandboxCreated records that a sandbox was created.
func (mc *MetricsCollector) RecordSandboxCreated(tenantName string) {
	if counter, ok := mc.GetCounter("sandbox_created_total"); ok {
		counter.Inc()
	}
}

// RecordSandboxDeleted records that a sandbox was deleted.
func (mc *MetricsCollector) RecordSandboxDeleted(tenantName string) {
	if counter, ok := mc.GetCounter("sandbox_deleted_total"); ok {
		counter.Inc()
	}
}

// RecordSandboxScheduled records that a sandbox was scheduled.
func (mc *MetricsCollector) RecordSandboxScheduled(duration time.Duration) {
	if counter, ok := mc.GetCounter("sandbox_scheduled_total"); ok {
		counter.Inc()
	}
	if hist, ok := mc.GetHistogram("scheduler_scheduling_duration_seconds"); ok {
		hist.Observe(duration.Seconds())
	}
}

// RecordSandboxFailed records that a sandbox failed.
func (mc *MetricsCollector) RecordSandboxFailed(reason string) {
	if counter, ok := mc.GetCounter("sandbox_failed_total"); ok {
		counter.Inc()
	}
	if counter, ok := mc.GetCounter("errors_total"); ok {
		counter.Inc()
	}
}

// UpdatePendingSandboxes updates the pending sandbox count.
func (mc *MetricsCollector) UpdatePendingSandboxes(count int) {
	if gauge, ok := mc.GetGauge("scheduler_pending_sandboxes"); ok {
		gauge.Set(float64(count))
	}
}

// UpdateActiveNodes updates the active node count.
func (mc *MetricsCollector) UpdateActiveNodes(count int) {
	if gauge, ok := mc.GetGauge("scheduler_active_nodes"); ok {
		gauge.Set(float64(count))
	}
}

// UpdateActiveRuntimes updates the active runtime count.
func (mc *MetricsCollector) UpdateActiveRuntimes(count int) {
	if gauge, ok := mc.GetGauge("runtime_active_sandboxes"); ok {
		gauge.Set(float64(count))
	}
}

// UpdatePoolSize updates the pool size.
func (mc *MetricsCollector) UpdatePoolSize(size int) {
	if gauge, ok := mc.GetGauge("runtime_pool_size"); ok {
		gauge.Set(float64(size))
	}
}

// RecordHeartbeatSuccess records a successful heartbeat.
func (mc *MetricsCollector) RecordHeartbeatSuccess(nodeName string) {
	if counter, ok := mc.GetCounter("agent_heartbeat_success_total"); ok {
		counter.Inc()
	}
}

// RecordHeartbeatFailure records a failed heartbeat.
func (mc *MetricsCollector) RecordHeartbeatFailure(nodeName string) {
	if counter, ok := mc.GetCounter("agent_heartbeat_failure_total"); ok {
		counter.Inc()
	}
}

// GetAllMetrics returns all collected metrics as a map.
func (mc *MetricsCollector) GetAllMetrics() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make(map[string]interface{})

	for name, counter := range mc.counters {
		result["counter_"+name] = counter.Value()
	}

	for name, gauge := range mc.gauges {
		result["gauge_"+name] = gauge.Value()
	}

	for name, histogram := range mc.histograms {
		result["histogram_"+name] = map[string]interface{}{
			"count":  histogram.count,
			"sum":   histogram.sum,
			"buckets": histogram.bucketCounts,
		}
	}

	for name, summary := range mc.summaries {
		result["summary_"+name] = map[string]interface{}{
			"count": summary.count,
			"sum":   summary.sum,
		}
	}

	return result
}

// Start starts the metrics collector.
func (mc *MetricsCollector) Start(ctx context.Context) {
	if !mc.config.Enabled {
		return
	}

	klog.Info("Starting metrics collector")

	// Initialize cost tracker
	mc.costTracker = NewCostTracker(mc)

	klog.Info("Metrics collector started")
}

// Stop stops the metrics collector.
func (mc *MetricsCollector) Stop() {
	klog.Info("Stopping metrics collector")
}

// EventMetricsRecorder records events for metrics analysis.
type EventMetricsRecorder interface {
	RecordEvent(eventType, reason, message string, labels map[string]string)
	GetEvents(eventType string) []*EventRecord
}

// EventRecord represents a recorded event.
type EventRecord struct {
	Timestamp time.Time
	Type      string
	Reason    string
	Message   string
	Labels    map[string]string
}

// CostTracker tracks costs for billing and resource optimization.
type CostTracker struct {
	mu sync.RWMutex

	// collector is the parent metrics collector.
	collector *MetricsCollector

	// tenantCosts maps tenant name to its cost information.
	tenantCosts map[string]*TenantCostInfo

	// pricing holds cost per unit of resources.
	pricing *ResourcePricing
}

// TenantCostInfo holds cost information for a tenant.
type TenantCostInfo struct {
	TenantName           string
	TotalCost            float64
	CPUCost              float64
	MemoryCost           float64
	GPUCost              float64
	StorageCost          float64
	SandboxCount         int
	AvgSandboxDuration   time.Duration
	BillingPeriodStart   time.Time
	BillingPeriodEnd     time.Time
}

// ResourcePricing holds pricing information for resources.
type ResourcePricing struct {
	CPUHourlyPrice       float64 // Price per CPU core hour
	MemoryGBHourlyPrice  float64 // Price per GB memory hour
	GPUHourlyPrice       float64 // Price per GPU hour
	StorageGBMonthlyPrice float64 // Price per GB storage month
	SandboxBasePrice     float64 // Base price per sandbox
}

// DefaultResourcePricing returns default resource pricing.
func DefaultResourcePricing() *ResourcePricing {
	return &ResourcePricing{
		CPUHourlyPrice:       0.02,
		MemoryGBHourlyPrice:  0.004,
		GPUHourlyPrice:       0.50,
		StorageGBMonthlyPrice: 0.10,
		SandboxBasePrice:     0.001,
	}
}

// NewCostTracker creates a new CostTracker.
func NewCostTracker(collector *MetricsCollector) *CostTracker {
	ct := &CostTracker{
		collector:   collector,
		tenantCosts: make(map[string]*TenantCostInfo),
		pricing:     DefaultResourcePricing(),
	}

	return ct
}

// TrackUsage tracks resource usage for a tenant.
func (ct *CostTracker) TrackUsage(tenantName string, cpuHours, memoryGBHours, gpuHours float64, storageGB float64) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	cost, exists := ct.tenantCosts[tenantName]
	if !exists {
		cost = &TenantCostInfo{
			TenantName:         tenantName,
			BillingPeriodStart: time.Now(),
			BillingPeriodEnd:   time.Now().AddDate(0, 1, 0),
		}
		ct.tenantCosts[tenantName] = cost
	}

	// Calculate costs
	cost.CPUCost += cpuHours * ct.pricing.CPUHourlyPrice
	cost.MemoryCost += memoryGBHours * ct.pricing.MemoryGBHourlyPrice
	cost.GPUCost += gpuHours * ct.pricing.GPUHourlyPrice
	cost.StorageCost += storageGB * ct.pricing.StorageGBMonthlyPrice / (30 * 24)
	cost.TotalCost = cost.CPUCost + cost.MemoryCost + cost.GPUCost + cost.StorageCost
}

// GetTenantCost returns cost information for a tenant.
func (ct *CostTracker) GetTenantCost(tenantName string) (*TenantCostInfo, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	cost, exists := ct.tenantCosts[tenantName]
	if !exists {
		return nil, false
	}

	copy := *cost
	return &copy, true
}

// GetAllTenantCosts returns cost information for all tenants.
func (ct *CostTracker) GetAllTenantCosts() map[string]*TenantCostInfo {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	result := make(map[string]*TenantCostInfo, len(ct.tenantCosts))
	for key, cost := range ct.tenantCosts {
		copy := *cost
		result[key] = &copy
	}
	return result
}

// GenerateBillingReport generates a billing report for a tenant.
func (ct *CostTracker) GenerateBillingReport(tenantName string) *BillingReport {
	cost, exists := ct.GetTenantCost(tenantName)
	if !exists {
		return nil
	}

	report := &BillingReport{
		TenantName:         tenantName,
		PeriodStart:        cost.BillingPeriodStart,
		PeriodEnd:          cost.BillingPeriodEnd,
		TotalCost:          cost.TotalCost,
		ResourceBreakdown: ResourceBreakdown{
			CPU:    cost.CPUCost,
			Memory: cost.MemoryCost,
			GPU:    cost.GPUCost,
			Storage: cost.StorageCost,
		},
		SandboxCount:       cost.SandboxCount,
	}

	return report
}

// BillingReport represents a billing report.
type BillingReport struct {
	TenantName         string
	PeriodStart        time.Time
	PeriodEnd          time.Time
	TotalCost          float64
	ResourceBreakdown  ResourceBreakdown
	SandboxCount       int
}

// ResourceBreakdown breaks down costs by resource type.
type ResourceBreakdown struct {
	CPU     float64
	Memory  float64
	GPU     float64
	Storage float64
}

// String returns a string representation of the billing report.
func (r *BillingReport) String() string {
	return fmt.Sprintf(`Billing Report for %s:
  Period: %s - %s
  Total Cost: $%.2f
  CPU: $%.2f
  Memory: $%.2f
  GPU: $%.2f
  Storage: $%.2f
  Sandboxes: %d`,
		r.TenantName,
		r.PeriodStart.Format(time.RFC3339),
		r.PeriodEnd.Format(time.RFC3339),
		r.TotalCost,
		r.ResourceBreakdown.CPU,
		r.ResourceBreakdown.Memory,
		r.ResourceBreakdown.GPU,
		r.ResourceBreakdown.Storage,
		r.SandboxCount,
	)
}
