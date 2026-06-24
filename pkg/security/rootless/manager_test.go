/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package rootless

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestManager creates a Manager with the given subuid/subgid ranges,
// bypassing the /etc/subuid and /etc/subgid file parsing.
// This is used for unit testing the mapping logic.
func newTestManager(subuidStart, subuidSize, subgidStart, subgidSize uint32) *Manager {
	m := &Manager{
		mappingSize: defaultSubuidSize,
	}
	if subuidSize > 0 {
		m.subuidRange = &idRange{Start: subuidStart, Size: subuidSize}
	}
	if subgidSize > 0 {
		m.subgidRange = &idRange{Start: subgidStart, Size: subgidSize}
	}
	// Recalculate mapping size based on the smallest available range
	if m.subuidRange != nil && m.subgidRange != nil {
		m.mappingSize = min(defaultSubuidSize, m.subuidRange.Size, m.subgidRange.Size)
	}
	return m
}

func TestParseSubidFile(t *testing.T) {
	// Create a temporary subuid file
	tmpDir := t.TempDir()
	subuidPath := filepath.Join(tmpDir, "subuid")

	content := `# comment line
root:0:65536
sandbox:100000:65536
testuser:200000:100000
`
	if err := os.WriteFile(subuidPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test subuid file: %v", err)
	}

	tests := []struct {
		name        string
		username    string
		wantStart   uint32
		wantSize    uint32
		wantErr     bool
	}{
		{
			name:      "existing user sandbox",
			username:  "sandbox",
			wantStart: 100000,
			wantSize:  65536,
			wantErr:   false,
		},
		{
			name:      "existing user testuser",
			username:  "testuser",
			wantStart: 200000,
			wantSize:  100000,
			wantErr:   false,
		},
		{
			name:      "root user",
			username:  "root",
			wantStart: 0,
			wantSize:  65536,
			wantErr:   false,
		},
		{
			name:     "nonexistent user",
			username: "ghost",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseSubidFile(subuidPath, tt.username)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for user %s, got none", tt.username)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Start != tt.wantStart {
				t.Errorf("start = %d, want %d", r.Start, tt.wantStart)
			}
			if r.Size != tt.wantSize {
				t.Errorf("size = %d, want %d", r.Size, tt.wantSize)
			}
		})
	}
}

func TestParseSubidFile_Malformed(t *testing.T) {
	tmpDir := t.TempDir()
	subuidPath := filepath.Join(tmpDir, "subuid")

	content := `# comment
sandbox:100000
invalid:abc:65536
sandbox:200000:0
`
	if err := os.WriteFile(subuidPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test subuid file: %v", err)
	}

	// "sandbox:100000" has only 2 fields, should be skipped
	// "invalid:abc:65536" has non-numeric start, should error
	// "sandbox:200000:0" has zero size, should error
	// The first valid "sandbox" entry is the malformed 2-field one, which is skipped,
	// then "invalid" line is parsed (but for user "invalid" not "sandbox"),
	// then "sandbox:200000:0" is found but has zero size.

	_, err := parseSubidFile(subuidPath, "sandbox")
	if err == nil {
		t.Fatal("expected error for zero-size range, got nil")
	}
}

func TestParseSubidFile_NotFound(t *testing.T) {
	_, err := parseSubidFile("/nonexistent/path/subuid", "sandbox")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestConfig_DisabledWhenNoRanges(t *testing.T) {
	m := newTestManager(0, 0, 0, 0)
	cfg := m.Config()

	if cfg.Enabled {
		t.Error("Config should be disabled when no subuid/subgid ranges are configured")
	}
	if len(cfg.UIDMappings) != 0 {
		t.Errorf("UIDMappings should be empty, got %d", len(cfg.UIDMappings))
	}
	if len(cfg.GIDMappings) != 0 {
		t.Errorf("GIDMappings should be empty, got %d", len(cfg.GIDMappings))
	}
}

func TestConfig_DisabledWhenOnlySubuid(t *testing.T) {
	m := newTestManager(100000, 65536, 0, 0)
	cfg := m.Config()

	if cfg.Enabled {
		t.Error("Config should be disabled when only subuid is configured (no subgid)")
	}
}

func TestConfig_DisabledWhenOnlySubgid(t *testing.T) {
	m := newTestManager(0, 0, 100000, 65536)
	cfg := m.Config()

	if cfg.Enabled {
		t.Error("Config should be disabled when only subgid is configured (no subuid)")
	}
}

func TestConfig_EnabledWithBothRanges(t *testing.T) {
	m := newTestManager(100000, 65536, 100000, 65536)
	cfg := m.Config()

	if !cfg.Enabled {
		t.Fatal("Config should be enabled when both subuid and subgid are configured")
	}

	if len(cfg.UIDMappings) != 1 {
		t.Fatalf("expected 1 UID mapping, got %d", len(cfg.UIDMappings))
	}
	if len(cfg.GIDMappings) != 1 {
		t.Fatalf("expected 1 GID mapping, got %d", len(cfg.GIDMappings))
	}

	uidMap := cfg.UIDMappings[0]
	if uidMap.ContainerID != 0 {
		t.Errorf("UID ContainerID = %d, want 0 (root inside namespace)", uidMap.ContainerID)
	}
	if uidMap.HostID != 100000 {
		t.Errorf("UID HostID = %d, want 100000", uidMap.HostID)
	}
	if uidMap.Size != 65536 {
		t.Errorf("UID Size = %d, want 65536", uidMap.Size)
	}

	gidMap := cfg.GIDMappings[0]
	if gidMap.ContainerID != 0 {
		t.Errorf("GID ContainerID = %d, want 0 (root inside namespace)", gidMap.ContainerID)
	}
	if gidMap.HostID != 100000 {
		t.Errorf("GID HostID = %d, want 100000", gidMap.HostID)
	}
	if gidMap.Size != 65536 {
		t.Errorf("GID Size = %d, want 65536", gidMap.Size)
	}
}

func TestConfig_MappingSizeUsesSmallestRange(t *testing.T) {
	// subuid has 65536, subgid has only 10000
	// mapping size should be 10000 (the smaller one)
	m := newTestManager(100000, 65536, 200000, 10000)
	cfg := m.Config()

	if !cfg.Enabled {
		t.Fatal("Config should be enabled")
	}

	if cfg.UIDMappings[0].Size != 10000 {
		t.Errorf("UID mapping size = %d, want 10000 (smallest range)", cfg.UIDMappings[0].Size)
	}
	if cfg.GIDMappings[0].Size != 10000 {
		t.Errorf("GID mapping size = %d, want 10000 (smallest range)", cfg.GIDMappings[0].Size)
	}
}

func TestConfig_MappingSizeCappedAtDefault(t *testing.T) {
	// Both ranges are huge, but mapping size should be capped at defaultSubuidSize (65536)
	m := newTestManager(100000, 1000000, 100000, 1000000)
	cfg := m.Config()

	if cfg.UIDMappings[0].Size != defaultSubuidSize {
		t.Errorf("UID mapping size = %d, want %d (capped at default)", cfg.UIDMappings[0].Size, defaultSubuidSize)
	}
}

func TestIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		subuid   uint32
		subuidSz uint32
		subgid   uint32
		subgidSz uint32
		want     bool
	}{
		{"both configured", 100000, 65536, 100000, 65536, true},
		{"only subuid", 100000, 65536, 0, 0, false},
		{"only subgid", 0, 0, 100000, 65536, false},
		{"neither", 0, 0, 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager(tt.subuid, tt.subuidSz, tt.subgid, tt.subgidSz)
			if m.IsEnabled() != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", m.IsEnabled(), tt.want)
			}
		})
	}
}

func TestHostUIDForContainerUID(t *testing.T) {
	m := newTestManager(100000, 65536, 100000, 65536)

	tests := []struct {
		name        string
		containerUID uint32
		wantHost     uint32
		wantErr      bool
	}{
		{"root maps to subuid start", 0, 100000, false},
		{"uid 1 maps to subuid+1", 1, 100001, false},
		{"uid 1000 maps to subuid+1000", 1000, 101000, false},
		{"last valid uid", 65535, 165535, false},
		{"uid out of range", 65536, 0, true},
		{"uid way out of range", 100000, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hostUID, err := m.HostUIDForContainerUID(tt.containerUID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for container uid %d, got none", tt.containerUID)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hostUID != tt.wantHost {
				t.Errorf("HostUIDForContainerUID(%d) = %d, want %d",
					tt.containerUID, hostUID, tt.wantHost)
			}
		})
	}
}

func TestHostGIDForContainerGID(t *testing.T) {
	m := newTestManager(100000, 65536, 200000, 65536)

	// GID mapping should use subgid range (200000), not subuid (100000)
	hostGID, err := m.HostGIDForContainerGID(0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hostGID != 200000 {
		t.Errorf("HostGIDForContainerGID(0) = %d, want 200000 (subgid start)", hostGID)
	}

	hostGID, err = m.HostGIDForContainerGID(500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hostGID != 200500 {
		t.Errorf("HostGIDForContainerGID(500) = %d, want 200500", hostGID)
	}
}

func TestHostUIDForContainerUID_NotConfigured(t *testing.T) {
	m := newTestManager(0, 0, 0, 0)
	_, err := m.HostUIDForContainerUID(0)
	if err == nil {
		t.Fatal("expected error when rootless not configured, got nil")
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		name string
		vals []uint32
		want uint32
	}{
		{"single value", []uint32{42}, 42},
		{"two values", []uint32{10, 20}, 10},
		{"three values", []uint32{30, 10, 20}, 10},
		{"with zero", []uint32{0, 10, 20}, 0},
		{"all same", []uint32{5, 5, 5}, 5},
		{"empty", []uint32{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := min(tt.vals...)
			if got != tt.want {
				t.Errorf("min(%v) = %d, want %d", tt.vals, got, tt.want)
			}
		})
	}
}

func TestNewManager_NoSubuidFile(t *testing.T) {
	// When /etc/subuid doesn't exist, NewManager should succeed but
	// rootless should be disabled.
	// Note: This test depends on the host system. On systems without
	// /etc/subuid, IsEnabled() will be false.
	m, err := NewManager()
	if err != nil {
		t.Fatalf("NewManager() failed: %v", err)
	}
	// We can't assert IsEnabled() here because it depends on the host,
	// but we can verify the manager was created without error.
	if m == nil {
		t.Fatal("NewManager() returned nil manager")
	}
}
