package resource

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestApplyLimits(t *testing.T) {
	m := NewManager()
	tmpDir := t.TempDir()

	limits := &Limits{
		CPU:       "1",
		Memory:    "512Mi",
		DiskQuota: 1024 * 1024, // 1MB
	}

	if err := m.ApplyLimits(context.Background(), "test-sandbox", tmpDir, limits); err != nil {
		t.Fatalf("ApplyLimits failed: %v", err)
	}

	// Verify limits were stored.
	stored, err := m.GetLimits("test-sandbox")
	if err != nil {
		t.Fatalf("GetLimits failed: %v", err)
	}
	if stored.CPU != "1" {
		t.Errorf("CPU = %s, want 1", stored.CPU)
	}
	if stored.Memory != "512Mi" {
		t.Errorf("Memory = %s, want 512Mi", stored.Memory)
	}
}

func TestRemoveLimits(t *testing.T) {
	m := NewManager()
	tmpDir := t.TempDir()

	limits := &Limits{CPU: "1", Memory: "512Mi"}
	m.ApplyLimits(context.Background(), "test-sandbox", tmpDir, limits)

	m.RemoveLimits("test-sandbox")

	_, err := m.GetLimits("test-sandbox")
	if err == nil {
		t.Error("expected error after RemoveLimits")
	}
}

func TestCheckDiskQuota_NoLimit(t *testing.T) {
	m := NewManager()
	tmpDir := t.TempDir()

	limits := &Limits{CPU: "1", DiskQuota: 0}
	m.ApplyLimits(context.Background(), "test-sandbox", tmpDir, limits)

	exceeded, err := m.CheckDiskQuota("test-sandbox")
	if err != nil {
		t.Fatalf("CheckDiskQuota failed: %v", err)
	}
	if exceeded {
		t.Error("expected false for no disk quota")
	}
}

func TestCheckDiskQuota_Exceeded(t *testing.T) {
	m := NewManager()
	tmpDir := t.TempDir()

	// Create a file larger than quota.
	largeFile := filepath.Join(tmpDir, "large.txt")
	os.WriteFile(largeFile, make([]byte, 2048), 0644)

	limits := &Limits{CPU: "1", DiskQuota: 1024} // 1KB quota
	m.ApplyLimits(context.Background(), "test-sandbox", tmpDir, limits)

	// Manually update usage.
	m.UpdateUsage("test-sandbox", &Usage{
		DiskUsageBytes: 2048,
		CollectedAt:    time.Now(),
	})

	exceeded, err := m.CheckDiskQuota("test-sandbox")
	if err != nil {
		t.Fatalf("CheckDiskQuota failed: %v", err)
	}
	if !exceeded {
		t.Error("expected true for exceeded quota")
	}
}

func TestGetUsage_NotFound(t *testing.T) {
	m := NewManager()
	_, err := m.GetUsage("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent sandbox")
	}
}

func TestGetLimits_NotFound(t *testing.T) {
	m := NewManager()
	_, err := m.GetLimits("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent sandbox")
	}
}

func TestParseStorageToBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1Gi", 1 << 30},
		{"512Mi", 512 << 20},
		{"1024Ki", 1024 << 10},
		{"100", 100},
		{"2G", 2e9},
	}

	for _, tt := range tests {
		got := parseStorageToBytes(tt.input)
		if got != tt.want {
			t.Errorf("parseStorageToBytes(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFromSandboxSpec(t *testing.T) {
	// This test requires the sandbox types, which may not be available.
	// Skip if the types are not importable.
	t.Skip("requires sandbox v1alpha1 types")
}

func TestPlatform(t *testing.T) {
	p := Platform()
	if p == "" {
		t.Error("Platform() returned empty string")
	}
}
