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

package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// PoolManager manages pre-warmed sandbox pools to reduce cold-start latency.
// It maintains a pool of ready-to-use sandbox runtimes that can be
// immediately assigned to incoming sandbox requests.
type PoolManager struct {
	mu sync.RWMutex

	// runtimeManager is the parent runtime manager.
	runtimeManager *RuntimeManager

	// config holds pool configuration.
	config *RuntimeManagerConfig

	// pools maps runtime type to its pool of pre-warmed handles.
	pools map[sandboxv1alpha1.SandboxRuntimeType]*SandboxPool

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// SandboxPool represents a pool of pre-warmed sandbox runtimes.
type SandboxPool struct {
	mu sync.RWMutex

	// RuntimeType is the type of runtime in this pool.
	RuntimeType sandboxv1alpha1.SandboxRuntimeType

	// TargetSize is the desired pool size.
	TargetSize int32

	// Available are ready-to-use sandbox handles.
	Available []RuntimeHandle

	// InUse are sandbox handles currently in use.
	InUse map[string]RuntimeHandle

	// Creating is the count of sandboxes being created.
	Creating int32

	// Stats tracks pool statistics.
	Stats PoolStats
}

// PoolStats tracks pool statistics.
type PoolStats struct {
	// TotalCreated is the total number of sandboxes created.
	TotalCreated int64
	// TotalReused is the total number of sandboxes reused from pool.
	TotalReused int64
	// TotalEvicted is the total number of sandboxes evicted from pool.
	TotalEvicted int64
	// AvgCreateTime is the average time to create a sandbox.
	AvgCreateTime time.Duration
	// AvgReuseTime is the average time to reuse a sandbox from pool.
	AvgReuseTime time.Duration
	// HitRate is the pool hit rate (reuses / total requests).
	HitRate float64
}

// NewPoolManager creates a new PoolManager.
func NewPoolManager(runtimeManager *RuntimeManager, config *RuntimeManagerConfig) *PoolManager {
	pm := &PoolManager{
		runtimeManager: runtimeManager,
		config:         config,
		pools:          make(map[sandboxv1alpha1.SandboxRuntimeType]*SandboxPool),
		stopCh:         make(chan struct{}),
	}

	// Initialize pools for each runtime type
	for runtimeType, size := range config.PoolSize {
		pm.pools[runtimeType] = &SandboxPool{
			RuntimeType: runtimeType,
			TargetSize:  size,
			Available:   make([]RuntimeHandle, 0),
			InUse:       make(map[string]RuntimeHandle),
		}
	}

	return pm
}

// Start starts the pool manager's background goroutines.
func (pm *PoolManager) Start(ctx context.Context) {
	klog.Info("Starting sandbox pool manager")

	// Initial pool warm-up
	go pm.warmUpPools(ctx)

	// Periodic pool maintenance
	go wait.Until(func() {
		pm.replenishPools(ctx)
		pm.evictIdleSandboxes()
		pm.updatePoolStats()
	}, pm.config.PoolRefreshInterval, pm.stopCh)

	klog.Info("Sandbox pool manager started")
}

// Stop stops the pool manager.
func (pm *PoolManager) Stop() {
	klog.Info("Stopping sandbox pool manager")
	close(pm.stopCh)
	pm.drainPools()
}

// GetFromPool gets a sandbox handle from the pool.
func (pm *PoolManager) GetFromPool(runtimeType sandboxv1alpha1.SandboxRuntimeType, spec *RuntimeSpec) RuntimeHandle {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pool, exists := pm.pools[runtimeType]
	if !exists {
		return nil
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	if len(pool.Available) == 0 {
		return nil
	}

	// Get the first available handle
	handle := pool.Available[0]
	pool.Available = pool.Available[1:]

	// Track as in-use
	key := spec.SandboxName + "/" + spec.Namespace
	pool.InUse[key] = handle

	// Update stats
	pool.Stats.TotalReused++

	klog.V(4).Infof("Reused sandbox from pool (type: %s, remaining: %d)", runtimeType, len(pool.Available))
	return handle
}

// ReturnToPool returns a sandbox handle to the pool.
func (pm *PoolManager) ReturnToPool(runtimeType sandboxv1alpha1.SandboxRuntimeType, key string) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	pool, exists := pm.pools[runtimeType]
	if !exists {
		return
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	handle, exists := pool.InUse[key]
	if !exists {
		return
	}

	delete(pool.InUse, key)

	// Only return to pool if it's still ready and pool isn't full
	if handle.IsReady() && int32(len(pool.Available)) < pool.TargetSize {
		pool.Available = append(pool.Available, handle)
		klog.V(4).Infof("Returned sandbox to pool (type: %s, available: %d)", runtimeType, len(pool.Available))
	} else {
		// Clean up the handle
		handle.Cleanup(context.Background())
	}
}

// warmUpPools warms up all pools to their target sizes.
func (pm *PoolManager) warmUpPools(ctx context.Context) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for runtimeType, pool := range pm.pools {
		needed := pool.TargetSize - int32(len(pool.Available)+len(pool.InUse)) - pool.Creating
		if needed <= 0 {
			continue
		}

		klog.Infof("Warming up pool for %s: need %d sandboxes", runtimeType, needed)

		for i := int32(0); i < needed; i++ {
			go pm.createPooledSandbox(ctx, runtimeType)
		}
	}
}

// replenishPools replenishes pools that are below target size.
func (pm *PoolManager) replenishPools(ctx context.Context) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for runtimeType, pool := range pm.pools {
		pool.mu.RLock()
		available := int32(len(pool.Available))
		inUse := int32(len(pool.InUse))
		creating := pool.Creating
		pool.mu.RUnlock()

		total := available + inUse + creating
		if total < pool.TargetSize {
			needed := pool.TargetSize - total
			klog.V(4).Infof("Replenishing pool for %s: need %d more sandboxes", runtimeType, needed)

			for i := int32(0); i < needed; i++ {
				go pm.createPooledSandbox(ctx, runtimeType)
			}
		}
	}
}

// createPooledSandbox creates a sandbox for the pool.
func (pm *PoolManager) createPooledSandbox(ctx context.Context, runtimeType sandboxv1alpha1.SandboxRuntimeType) {
	pm.mu.RLock()
	pool, exists := pm.pools[runtimeType]
	pm.mu.RUnlock()

	if !exists {
		return
	}

	pool.mu.Lock()
	pool.Creating++
	pool.mu.Unlock()

	defer func() {
		pool.mu.Lock()
		pool.Creating--
		pool.mu.Unlock()
	}()

	// Create a generic spec for the pooled sandbox
	spec := &RuntimeSpec{
		SandboxName: fmt.Sprintf("pool-%s-%d", runtimeType, time.Now().UnixNano()),
		Namespace:   "nexusbox-system",
		TenantName:  "system",
		RuntimeType: runtimeType,
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU:    "500m",
			Memory: "512Mi",
		},
	}

	startTime := time.Now()

	provider, exists := pm.runtimeManager.GetProvider(runtimeType)
	if !exists {
		klog.Warningf("No provider for runtime type %s", runtimeType)
		return
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		klog.Warningf("Failed to create pooled sandbox (type: %s): %v", runtimeType, err)
		return
	}

	elapsed := time.Since(startTime)

	pool.mu.Lock()
	if int32(len(pool.Available)) < pool.TargetSize {
		pool.Available = append(pool.Available, handle)
		pool.Stats.TotalCreated++
		pool.Stats.AvgCreateTime = (pool.Stats.AvgCreateTime*time.Duration(pool.Stats.TotalCreated-1) + elapsed) / time.Duration(pool.Stats.TotalCreated)
	} else {
		// Pool is full, clean up
		handle.Cleanup(ctx)
	}
	pool.mu.Unlock()

	klog.V(4).Infof("Created pooled sandbox (type: %s, time: %v)", runtimeType, elapsed)
}

// evictIdleSandboxes removes sandboxes that have been idle too long.
func (pm *PoolManager) evictIdleSandboxes() {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for runtimeType, pool := range pm.pools {
		pool.mu.Lock()
		// If we have more than target, evict excess
		excess := int32(len(pool.Available)) - pool.TargetSize
		if excess > 0 {
			for i := int32(0); i < excess && len(pool.Available) > 0; i++ {
				handle := pool.Available[len(pool.Available)-1]
				pool.Available = pool.Available[:len(pool.Available)-1]
				handle.Cleanup(context.Background())
				pool.Stats.TotalEvicted++
			}
		}
		pool.mu.Unlock()

		klog.V(6).Infof("Pool %s: available=%d, inUse=%d",
			runtimeType, len(pool.Available), len(pool.InUse))
	}
}

// updatePoolStats updates pool statistics.
func (pm *PoolManager) updatePoolStats() {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, pool := range pm.pools {
		pool.mu.Lock()
		totalRequests := pool.Stats.TotalReused + pool.Stats.TotalCreated
		if totalRequests > 0 {
			pool.Stats.HitRate = float64(pool.Stats.TotalReused) / float64(totalRequests)
		}
		pool.mu.Unlock()
	}
}

// drainPools drains all pools during shutdown.
func (pm *PoolManager) drainPools() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	ctx := context.Background()

	for runtimeType, pool := range pm.pools {
		pool.mu.Lock()

		// Clean up all available handles
		for _, handle := range pool.Available {
			handle.Cleanup(ctx)
		}
		pool.Available = nil

		// Clean up all in-use handles
		for key, handle := range pool.InUse {
			handle.Cleanup(ctx)
			delete(pool.InUse, key)
		}

		pool.mu.Unlock()
		klog.Infof("Drained pool for %s", runtimeType)
	}
}

// GetPoolStats returns statistics for all pools.
func (pm *PoolManager) GetPoolStats() map[sandboxv1alpha1.SandboxRuntimeType]*PoolStats {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make(map[sandboxv1alpha1.SandboxRuntimeType]*PoolStats)
	for runtimeType, pool := range pm.pools {
		pool.mu.RLock()
		statsCopy := pool.Stats
		pool.mu.RUnlock()
		result[runtimeType] = &statsCopy
	}
	return result
}

// ResizePool changes the target size of a pool.
func (pm *PoolManager) ResizePool(runtimeType sandboxv1alpha1.SandboxRuntimeType, newSize int32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pool, exists := pm.pools[runtimeType]
	if !exists {
		return
	}

	pool.mu.Lock()
	oldSize := pool.TargetSize
	pool.TargetSize = newSize
	pool.mu.Unlock()

	klog.Infof("Resized pool for %s: %d -> %d", runtimeType, oldSize, newSize)
}
