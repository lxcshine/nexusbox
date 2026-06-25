package runtime

import (
	"context"
	"testing"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

func TestRuntimeManager_GetConfig_ReturnsDeepCopy(t *testing.T) {
	rm := NewRuntimeManager(nil) // uses default config
	cfg := rm.GetConfig()

	// Mutate the returned copy; the manager's internal config must not change.
	cfg.CreateTimeout = 1 * time.Nanosecond
	cfg.PoolSize[sandboxv1alpha1.RuntimeRunc] = 999

	cfg2 := rm.GetConfig()
	if cfg2.CreateTimeout == 1*time.Nanosecond {
		t.Errorf("GetConfig did not return a copy: CreateTimeout leaked")
	}
	if cfg2.PoolSize[sandboxv1alpha1.RuntimeRunc] == 999 {
		t.Errorf("GetConfig did not deep-copy PoolSize map")
	}
}

func TestRuntimeManager_UpdateConfig_AppliesHotReloadableFields(t *testing.T) {
	rm := NewRuntimeManager(nil)
	original := rm.GetConfig()

	newCfg := &RuntimeManagerConfig{
		// Endpoints left empty -> must be preserved from the previous config.
		CreateTimeout:          7 * time.Second,
		StartTimeout:           8 * time.Second,
		StopTimeout:            9 * time.Second,
		PoolEnabled:            false,
		PoolRefreshInterval:    11 * time.Second,
		MaxConcurrentOperations: 42,
		PoolSize: map[sandboxv1alpha1.SandboxRuntimeType]int32{
			sandboxv1alpha1.RuntimeKataContainers: 1,
		},
	}
	if err := rm.UpdateConfig(newCfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	got := rm.GetConfig()
	if got.CreateTimeout != 7*time.Second {
		t.Errorf("CreateTimeout = %s, want 7s", got.CreateTimeout)
	}
	if got.MaxConcurrentOperations != 42 {
		t.Errorf("MaxConcurrentOperations = %d, want 42", got.MaxConcurrentOperations)
	}
	if got.PoolEnabled != false {
		t.Errorf("PoolEnabled = %v, want false", got.PoolEnabled)
	}
	if got.PoolSize[sandboxv1alpha1.RuntimeKataContainers] != 1 {
		t.Errorf("PoolSize[Kata] = %d, want 1", got.PoolSize[sandboxv1alpha1.RuntimeKataContainers])
	}
	// Endpoints preserved.
	if got.KataContainersEndpoint != original.KataContainersEndpoint {
		t.Errorf("KataContainersEndpoint was not preserved: got %q, want %q",
			got.KataContainersEndpoint, original.KataContainersEndpoint)
	}
}

func TestRuntimeManager_UpdateConfig_RejectsNil(t *testing.T) {
	rm := NewRuntimeManager(nil)
	if err := rm.UpdateConfig(nil); err == nil {
		t.Errorf("UpdateConfig(nil) returned nil error, want error")
	}
}

func TestRuntimeManager_UpdateConfig_ZeroFieldsInheritPrevious(t *testing.T) {
	rm := NewRuntimeManager(nil)
	prev := rm.GetConfig()

	// A config with all operational fields zeroed should inherit the previous
	// values, so a partial hot-reload can't silently disable safety limits.
	newCfg := &RuntimeManagerConfig{
		KataContainersEndpoint: prev.KataContainersEndpoint,
		GVisorEndpoint:         prev.GVisorEndpoint,
		RuncEndpoint:           prev.RuncEndpoint,
	}
	if err := rm.UpdateConfig(newCfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	got := rm.GetConfig()
	if got.CreateTimeout != prev.CreateTimeout {
		t.Errorf("CreateTimeout = %s, want previous %s", got.CreateTimeout, prev.CreateTimeout)
	}
	if got.MaxConcurrentOperations != prev.MaxConcurrentOperations {
		t.Errorf("MaxConcurrentOperations = %d, want previous %d", got.MaxConcurrentOperations, prev.MaxConcurrentOperations)
	}
}

func TestRuntimeManager_Reload_RejectsWrongType(t *testing.T) {
	rm := NewRuntimeManager(nil)
	if err := rm.Reload(context.Background(), "not a config"); err == nil {
		t.Errorf("Reload with wrong type returned nil error, want error")
	}
}

func TestRuntimeManager_Reload_AppliesConfig(t *testing.T) {
	rm := NewRuntimeManager(nil)
	cfg := &RuntimeManagerConfig{
		KataContainersEndpoint:  rm.GetConfig().KataContainersEndpoint,
		GVisorEndpoint:          rm.GetConfig().GVisorEndpoint,
		RuncEndpoint:            rm.GetConfig().RuncEndpoint,
		CreateTimeout:           3 * time.Second,
		MaxConcurrentOperations: 7,
	}
	if err := rm.Reload(context.Background(), cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got := rm.GetConfig()
	if got.CreateTimeout != 3*time.Second {
		t.Errorf("CreateTimeout = %s, want 3s", got.CreateTimeout)
	}
	if got.MaxConcurrentOperations != 7 {
		t.Errorf("MaxConcurrentOperations = %d, want 7", got.MaxConcurrentOperations)
	}
}

func TestRuntimeManager_Name(t *testing.T) {
	rm := NewRuntimeManager(nil)
	if rm.Name() != "runtime-manager" {
		t.Errorf("Name = %q, want runtime-manager", rm.Name())
	}
}
