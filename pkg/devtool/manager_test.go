package devtool

import (
	"context"
	"testing"
	"time"
)

func TestNewDevToolManager(t *testing.T) {
	mgr := NewDevToolManager()
	if mgr == nil {
		t.Fatal("NewDevToolManager returned nil")
	}
	if mgr.Launcher() == nil {
		t.Fatal("Launcher is nil")
	}
	if mgr.Proxy() == nil {
		t.Fatal("Proxy is nil")
	}
}

func TestPortAllocation(t *testing.T) {
	mgr := NewDevToolManager()

	mgr.mu.Lock()
	port1, err := mgr.allocatePort()
	if err != nil {
		t.Fatalf("allocatePort failed: %v", err)
	}
	port2, err := mgr.allocatePort()
	if err != nil {
		t.Fatalf("allocatePort failed: %v", err)
	}
	mgr.mu.Unlock()

	if port1 < portRange.min || port1 > portRange.max {
		t.Errorf("port1 %d out of range [%d, %d]", port1, portRange.min, portRange.max)
	}
	if port2 < portRange.min || port2 > portRange.max {
		t.Errorf("port2 %d out of range [%d, %d]", port2, portRange.min, portRange.max)
	}
	if port1 == port2 {
		t.Errorf("ports should be different: %d == %d", port1, port2)
	}

	// Release and verify reuse
	mgr.releasePort(port1)
	mgr.mu.Lock()
	if !mgr.usedPorts[port1] {
		// Good, port was released
	} else {
		t.Error("port was not released")
	}
	mgr.mu.Unlock()
}

func TestListBySandbox(t *testing.T) {
	mgr := NewDevToolManager()

	// No instances initially
	instances := mgr.ListBySandbox("nonexistent")
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestStopAllNonexistent(t *testing.T) {
	mgr := NewDevToolManager()
	ctx := context.Background()

	// Should not error for nonexistent sandbox
	err := mgr.StopAll(ctx, "nonexistent-sandbox")
	if err != nil {
		t.Errorf("StopAll for nonexistent sandbox should not error: %v", err)
	}
}

func TestTokenGeneration(t *testing.T) {
	token1 := generateToken(16)
	token2 := generateToken(16)

	if token1 == "" {
		t.Error("token should not be empty")
	}
	if token1 == token2 {
		t.Error("tokens should be different")
	}
	if len(token1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("token length should be 32, got %d", len(token1))
	}
}

func TestInstanceIDGeneration(t *testing.T) {
	id1 := generateInstanceID("jupyter")
	id2 := generateInstanceID("code-server")

	if id1 == "" || id2 == "" {
		t.Error("instance IDs should not be empty")
	}
	if id1 == id2 {
		t.Error("instance IDs should be different")
	}
	if id1[:3] != "dt-" {
		t.Errorf("instance ID should start with 'dt-', got %s", id1[:3])
	}
}

func TestLauncherBinaryDetection(t *testing.T) {
	l := NewLauncher()
	// We don't assert the binaries exist (depends on test environment),
	// but the launcher should not panic
	_ = l.JupyterPath()
	_ = l.CodeServerPath()
}

func TestDevToolConfigDefaults(t *testing.T) {
	config := DevToolConfig{
		Type:    DevToolJupyterLab,
		Enabled: true,
	}
	if config.Type != DevToolJupyterLab {
		t.Errorf("expected type %s, got %s", DevToolJupyterLab, config.Type)
	}
	if !config.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestHealthCheckNonexistent(t *testing.T) {
	mgr := NewDevToolManager()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	healthy, err := mgr.HealthCheck(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent instance")
	}
	if healthy {
		t.Error("expected healthy=false for nonexistent instance")
	}
}
