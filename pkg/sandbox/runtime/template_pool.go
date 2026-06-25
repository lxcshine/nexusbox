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

// TemplatePoolConfig defines pre-warming configuration for a specific template.
type TemplatePoolConfig struct {
	// TemplateName is the name of the SandboxTemplate.
	TemplateName string
	// TargetSize is the desired number of pre-warmed sandboxes.
	TargetSize int32
	// MinSize is the minimum pool size (won't go below this).
	MinSize int32
	// MaxSize is the maximum pool size (won't exceed this).
	MaxSize int32
	// ScaleUpThreshold is the utilization % that triggers scaling up.
	// Example: 80 means scale up when 80% of pool is in use.
	ScaleUpThreshold int32
	// ScaleDownThreshold is the utilization % that triggers scaling down.
	ScaleDownThreshold int32
	// TTL is the time-to-live for pooled sandboxes before recycling.
	TTL time.Duration
}

// TemplatePoolEntry represents a pre-warmed sandbox in the template pool.
type TemplatePoolEntry struct {
	Handle      RuntimeHandle
	CreatedAt   time.Time
	LastUsedAt  time.Time
	TemplateRef string
}

// TemplatePoolManager manages pre-warmed sandbox pools per template.
//
// Unlike the runtime-type-based PoolManager, this maintains pools keyed by
// template name, allowing sandboxes to be created with pre-configured
// environments (images, env vars, packages) for < 100ms cold-starts.
//
// Inspired by CubeSandbox's resource pool pre-allocation strategy.
type TemplatePoolManager struct {
	mu sync.RWMutex

	runtimeManager *RuntimeManager
	config         *RuntimeManagerConfig

	// pools maps template name -> pool of pre-warmed entries
	pools map[string]*templatePool

	// configs maps template name -> pool config
	configs map[string]*TemplatePoolConfig

	stopCh chan struct{}
}

type templatePool struct {
	mu         sync.RWMutex
	template   string
	config     *TemplatePoolConfig
	available  []TemplatePoolEntry
	inUse      map[string]TemplatePoolEntry
	creating   int32
	stats      TemplatePoolStats
}

// TemplatePoolStats tracks statistics for a template pool.
type TemplatePoolStats struct {
	TotalCreated   int64
	TotalReused    int64
	TotalEvicted   int64
	TotalExpired   int64
	AvgCreateTime  time.Duration
	AvgReuseTime   time.Duration
	HitRate        float64
}

// NewTemplatePoolManager creates a new TemplatePoolManager.
func NewTemplatePoolManager(rm *RuntimeManager, config *RuntimeManagerConfig) *TemplatePoolManager {
	if config == nil {
		config = DefaultRuntimeManagerConfig()
	}
	return &TemplatePoolManager{
		runtimeManager: rm,
		config:         config,
		pools:          make(map[string]*templatePool),
		configs:        make(map[string]*TemplatePoolConfig),
		stopCh:         make(chan struct{}),
	}
}

// RegisterTemplatePool registers a template for pool pre-warming.
func (m *TemplatePoolManager) RegisterTemplatePool(config *TemplatePoolConfig) error {
	if config.TemplateName == "" {
		return fmt.Errorf("template name is required")
	}
	if config.TargetSize == 0 {
		config.TargetSize = 3
	}
	if config.MinSize == 0 {
		config.MinSize = 1
	}
	if config.MaxSize == 0 {
		config.MaxSize = 10
	}
	if config.ScaleUpThreshold == 0 {
		config.ScaleUpThreshold = 80
	}
	if config.ScaleDownThreshold == 0 {
		config.ScaleDownThreshold = 20
	}
	if config.TTL == 0 {
		config.TTL = 30 * time.Minute
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.configs[config.TemplateName] = config
	m.pools[config.TemplateName] = &templatePool{
		template: config.TemplateName,
		config:   config,
		available: make([]TemplatePoolEntry, 0),
		inUse:    make(map[string]TemplatePoolEntry),
	}

	klog.Infof("Registered template pool for %s (target=%d, min=%d, max=%d)",
		config.TemplateName, config.TargetSize, config.MinSize, config.MaxSize)
	return nil
}

// Start starts the template pool manager's background goroutines.
func (m *TemplatePoolManager) Start(ctx context.Context) {
	klog.Info("Starting template pool manager")

	// Initial warm-up
	go m.warmUpAll(ctx)

	// Periodic maintenance
	go wait.Until(func() {
		m.replenishAll(ctx)
		m.evictExpired()
		m.scalePools(ctx)
		m.updateStats()
	}, m.config.PoolRefreshInterval, m.stopCh)

	klog.Info("Template pool manager started")
}

// Stop stops the manager.
func (m *TemplatePoolManager) Stop() {
	klog.Info("Stopping template pool manager")
	close(m.stopCh)
	m.drainAll()
}

// Acquire gets a pre-warmed sandbox from the template pool.
// Returns nil if no sandbox is available in the pool.
func (m *TemplatePoolManager) Acquire(templateName, sandboxKey string) RuntimeHandle {
	m.mu.RLock()
	pool, exists := m.pools[templateName]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	if len(pool.available) == 0 {
		return nil
	}

	start := time.Now()

	// Get the first available entry
	entry := pool.available[0]
	pool.available = pool.available[1:]

	// Move to in-use
	pool.inUse[sandboxKey] = entry
	pool.stats.TotalReused++

	elapsed := time.Since(start)
	if pool.stats.TotalReused > 0 {
		pool.stats.AvgReuseTime = (pool.stats.AvgReuseTime*time.Duration(pool.stats.TotalReused-1) + elapsed) / time.Duration(pool.stats.TotalReused)
	}

	klog.V(4).Infof("Acquired pooled sandbox for template %s (key=%s, remaining=%d)",
		templateName, sandboxKey, len(pool.available))
	return entry.Handle
}

// Release returns a sandbox handle back to the pool.
func (m *TemplatePoolManager) Release(templateName, sandboxKey string, recycle bool) {
	m.mu.RLock()
	pool, exists := m.pools[templateName]
	m.mu.RUnlock()

	if !exists {
		return
	}

	pool.mu.Lock()
	defer pool.mu.Unlock()

	entry, exists := pool.inUse[sandboxKey]
	if !exists {
		return
	}
	delete(pool.inUse, sandboxKey)

	if recycle && int32(len(pool.available)) < pool.config.MaxSize {
		entry.LastUsedAt = time.Now()
		pool.available = append(pool.available, entry)
		klog.V(4).Infof("Recycled sandbox to pool %s (available=%d)", templateName, len(pool.available))
	} else {
		// Clean up
		entry.Handle.Cleanup(context.Background())
	}
}

// warmUpAll warms up all registered template pools.
func (m *TemplatePoolManager) warmUpAll(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for templateName, pool := range m.pools {
		pool.mu.RLock()
		needed := pool.config.TargetSize - int32(len(pool.available)+len(pool.inUse)) - pool.creating
		pool.mu.RUnlock()

		if needed <= 0 {
			continue
		}

		klog.Infof("Warming up template pool %s: need %d sandboxes", templateName, needed)
		for i := int32(0); i < needed; i++ {
			go m.createPooledSandbox(ctx, templateName)
		}
	}
}

// replenishAll replenishes pools below their minimum size.
func (m *TemplatePoolManager) replenishAll(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for templateName, pool := range m.pools {
		pool.mu.RLock()
		available := int32(len(pool.available))
		inUse := int32(len(pool.inUse))
		creating := pool.creating
		pool.mu.RUnlock()

		total := available + inUse + creating
		if total < pool.config.MinSize {
			needed := pool.config.MinSize - total
			klog.V(4).Infof("Replenishing template pool %s: need %d more", templateName, needed)
			for i := int32(0); i < needed; i++ {
				go m.createPooledSandbox(ctx, templateName)
			}
		}
	}
}

// createPooledSandbox creates a sandbox for the template pool.
func (m *TemplatePoolManager) createPooledSandbox(ctx context.Context, templateName string) {
	m.mu.RLock()
	pool, exists := m.pools[templateName]
	config := m.configs[templateName]
	m.mu.RUnlock()

	if !exists || config == nil {
		return
	}

	pool.mu.Lock()
	pool.creating++
	pool.mu.Unlock()

	defer func() {
		pool.mu.Lock()
		pool.creating--
		pool.mu.Unlock()
	}()

	// Build spec for pooled sandbox (would normally apply template here)
	spec := &RuntimeSpec{
		SandboxName: fmt.Sprintf("pool-%s-%d", templateName, time.Now().UnixNano()),
		Namespace:   "nexusbox-system",
		TenantName:  "system",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU:    "500m",
			Memory: "512Mi",
		},
	}

	start := time.Now()

	provider, exists := m.runtimeManager.GetProvider(spec.RuntimeType)
	if !exists {
		klog.Warningf("No provider for runtime type %s", spec.RuntimeType)
		return
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		klog.Warningf("Failed to create pooled sandbox for template %s: %v", templateName, err)
		return
	}

	elapsed := time.Since(start)

	pool.mu.Lock()
	if int32(len(pool.available)) < pool.config.MaxSize {
		pool.available = append(pool.available, TemplatePoolEntry{
			Handle:      handle,
			CreatedAt:   time.Now(),
			LastUsedAt:  time.Now(),
			TemplateRef: templateName,
		})
		pool.stats.TotalCreated++
		if pool.stats.TotalCreated > 0 {
			pool.stats.AvgCreateTime = (pool.stats.AvgCreateTime*time.Duration(pool.stats.TotalCreated-1) + elapsed) / time.Duration(pool.stats.TotalCreated)
		}
	} else {
		handle.Cleanup(ctx)
	}
	pool.mu.Unlock()

	klog.V(4).Infof("Created pooled sandbox for template %s (time=%v)", templateName, elapsed)
}

// evictExpired removes sandboxes that have exceeded their TTL.
func (m *TemplatePoolManager) evictExpired() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	for templateName, pool := range m.pools {
		pool.mu.Lock()
		var keep []TemplatePoolEntry
		evicted := 0
		for _, entry := range pool.available {
			if now.Sub(entry.CreatedAt) > pool.config.TTL {
				entry.Handle.Cleanup(context.Background())
				pool.stats.TotalExpired++
				evicted++
			} else {
				keep = append(keep, entry)
			}
		}
		pool.available = keep
		pool.mu.Unlock()

		if evicted > 0 {
			klog.V(4).Infof("Evicted %d expired sandboxes from template pool %s", evicted, templateName)
		}
	}
}

// scalePools adjusts pool sizes based on utilization.
func (m *TemplatePoolManager) scalePools(ctx context.Context) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for templateName, pool := range m.pools {
		pool.mu.RLock()
		available := int32(len(pool.available))
		inUse := int32(len(pool.inUse))
		total := available + inUse
		pool.mu.RUnlock()

		if total == 0 {
			continue
		}

		utilization := int32((inUse * 100) / total)

		// Scale up if utilization is high
		if utilization > pool.config.ScaleUpThreshold && total < pool.config.MaxSize {
			klog.Infof("Scaling up template pool %s (utilization=%d%%)", templateName, utilization)
			go m.createPooledSandbox(ctx, templateName)
		}

		// Scale down is handled by evictExpired (TTL-based)
	}
}

// updateStats updates hit rate statistics.
func (m *TemplatePoolManager) updateStats() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, pool := range m.pools {
		pool.mu.Lock()
		total := pool.stats.TotalReused + pool.stats.TotalCreated
		if total > 0 {
			pool.stats.HitRate = float64(pool.stats.TotalReused) / float64(total)
		}
		pool.mu.Unlock()
	}
}

// drainAll drains all pools during shutdown.
func (m *TemplatePoolManager) drainAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctx := context.Background()
	for templateName, pool := range m.pools {
		pool.mu.Lock()
		for _, entry := range pool.available {
			entry.Handle.Cleanup(ctx)
		}
		pool.available = nil
		for key, entry := range pool.inUse {
			entry.Handle.Cleanup(ctx)
			delete(pool.inUse, key)
		}
		pool.mu.Unlock()
		klog.Infof("Drained template pool %s", templateName)
	}
}

// GetPoolStats returns statistics for all template pools.
func (m *TemplatePoolManager) GetPoolStats() map[string]*TemplatePoolStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*TemplatePoolStats)
	for name, pool := range m.pools {
		pool.mu.RLock()
		statsCopy := pool.stats
		pool.mu.RUnlock()
		result[name] = &statsCopy
	}
	return result
}

// GetPoolSize returns the current size of a template pool.
func (m *TemplatePoolManager) GetPoolSize(templateName string) (available, inUse int) {
	m.mu.RLock()
	pool, exists := m.pools[templateName]
	m.mu.RUnlock()

	if !exists {
		return 0, 0
	}

	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return len(pool.available), len(pool.inUse)
}
