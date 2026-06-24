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

package quota

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// QuotaManager manages resource quotas for tenants.
// It tracks quota limits and current usage, and provides methods
// to check, allocate, and release resources.
type QuotaManager struct {
	mu sync.RWMutex
	// quotas maps tenant name to its quota information.
	quotas map[string]*TenantQuota
}

// TenantQuota holds quota information for a single tenant.
type TenantQuota struct {
	// TenantName is the tenant identifier.
	TenantName string
	// HardLimits defines the hard quota limits.
	HardLimits ResourceQuantities
	// Used defines the currently used resources.
	Used ResourceQuantities
	// Reserved defines resources reserved but not yet consumed.
	Reserved ResourceQuantities
}

// ResourceQuantities represents quantities of various resources.
type ResourceQuantities struct {
	// CPU in millicores.
	CPU int64
	// Memory in bytes.
	Memory int64
	// GPU count.
	GPU int64
	// EphemeralStorage in bytes.
	EphemeralStorage int64
	// PersistentStorage in bytes.
	PersistentStorage int64
	// InstanceCount is the number of sandbox instances.
	InstanceCount int32
	// InstanceCountPerNode maps node name to instance count.
	InstanceCountPerNode map[string]int32
}

// NewQuotaManager creates a new QuotaManager.
func NewQuotaManager() *QuotaManager {
	return &QuotaManager{
		quotas: make(map[string]*TenantQuota),
	}
}

// RegisterQuota registers a resource quota for a tenant.
func (qm *QuotaManager) RegisterQuota(tenantName string, quota *sandboxv1alpha1.TenantResourceQuota) error {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	hardLimits, err := qm.parseResourceQuota(quota)
	if err != nil {
		return fmt.Errorf("failed to parse resource quota for tenant %s: %w", tenantName, err)
	}

	qm.quotas[tenantName] = &TenantQuota{
		TenantName: tenantName,
		HardLimits: hardLimits,
		Used: ResourceQuantities{
			InstanceCountPerNode: make(map[string]int32),
		},
		Reserved: ResourceQuantities{
			InstanceCountPerNode: make(map[string]int32),
		},
	}

	klog.Infof("Registered quota for tenant %s: CPU=%dm, Memory=%d, Instances=%d",
		tenantName, hardLimits.CPU, hardLimits.Memory, hardLimits.InstanceCount)
	return nil
}

// UnregisterQuota removes a tenant's quota.
func (qm *QuotaManager) UnregisterQuota(tenantName string) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	delete(qm.quotas, tenantName)
	klog.Infof("Unregistered quota for tenant %s", tenantName)
}

// CheckQuota checks if a resource allocation would fit within the tenant's quota.
func (qm *QuotaManager) CheckQuota(tenantName string, resources *sandboxv1alpha1.ResourceRequirements) error {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return fmt.Errorf("quota not found for tenant %s", tenantName)
	}

	if resources == nil {
		return nil
	}

	// Check CPU
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		requestedCPU := cpu.MilliValue()
		if quota.Used.CPU+requestedCPU > quota.HardLimits.CPU {
			return fmt.Errorf("CPU quota exceeded for tenant %s: used %dm + requested %dm > limit %dm",
				tenantName, quota.Used.CPU, requestedCPU, quota.HardLimits.CPU)
		}
	}

	// Check Memory
	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		requestedMem := mem.Value()
		if quota.Used.Memory+requestedMem > quota.HardLimits.Memory {
			return fmt.Errorf("memory quota exceeded for tenant %s: used %d + requested %d > limit %d",
				tenantName, quota.Used.Memory, requestedMem, quota.HardLimits.Memory)
		}
	}

	// Check GPU
	if resources.GPU != "" {
		if gpu, err := resource.ParseQuantity(resources.GPU); err == nil {
			requestedGPU := gpu.Value()
			if quota.Used.GPU+requestedGPU > quota.HardLimits.GPU {
				return fmt.Errorf("GPU quota exceeded for tenant %s: used %d + requested %d > limit %d",
					tenantName, quota.Used.GPU, requestedGPU, quota.HardLimits.GPU)
			}
		}
	}

	// Check instance count
	if quota.Used.InstanceCount+1 > quota.HardLimits.InstanceCount {
		return fmt.Errorf("instance quota exceeded for tenant %s: used %d + 1 > limit %d",
			tenantName, quota.Used.InstanceCount, quota.HardLimits.InstanceCount)
	}

	return nil
}

// Allocate allocates resources for a tenant.
func (qm *QuotaManager) Allocate(tenantName string, resources *sandboxv1alpha1.ResourceRequirements) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return
	}

	if resources == nil {
		return
	}

	// Allocate CPU
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		quota.Used.CPU += cpu.MilliValue()
	}

	// Allocate Memory
	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		quota.Used.Memory += mem.Value()
	}

	// Allocate GPU
	if resources.GPU != "" {
		if gpu, err := resource.ParseQuantity(resources.GPU); err == nil {
			quota.Used.GPU += gpu.Value()
		}
	}

	// Allocate EphemeralStorage
	if resources.EphemeralStorage != "" {
		if storage, err := resource.ParseQuantity(resources.EphemeralStorage); err == nil {
			quota.Used.EphemeralStorage += storage.Value()
		}
	}

	// Increment instance count
	quota.Used.InstanceCount++

	klog.V(4).Infof("Allocated resources for tenant %s: CPU=%dm, Memory=%d, Instances=%d",
		tenantName, quota.Used.CPU, quota.Used.Memory, quota.Used.InstanceCount)
}

// Release releases resources for a tenant.
func (qm *QuotaManager) Release(tenantName string, resources *sandboxv1alpha1.ResourceRequirements) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return
	}

	if resources == nil {
		return
	}

	// Release CPU
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		quota.Used.CPU -= cpu.MilliValue()
		if quota.Used.CPU < 0 {
			quota.Used.CPU = 0
		}
	}

	// Release Memory
	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		quota.Used.Memory -= mem.Value()
		if quota.Used.Memory < 0 {
			quota.Used.Memory = 0
		}
	}

	// Release GPU
	if resources.GPU != "" {
		if gpu, err := resource.ParseQuantity(resources.GPU); err == nil {
			quota.Used.GPU -= gpu.Value()
			if quota.Used.GPU < 0 {
				quota.Used.GPU = 0
			}
		}
	}

	// Release EphemeralStorage
	if resources.EphemeralStorage != "" {
		if storage, err := resource.ParseQuantity(resources.EphemeralStorage); err == nil {
			quota.Used.EphemeralStorage -= storage.Value()
			if quota.Used.EphemeralStorage < 0 {
				quota.Used.EphemeralStorage = 0
			}
		}
	}

	// Decrement instance count
	quota.Used.InstanceCount--
	if quota.Used.InstanceCount < 0 {
		quota.Used.InstanceCount = 0
	}

	klog.V(4).Infof("Released resources for tenant %s: CPU=%dm, Memory=%d, Instances=%d",
		tenantName, quota.Used.CPU, quota.Used.Memory, quota.Used.InstanceCount)
}

// Reserve reserves resources for a tenant (for gang scheduling).
func (qm *QuotaManager) Reserve(tenantName string, resources *sandboxv1alpha1.ResourceRequirements) error {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return fmt.Errorf("quota not found for tenant %s", tenantName)
	}

	if resources == nil {
		return nil
	}

	// Check against used + reserved
	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		if quota.Used.CPU+quota.Reserved.CPU+cpu.MilliValue() > quota.HardLimits.CPU {
			return fmt.Errorf("CPU quota would be exceeded for tenant %s (including reservations)", tenantName)
		}
		quota.Reserved.CPU += cpu.MilliValue()
	}

	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		if quota.Used.Memory+quota.Reserved.Memory+mem.Value() > quota.HardLimits.Memory {
			return fmt.Errorf("memory quota would be exceeded for tenant %s (including reservations)", tenantName)
		}
		quota.Reserved.Memory += mem.Value()
	}

	quota.Reserved.InstanceCount++

	return nil
}

// Unreserve releases previously reserved resources.
func (qm *QuotaManager) Unreserve(tenantName string, resources *sandboxv1alpha1.ResourceRequirements) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return
	}

	if resources == nil {
		return
	}

	if cpu, err := resource.ParseQuantity(resources.CPU); err == nil {
		quota.Reserved.CPU -= cpu.MilliValue()
		if quota.Reserved.CPU < 0 {
			quota.Reserved.CPU = 0
		}
	}

	if mem, err := resource.ParseQuantity(resources.Memory); err == nil {
		quota.Reserved.Memory -= mem.Value()
		if quota.Reserved.Memory < 0 {
			quota.Reserved.Memory = 0
		}
	}

	quota.Reserved.InstanceCount--
	if quota.Reserved.InstanceCount < 0 {
		quota.Reserved.InstanceCount = 0
	}
}

// GetUsage returns the current quota usage for a tenant.
func (qm *QuotaManager) GetUsage(tenantName string) *sandboxv1alpha1.QuotaUsage {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return nil
	}

	return &sandboxv1alpha1.QuotaUsage{
		CPUUsed:          fmt.Sprintf("%dm", quota.Used.CPU),
		CPULimit:         fmt.Sprintf("%dm", quota.HardLimits.CPU),
		MemoryUsedBytes:  uint64(quota.Used.Memory),
		MemoryLimitBytes: uint64(quota.HardLimits.Memory),
		InstanceUsed:     quota.Used.InstanceCount,
		InstanceLimit:    quota.HardLimits.InstanceCount,
		GPUUsed:          fmt.Sprintf("%d", quota.Used.GPU),
		GPULimit:         fmt.Sprintf("%d", quota.HardLimits.GPU),
	}
}

// GetQuota returns the full quota information for a tenant.
func (qm *QuotaManager) GetQuota(tenantName string) (*TenantQuota, bool) {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	quota, exists := qm.quotas[tenantName]
	if !exists {
		return nil, false
	}

	// Return a copy
	copy := *quota
	copy.Used.InstanceCountPerNode = make(map[string]int32)
	for k, v := range quota.Used.InstanceCountPerNode {
		copy.Used.InstanceCountPerNode[k] = v
	}
	copy.Reserved.InstanceCountPerNode = make(map[string]int32)
	for k, v := range quota.Reserved.InstanceCountPerNode {
		copy.Reserved.InstanceCountPerNode[k] = v
	}

	return &copy, true
}

// parseResourceQuota parses a TenantResourceQuota into ResourceQuantities.
func (qm *QuotaManager) parseResourceQuota(quota *sandboxv1alpha1.TenantResourceQuota) (ResourceQuantities, error) {
	result := ResourceQuantities{
		InstanceCountPerNode: make(map[string]int32),
	}

	// Parse CPU
	if quota.CPU != "" {
		cpu, err := resource.ParseQuantity(quota.CPU)
		if err != nil {
			return result, fmt.Errorf("invalid CPU quota %q: %w", quota.CPU, err)
		}
		result.CPU = cpu.MilliValue()
	}

	// Parse Memory
	if quota.Memory != "" {
		mem, err := resource.ParseQuantity(quota.Memory)
		if err != nil {
			return result, fmt.Errorf("invalid memory quota %q: %w", quota.Memory, err)
		}
		result.Memory = mem.Value()
	}

	// Parse GPU
	if quota.GPU != "" {
		gpu, err := resource.ParseQuantity(quota.GPU)
		if err != nil {
			return result, fmt.Errorf("invalid GPU quota %q: %w", quota.GPU, err)
		}
		result.GPU = gpu.Value()
	}

	// Parse EphemeralStorage
	if quota.EphemeralStorage != "" {
		storage, err := resource.ParseQuantity(quota.EphemeralStorage)
		if err != nil {
			return result, fmt.Errorf("invalid ephemeral storage quota %q: %w", quota.EphemeralStorage, err)
		}
		result.EphemeralStorage = storage.Value()
	}

	// Parse PersistentStorage
	if quota.PersistentStorage != "" {
		storage, err := resource.ParseQuantity(quota.PersistentStorage)
		if err != nil {
			return result, fmt.Errorf("invalid persistent storage quota %q: %w", quota.PersistentStorage, err)
		}
		result.PersistentStorage = storage.Value()
	}

	result.InstanceCount = quota.MaxInstances
	result.InstanceCountPerNode = make(map[string]int32)
	if quota.MaxInstancesPerNode > 0 {
		// This will be populated per-node during scheduling
	}

	return result, nil
}
