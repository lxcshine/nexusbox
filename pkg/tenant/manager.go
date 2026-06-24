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
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

// TenantManager manages tenant lifecycle and provides tenant-related operations.
// It is responsible for tenant registration, validation, resource tracking,
// and enforcing multi-tenant isolation policies.
type TenantManager struct {
	// mu protects the internal state of the TenantManager.
	mu sync.RWMutex

	// tenants stores tenant information indexed by tenant name.
	tenants map[string]*TenantInfo

	// quotaManager manages resource quotas for tenants.
	quotaManager *quota.QuotaManager

	// isolationEnforcer enforces tenant isolation policies.
	isolationEnforcer *IsolationEnforcer

	// rateLimiter tracks rate limits per tenant.
	rateLimiter *RateLimiter

	// informer watches for tenant CRD changes.
	informer cache.SharedIndexInformer

	// eventRecorder records tenant-related events.
	eventRecorder EventRecorder

	// costTracker tracks cost per tenant.
	costTracker *CostTracker

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// TenantInfo holds runtime information about a tenant.
type TenantInfo struct {
	// Name is the tenant identifier.
	Name string
	// Spec is the tenant specification.
	Spec sandboxv1alpha1.TenantSpec
	// Status is the current tenant status.
	Status sandboxv1alpha1.TenantStatus
	// ResourceUsage tracks real-time resource usage.
	ResourceUsage *TenantResourceUsage
	// LastUpdated is the last time this info was updated.
	LastUpdated time.Time
	// SandboxCount tracks the number of sandboxes per node.
	SandboxCount map[string]int32
	// DailySandboxCount tracks sandboxes created today.
	DailySandboxCount int32
	// DailyCountResetTime tracks when the daily count was last reset.
	DailyCountResetTime time.Time
}

// TenantResourceUsage tracks real-time resource usage for a tenant.
type TenantResourceUsage struct {
	// CPUUsed is the currently used CPU in millicores.
	CPUUsed int64
	// MemoryUsedBytes is the currently used memory in bytes.
	MemoryUsedBytes int64
	// GPUUsed is the currently used GPU count.
	GPUUsed int64
	// StorageUsedBytes is the currently used storage in bytes.
	StorageUsedBytes int64
	// ActiveSandboxCount is the number of active sandboxes.
	ActiveSandboxCount int32
	// TotalSandboxCount is the total number of sandboxes (including non-active).
	TotalSandboxCount int64
}

// EventRecorder records events for tenant operations.
type EventRecorder interface {
	RecordEvent(tenantName, eventType, reason, message string)
}

// CostTracker tracks cost information for tenants.
type CostTracker struct {
	mu sync.RWMutex
	// costPerTenant tracks accumulated cost per tenant.
	costPerTenant map[string]*TenantCostRecord
	// pricingModel defines the pricing model.
	pricingModel *PricingModel
}

// TenantCostRecord holds cost information for a single tenant.
type TenantCostRecord struct {
	// TenantName is the tenant identifier.
	TenantName string
	// AccumulatedCost is the total accumulated cost.
	AccumulatedCost float64
	// DailyCost is the cost for the current day.
	DailyCost float64
	// MonthlyCost is the cost for the current month.
	MonthlyCost float64
	// CostByResource breaks down cost by resource type.
	CostByResource map[string]float64
	// CostByNode breaks down cost by node.
	CostByNode map[string]float64
	// LastUpdated is the last time the cost was updated.
	LastUpdated time.Time
}

// PricingModel defines the pricing model for sandbox resources.
type PricingModel struct {
	// CPUPricePerCorePerHour is the price per CPU core per hour.
	CPUPricePerCorePerHour float64
	// MemoryPricePerGBPerHour is the price per GB of memory per hour.
	MemoryPricePerGBPerHour float64
	// GPUPricePerUnitPerHour is the price per GPU unit per hour.
	GPUPricePerUnitPerHour float64
	// StoragePricePerGBPerHour is the price per GB of storage per hour.
	StoragePricePerGBPerHour float64
	// NetworkPricePerGB is the price per GB of network transfer.
	NetworkPricePerGB float64
	// KataSurchargePercent is the additional cost percentage for Kata Containers.
	KataSurchargePercent float64
}

// NewTenantManager creates a new TenantManager.
func NewTenantManager(quotaManager *quota.QuotaManager, informer cache.SharedIndexInformer, eventRecorder EventRecorder) *TenantManager {
	pricingModel := &PricingModel{
		CPUPricePerCorePerHour:   0.05,
		MemoryPricePerGBPerHour:  0.01,
		GPUPricePerUnitPerHour:   0.50,
		StoragePricePerGBPerHour: 0.001,
		NetworkPricePerGB:        0.09,
		KataSurchargePercent:     20.0,
	}

	tm := &TenantManager{
		tenants:           make(map[string]*TenantInfo),
		quotaManager:      quotaManager,
		isolationEnforcer: NewIsolationEnforcer(),
		rateLimiter:       NewRateLimiter(),
		informer:          informer,
		eventRecorder:     eventRecorder,
		costTracker: &CostTracker{
			costPerTenant: make(map[string]*TenantCostRecord),
			pricingModel:  pricingModel,
		},
		stopCh: make(chan struct{}),
	}

	if informer != nil {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    tm.onTenantAdd,
			UpdateFunc: tm.onTenantUpdate,
			DeleteFunc: tm.onTenantDelete,
		})
	}

	return tm
}

// Start starts the tenant manager's background goroutines.
func (tm *TenantManager) Start(ctx context.Context) {
	klog.Info("Starting tenant manager")

	// Start daily counter reset goroutine
	go wait.Until(tm.resetDailyCounters, 24*time.Hour, tm.stopCh)

	// Start cost calculation goroutine
	go wait.Until(tm.calculateCosts, 1*time.Minute, tm.stopCh)

	// Start rate limiter cleanup goroutine
	go wait.Until(tm.rateLimiter.Cleanup, 1*time.Minute, tm.stopCh)

	klog.Info("Tenant manager started")
}

// Stop stops the tenant manager.
func (tm *TenantManager) Stop() {
	klog.Info("Stopping tenant manager")
	close(tm.stopCh)
}

// RegisterTenant registers a new tenant or updates an existing one.
func (tm *TenantManager) RegisterTenant(ctx context.Context, tenant *sandboxv1alpha1.Tenant) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	name := tenant.Name
	klog.Infof("Registering tenant %s", name)

	// Validate tenant spec
	if err := tm.validateTenantSpec(tenant.Spec); err != nil {
		return fmt.Errorf("invalid tenant spec for %s: %w", name, err)
	}

	// Check if tenant already exists
	info, exists := tm.tenants[name]
	if !exists {
		info = &TenantInfo{
			Name:                name,
			ResourceUsage:       &TenantResourceUsage{},
			SandboxCount:        make(map[string]int32),
			DailyCountResetTime: time.Now(),
		}
		tm.tenants[name] = info
	}

	info.Spec = tenant.Spec
	status := tenant.Status
	// Auto-activate tenants that don't have an explicit phase set
	if status.Phase == "" {
		status.Phase = sandboxv1alpha1.TenantActive
	}
	info.Status = status
	info.LastUpdated = time.Now()

	// Register quota with quota manager
	if err := tm.quotaManager.RegisterQuota(name, &tenant.Spec.ResourceQuota); err != nil {
		return fmt.Errorf("failed to register quota for tenant %s: %w", name, err)
	}

	// Register rate limits
	if tenant.Spec.RateLimit != nil {
		tm.rateLimiter.RegisterTenant(name, tenant.Spec.RateLimit)
	}

	// Initialize cost tracking
	tm.costTracker.mu.Lock()
	if _, exists := tm.costTracker.costPerTenant[name]; !exists {
		tm.costTracker.costPerTenant[name] = &TenantCostRecord{
			TenantName:     name,
			CostByResource: make(map[string]float64),
			CostByNode:     make(map[string]float64),
			LastUpdated:    time.Now(),
		}
	}
	tm.costTracker.mu.Unlock()

	klog.Infof("Tenant %s registered successfully", name)
	if tm.eventRecorder != nil {
		tm.eventRecorder.RecordEvent(name, "Normal", "TenantRegistered", fmt.Sprintf("Tenant %s registered", name))
	}

	return nil
}

// UnregisterTenant removes a tenant from the manager.
func (tm *TenantManager) UnregisterTenant(ctx context.Context, tenantName string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	klog.Infof("Unregistering tenant %s", tenantName)

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	// Check for active sandboxes
	if info.ResourceUsage != nil && info.ResourceUsage.ActiveSandboxCount > 0 {
		return fmt.Errorf("cannot unregister tenant %s: %d active sandboxes exist",
			tenantName, info.ResourceUsage.ActiveSandboxCount)
	}

	// Remove from quota manager
	tm.quotaManager.UnregisterQuota(tenantName)

	// Remove from rate limiter
	tm.rateLimiter.UnregisterTenant(tenantName)

	// Remove cost tracking
	tm.costTracker.mu.Lock()
	delete(tm.costTracker.costPerTenant, tenantName)
	tm.costTracker.mu.Unlock()

	// Remove tenant
	delete(tm.tenants, tenantName)

	klog.Infof("Tenant %s unregistered successfully", tenantName)
	if tm.eventRecorder != nil {
		tm.eventRecorder.RecordEvent(tenantName, "Normal", "TenantUnregistered", fmt.Sprintf("Tenant %s unregistered", tenantName))
	}

	return nil
}

// GetTenant retrieves tenant information.
func (tm *TenantManager) GetTenant(tenantName string) (*TenantInfo, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return nil, false
	}
	return info, true
}

// ListTenants returns all registered tenants.
func (tm *TenantManager) ListTenants() []*TenantInfo {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]*TenantInfo, 0, len(tm.tenants))
	for _, info := range tm.tenants {
		result = append(result, info)
	}
	return result
}

// CanCreateSandbox checks if a tenant can create a new sandbox.
func (tm *TenantManager) CanCreateSandbox(ctx context.Context, tenantName string, resources *sandboxv1alpha1.ResourceRequirements) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	// Check tenant phase
	if !info.Status.Phase.IsAvailable() {
		return fmt.Errorf("tenant %s is not active (phase: %s)", tenantName, info.Status.Phase)
	}

	// Check rate limits
	if !tm.rateLimiter.Allow(tenantName, "CreateSandbox") {
		return fmt.Errorf("tenant %s exceeded rate limit for sandbox creation", tenantName)
	}

	// Check daily sandbox limit
	if info.Spec.MaxSandboxesPerDay > 0 && info.DailySandboxCount >= info.Spec.MaxSandboxesPerDay {
		return fmt.Errorf("tenant %s exceeded daily sandbox limit (%d/%d)",
			tenantName, info.DailySandboxCount, info.Spec.MaxSandboxesPerDay)
	}

	// Check concurrent sandbox limit
	if info.Spec.MaxConcurrentSandboxes > 0 && info.ResourceUsage.ActiveSandboxCount >= info.Spec.MaxConcurrentSandboxes {
		return fmt.Errorf("tenant %s exceeded concurrent sandbox limit (%d/%d)",
			tenantName, info.ResourceUsage.ActiveSandboxCount, info.Spec.MaxConcurrentSandboxes)
	}

	// Check resource quota
	if err := tm.quotaManager.CheckQuota(tenantName, resources); err != nil {
		return fmt.Errorf("tenant %s quota check failed: %w", tenantName, err)
	}

	// Check allowed runtimes
	if resources != nil {
		// Runtime check is done at sandbox creation time
	}

	return nil
}

// AllocateResources allocates resources for a tenant's sandbox.
func (tm *TenantManager) AllocateResources(tenantName string, nodeName string, resources *sandboxv1alpha1.ResourceRequirements) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	// Parse and allocate CPU
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		info.ResourceUsage.CPUUsed += cpu.MilliValue()
	}

	// Parse and allocate memory
	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		info.ResourceUsage.MemoryUsedBytes += mem.Value()
	}

	// Allocate GPU
	if resources.GPU != "" {
		if gpu, err := resource.ParseQuantity(resources.GPU); err == nil {
			info.ResourceUsage.GPUUsed += gpu.Value()
		}
	}

	// Update sandbox counts
	info.ResourceUsage.ActiveSandboxCount++
	info.ResourceUsage.TotalSandboxCount++
	info.DailySandboxCount++
	info.SandboxCount[nodeName]++
	info.LastUpdated = time.Now()

	// Update quota usage
	tm.quotaManager.Allocate(tenantName, resources)

	klog.V(4).Infof("Allocated resources for tenant %s on node %s", tenantName, nodeName)
	return nil
}

// ReleaseResources releases resources for a tenant's sandbox.
func (tm *TenantManager) ReleaseResources(tenantName string, nodeName string, resources *sandboxv1alpha1.ResourceRequirements) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	// Parse and release CPU
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		info.ResourceUsage.CPUUsed -= cpu.MilliValue()
		if info.ResourceUsage.CPUUsed < 0 {
			info.ResourceUsage.CPUUsed = 0
		}
	}

	// Parse and release memory
	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		info.ResourceUsage.MemoryUsedBytes -= mem.Value()
		if info.ResourceUsage.MemoryUsedBytes < 0 {
			info.ResourceUsage.MemoryUsedBytes = 0
		}
	}

	// Release GPU
	if resources.GPU != "" {
		if gpu, err := resource.ParseQuantity(resources.GPU); err == nil {
			info.ResourceUsage.GPUUsed -= gpu.Value()
			if info.ResourceUsage.GPUUsed < 0 {
				info.ResourceUsage.GPUUsed = 0
			}
		}
	}

	// Update sandbox counts
	info.ResourceUsage.ActiveSandboxCount--
	if info.ResourceUsage.ActiveSandboxCount < 0 {
		info.ResourceUsage.ActiveSandboxCount = 0
	}
	if info.SandboxCount[nodeName] > 0 {
		info.SandboxCount[nodeName]--
	}
	info.LastUpdated = time.Now()

	// Release quota
	tm.quotaManager.Release(tenantName, resources)

	klog.V(4).Infof("Released resources for tenant %s on node %s", tenantName, nodeName)
	return nil
}

// ValidateRuntime checks if a tenant is allowed to use the specified runtime.
func (tm *TenantManager) ValidateRuntime(tenantName string, runtime sandboxv1alpha1.SandboxRuntimeType) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	for _, allowed := range info.Spec.AllowedRuntimes {
		if allowed == runtime {
			return nil
		}
	}

	return fmt.Errorf("tenant %s is not allowed to use runtime %s (allowed: %v)",
		tenantName, runtime, info.Spec.AllowedRuntimes)
}

// ValidateSchedulingPolicy checks if a tenant is allowed to use the specified scheduling policy.
func (tm *TenantManager) ValidateSchedulingPolicy(tenantName string, policy sandboxv1alpha1.SandboxSchedulingPolicy) error {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return fmt.Errorf("tenant %s not found", tenantName)
	}

	for _, allowed := range info.Spec.AllowedSchedulingPolicies {
		if allowed == policy {
			return nil
		}
	}

	return fmt.Errorf("tenant %s is not allowed to use scheduling policy %s (allowed: %v)",
		tenantName, policy, info.Spec.AllowedSchedulingPolicies)
}

// GetTenantNodes returns the nodes where a tenant has sandboxes.
func (tm *TenantManager) GetTenantNodes(tenantName string) []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return nil
	}

	nodes := make([]string, 0, len(info.SandboxCount))
	for node, count := range info.SandboxCount {
		if count > 0 {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetTenantSandboxCount returns the number of sandboxes a tenant has on a specific node.
func (tm *TenantManager) GetTenantSandboxCount(tenantName, nodeName string) int32 {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return 0
	}
	return info.SandboxCount[nodeName]
}

// UpdateTenantStatus updates the tenant status.
func (tm *TenantManager) UpdateTenantStatus(tenantName string, phase sandboxv1alpha1.TenantPhase) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return
	}

	info.Status.Phase = phase
	info.LastUpdated = time.Now()
}

// GetCostReport returns the cost report for a tenant.
func (tm *TenantManager) GetCostReport(tenantName string) (*TenantCostRecord, error) {
	tm.costTracker.mu.RLock()
	defer tm.costTracker.mu.RUnlock()

	record, exists := tm.costTracker.costPerTenant[tenantName]
	if !exists {
		return nil, fmt.Errorf("cost record not found for tenant %s", tenantName)
	}

	// Return a copy
	copy := *record
	copy.CostByResource = make(map[string]float64)
	for k, v := range record.CostByResource {
		copy.CostByResource[k] = v
	}
	copy.CostByNode = make(map[string]float64)
	for k, v := range record.CostByNode {
		copy.CostByNode[k] = v
	}

	return &copy, nil
}

// validateTenantSpec validates a tenant specification.
func (tm *TenantManager) validateTenantSpec(spec sandboxv1alpha1.TenantSpec) error {
	if spec.DisplayName == "" {
		return fmt.Errorf("displayName is required")
	}
	if spec.ResourceQuota.CPU == "" {
		return fmt.Errorf("resourceQuota.cpu is required")
	}
	if spec.ResourceQuota.Memory == "" {
		return fmt.Errorf("resourceQuota.memory is required")
	}
	if spec.ResourceQuota.MaxInstances <= 0 {
		return fmt.Errorf("resourceQuota.maxInstances must be positive")
	}
	if spec.MaxConcurrentSandboxes <= 0 {
		return fmt.Errorf("maxConcurrentSandboxes must be positive")
	}
	if len(spec.AllowedRuntimes) == 0 {
		return fmt.Errorf("at least one allowedRuntime is required")
	}
	return nil
}

// onTenantAdd handles tenant addition events.
func (tm *TenantManager) onTenantAdd(obj interface{}) {
	tenant, ok := obj.(*sandboxv1alpha1.Tenant)
	if !ok {
		klog.Warningf("Received non-Tenant object: %v", obj)
		return
	}

	ctx := context.Background()
	if err := tm.RegisterTenant(ctx, tenant); err != nil {
		klog.Errorf("Failed to register tenant %s: %v", tenant.Name, err)
	}
}

// onTenantUpdate handles tenant update events.
func (tm *TenantManager) onTenantUpdate(oldObj, newObj interface{}) {
	tenant, ok := newObj.(*sandboxv1alpha1.Tenant)
	if !ok {
		klog.Warningf("Received non-Tenant object: %v", newObj)
		return
	}

	ctx := context.Background()
	if err := tm.RegisterTenant(ctx, tenant); err != nil {
		klog.Errorf("Failed to update tenant %s: %v", tenant.Name, err)
	}
}

// onTenantDelete handles tenant deletion events.
func (tm *TenantManager) onTenantDelete(obj interface{}) {
	tenant, ok := obj.(*sandboxv1alpha1.Tenant)
	if !ok {
		klog.Warningf("Received non-Tenant object: %v", obj)
		return
	}

	ctx := context.Background()
	if err := tm.UnregisterTenant(ctx, tenant.Name); err != nil {
		klog.Errorf("Failed to unregister tenant %s: %v", tenant.Name, err)
	}
}

// resetDailyCounters resets the daily sandbox creation counters.
func (tm *TenantManager) resetDailyCounters() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	for name, info := range tm.tenants {
		// Reset if the day has changed
		if now.Sub(info.DailyCountResetTime) >= 24*time.Hour {
			info.DailySandboxCount = 0
			info.DailyCountResetTime = now
			klog.V(4).Infof("Reset daily sandbox counter for tenant %s", name)
		}
	}
}

// calculateCosts calculates costs for all tenants.
func (tm *TenantManager) calculateCosts() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	for name, info := range tm.tenants {
		tm.costTracker.mu.Lock()
		record, exists := tm.costTracker.costPerTenant[name]
		if !exists {
			record = &TenantCostRecord{
				TenantName:     name,
				CostByResource: make(map[string]float64),
				CostByNode:     make(map[string]float64),
			}
			tm.costTracker.costPerTenant[name] = record
		}

		// Calculate CPU cost
		cpuCores := float64(info.ResourceUsage.CPUUsed) / 1000.0
		cpuCostPerHour := cpuCores * tm.costTracker.pricingModel.CPUPricePerCorePerHour
		record.CostByResource["cpu"] = cpuCostPerHour

		// Calculate memory cost
		memGB := float64(info.ResourceUsage.MemoryUsedBytes) / (1024 * 1024 * 1024)
		memCostPerHour := memGB * tm.costTracker.pricingModel.MemoryPricePerGBPerHour
		record.CostByResource["memory"] = memCostPerHour

		// Calculate GPU cost
		gpuCount := float64(info.ResourceUsage.GPUUsed)
		gpuCostPerHour := gpuCount * tm.costTracker.pricingModel.GPUPricePerUnitPerHour
		record.CostByResource["gpu"] = gpuCostPerHour

		// Calculate storage cost
		storageGB := float64(info.ResourceUsage.StorageUsedBytes) / (1024 * 1024 * 1024)
		storageCostPerHour := storageGB * tm.costTracker.pricingModel.StoragePricePerGBPerHour
		record.CostByResource["storage"] = storageCostPerHour

		// Calculate total hourly rate
		hourlyRate := cpuCostPerHour + memCostPerHour + gpuCostPerHour + storageCostPerHour

		// Update accumulated cost (assuming 1-minute interval)
		record.AccumulatedCost += hourlyRate / 60.0
		record.DailyCost += hourlyRate / 60.0
		record.LastUpdated = time.Now()

		tm.costTracker.mu.Unlock()
	}
}

// GetTenantQuotaUsage returns the current quota usage for a tenant.
func (tm *TenantManager) GetTenantQuotaUsage(tenantName string) (*sandboxv1alpha1.QuotaUsage, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return nil, fmt.Errorf("tenant %s not found", tenantName)
	}

	quotaUsage := tm.quotaManager.GetUsage(tenantName)
	if quotaUsage == nil {
		return nil, fmt.Errorf("quota usage not found for tenant %s", tenantName)
	}

	// Build the status
	info.Status.QuotaUsage = quotaUsage
	info.Status.ActiveSandboxes = info.ResourceUsage.ActiveSandboxCount
	info.Status.TotalSandboxesCreated = info.ResourceUsage.TotalSandboxCount
	info.Status.SandboxesCreatedToday = info.DailySandboxCount

	return quotaUsage, nil
}

// BuildTenantStatus builds the full tenant status from current state.
func (tm *TenantManager) BuildTenantStatus(tenantName string) (*sandboxv1alpha1.TenantStatus, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	info, exists := tm.tenants[tenantName]
	if !exists {
		return nil, fmt.Errorf("tenant %s not found", tenantName)
	}

	status := &sandboxv1alpha1.TenantStatus{
		Phase:                 info.Status.Phase,
		ActiveSandboxes:       info.ResourceUsage.ActiveSandboxCount,
		TotalSandboxesCreated: info.ResourceUsage.TotalSandboxCount,
		SandboxesCreatedToday: info.DailySandboxCount,
		ResourceUsage: &sandboxv1alpha1.AggregateResourceUsage{
			CPUUsage:          fmt.Sprintf("%dm", info.ResourceUsage.CPUUsed),
			MemoryUsageBytes:  uint64(info.ResourceUsage.MemoryUsedBytes),
			GPUUsage:          fmt.Sprintf("%d", info.ResourceUsage.GPUUsed),
			StorageUsageBytes: uint64(info.ResourceUsage.StorageUsedBytes),
		},
	}

	// Get quota usage
	quotaUsage := tm.quotaManager.GetUsage(tenantName)
	if quotaUsage != nil {
		status.QuotaUsage = quotaUsage
	}

	// Get cost summary
	tm.costTracker.mu.RLock()
	if costRecord, exists := tm.costTracker.costPerTenant[tenantName]; exists {
		status.CostSummary = &sandboxv1alpha1.TenantCostSummary{
			DailyCost:      costRecord.DailyCost,
			MonthlyCost:    costRecord.MonthlyCost,
			CostByResource: costRecord.CostByResource,
			CostByNode:     costRecord.CostByNode,
			LastUpdated:    metav1.Now(),
		}
	}
	tm.costTracker.mu.RUnlock()

	// Set conditions
	status.Conditions = tm.buildTenantConditions(info)

	return status, nil
}

// buildTenantConditions builds the conditions for a tenant.
func (tm *TenantManager) buildTenantConditions(info *TenantInfo) []sandboxv1alpha1.TenantCondition {
	now := metav1.Now()
	conditions := []sandboxv1alpha1.TenantCondition{}

	// QuotaAvailable condition
	quotaOK := true
	if info.ResourceUsage.ActiveSandboxCount >= info.Spec.MaxConcurrentSandboxes {
		quotaOK = false
	}
	conditions = append(conditions, sandboxv1alpha1.TenantCondition{
		Type:               sandboxv1alpha1.TenantConditionQuotaAvailable,
		Status:             boolToConditionStatus(quotaOK),
		LastTransitionTime: now,
		Reason: func() string {
			if quotaOK {
				return "QuotaAvailable"
			}
			return "QuotaExhausted"
		}(),
		Message: func() string {
			if quotaOK {
				return "Tenant has quota available"
			}
			return fmt.Sprintf("Tenant has reached concurrent sandbox limit (%d)", info.Spec.MaxConcurrentSandboxes)
		}(),
	})

	// RateLimitOK condition
	rateLimitOK := tm.rateLimiter.Allow(info.Name, "APICall")
	conditions = append(conditions, sandboxv1alpha1.TenantCondition{
		Type:               sandboxv1alpha1.TenantConditionRateLimitOK,
		Status:             boolToConditionStatus(rateLimitOK),
		LastTransitionTime: now,
		Reason: func() string {
			if rateLimitOK {
				return "WithinRateLimit"
			}
			return "RateLimitExceeded"
		}(),
	})

	// ResourcesHealthy condition
	healthy := info.Status.Phase == sandboxv1alpha1.TenantActive
	conditions = append(conditions, sandboxv1alpha1.TenantCondition{
		Type:               sandboxv1alpha1.TenantConditionResourcesHealthy,
		Status:             boolToConditionStatus(healthy),
		LastTransitionTime: now,
		Reason: func() string {
			if healthy {
				return "ResourcesHealthy"
			}
			return "ResourcesUnhealthy"
		}(),
	})

	return conditions
}

func boolToConditionStatus(b bool) sandboxv1alpha1.ConditionStatus {
	if b {
		return sandboxv1alpha1.ConditionTrue
	}
	return sandboxv1alpha1.ConditionFalse
}
