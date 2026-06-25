package runtime

import (
	"context"
	"testing"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// fakeHandle is a test RuntimeHandle implementation.
type fakeHandle struct {
	id      string
	cleaned bool
}

func (f *fakeHandle) ID() string                          { return f.id }
func (f *fakeHandle) IsReady() bool                       { return true }
func (f *fakeHandle) GetSpec() *RuntimeSpec               { return &RuntimeSpec{SandboxName: f.id} }
func (f *fakeHandle) ForceStop(ctx context.Context) error { return nil }
func (f *fakeHandle) Cleanup(ctx context.Context) error {
	f.cleaned = true
	return nil
}

func TestNewTemplatePoolManager(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	if m == nil {
		t.Fatal("NewTemplatePoolManager returned nil")
	}
	if len(m.pools) != 0 {
		t.Fatalf("expected empty pools, got %d", len(m.pools))
	}
}

func TestTemplatePoolManager_RegisterTemplatePool_RequiresName(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())

	if err := m.RegisterTemplatePool(&TemplatePoolConfig{}); err == nil {
		t.Fatal("expected error for missing template name")
	}
}

func TestTemplatePoolManager_RegisterTemplatePool_DefaultsApplied(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())

	cfg := &TemplatePoolConfig{TemplateName: "python"}
	if err := m.RegisterTemplatePool(cfg); err != nil {
		t.Fatalf("RegisterTemplatePool failed: %v", err)
	}

	// Verify defaults were applied
	if cfg.TargetSize != 3 {
		t.Errorf("expected TargetSize=3, got %d", cfg.TargetSize)
	}
	if cfg.MinSize != 1 {
		t.Errorf("expected MinSize=1, got %d", cfg.MinSize)
	}
	if cfg.MaxSize != 10 {
		t.Errorf("expected MaxSize=10, got %d", cfg.MaxSize)
	}
	if cfg.ScaleUpThreshold != 80 {
		t.Errorf("expected ScaleUpThreshold=80, got %d", cfg.ScaleUpThreshold)
	}
	if cfg.ScaleDownThreshold != 20 {
		t.Errorf("expected ScaleDownThreshold=20, got %d", cfg.ScaleDownThreshold)
	}
	if cfg.TTL != 30*time.Minute {
		t.Errorf("expected TTL=30m, got %s", cfg.TTL)
	}

	// Verify pool created
	avail, inUse := m.GetPoolSize("python")
	if avail != 0 || inUse != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", avail, inUse)
	}
}

func TestTemplatePoolManager_Acquire_EmptyPool(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	_ = m.RegisterTemplatePool(&TemplatePoolConfig{TemplateName: "test"})

	// Acquire from empty pool should return nil
	if h := m.Acquire("test", "sb-1"); h != nil {
		t.Errorf("expected nil handle from empty pool, got %v", h)
	}
}

func TestTemplatePoolManager_Acquire_MissingPool(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())

	if h := m.Acquire("missing", "sb-1"); h != nil {
		t.Errorf("expected nil handle for missing pool, got %v", h)
	}
}

func TestTemplatePoolManager_Acquire_Release_Recycle(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	_ = m.RegisterTemplatePool(&TemplatePoolConfig{TemplateName: "test", MaxSize: 5})

	// Manually inject a pre-warmed entry
	pool := m.pools["test"]
	pool.mu.Lock()
	pool.available = append(pool.available, TemplatePoolEntry{
		Handle:      &fakeHandle{id: "warm-1"},
		CreatedAt:   time.Now(),
		LastUsedAt:  time.Now(),
		TemplateRef: "test",
	})
	pool.mu.Unlock()

	// Acquire should return the handle
	h := m.Acquire("test", "sb-1")
	if h == nil {
		t.Fatal("expected handle, got nil")
	}
	if h.ID() != "warm-1" {
		t.Errorf("expected ID=warm-1, got %s", h.ID())
	}

	// Pool should now have 1 in-use, 0 available
	avail, inUse := m.GetPoolSize("test")
	if avail != 0 || inUse != 1 {
		t.Errorf("expected (0,1), got (%d,%d)", avail, inUse)
	}

	// Release with recycle=true should return to pool
	m.Release("test", "sb-1", true)

	avail, inUse = m.GetPoolSize("test")
	if avail != 1 || inUse != 0 {
		t.Errorf("expected (1,0) after recycle, got (%d,%d)", avail, inUse)
	}
}

func TestTemplatePoolManager_Release_NoRecycle(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	_ = m.RegisterTemplatePool(&TemplatePoolConfig{TemplateName: "test", MaxSize: 5})

	handle := &fakeHandle{id: "warm-2"}
	pool := m.pools["test"]
	pool.mu.Lock()
	pool.available = append(pool.available, TemplatePoolEntry{
		Handle:     handle,
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	})
	pool.mu.Unlock()

	_ = m.Acquire("test", "sb-2")

	// Release with recycle=false should call Cleanup
	m.Release("test", "sb-2", false)

	if !handle.cleaned {
		t.Error("expected Cleanup to be called when recycle=false")
	}

	avail, inUse := m.GetPoolSize("test")
	if avail != 0 || inUse != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", avail, inUse)
	}
}

func TestTemplatePoolManager_Release_MissingPool(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())

	// Should be no-op, not panic
	m.Release("missing", "sb-1", true)
}

func TestTemplatePoolManager_Release_MissingKey(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	_ = m.RegisterTemplatePool(&TemplatePoolConfig{TemplateName: "test"})

	// Release unknown key should be no-op
	m.Release("test", "unknown", true)

	avail, inUse := m.GetPoolSize("test")
	if avail != 0 || inUse != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", avail, inUse)
	}
}

func TestTemplatePoolManager_GetPoolStats(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())
	_ = m.RegisterTemplatePool(&TemplatePoolConfig{TemplateName: "stats-test"})

	pool := m.pools["stats-test"]
	pool.mu.Lock()
	pool.stats.TotalCreated = 10
	pool.stats.TotalReused = 5
	pool.stats.TotalEvicted = 2
	pool.mu.Unlock()

	statsMap := m.GetPoolStats()
	if len(statsMap) != 1 {
		t.Fatalf("expected 1 stat entry, got %d", len(statsMap))
	}
	stats := statsMap["stats-test"]
	if stats == nil {
		t.Fatal("expected stats, got nil")
	}
	if stats.TotalCreated != 10 {
		t.Errorf("expected TotalCreated=10, got %d", stats.TotalCreated)
	}
	if stats.TotalReused != 5 {
		t.Errorf("expected TotalReused=5, got %d", stats.TotalReused)
	}
}

func TestTemplatePoolManager_GetPoolSize_MissingPool(t *testing.T) {
	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    DefaultRuntimeManagerConfig(),
		stopCh:    make(chan struct{}),
	}
	m := NewTemplatePoolManager(rm, DefaultRuntimeManagerConfig())

	avail, inUse := m.GetPoolSize("missing")
	if avail != 0 || inUse != 0 {
		t.Errorf("expected (0,0), got (%d,%d)", avail, inUse)
	}
}
