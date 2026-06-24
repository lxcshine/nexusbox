/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package security

import (
	"testing"
)

func TestParseCPUToQuota(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1", 100000},
		{"2", 200000},
		{"500m", 50000},
		{"1000m", 100000},
	}

	for _, tt := range tests {
		got := parseCPUToQuota(tt.input)
		if got != tt.want {
			t.Errorf("parseCPUToQuota(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParseMemoryToBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1Gi", 1 << 30},
		{"512Mi", 512 << 20},
		{"1Ki", 1024},
	}

	for _, tt := range tests {
		got := parseMemoryToBytes(tt.input)
		if got != tt.want {
			t.Errorf("parseMemoryToBytes(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestNewSecurityManager(t *testing.T) {
	sm := NewSecurityManager("", "", "")
	if sm == nil {
		t.Fatal("NewSecurityManager returned nil")
	}
	if sm.seccompDir != "/etc/nexusbox/seccomp" {
		t.Errorf("seccompDir = %q, want %q", sm.seccompDir, "/etc/nexusbox/seccomp")
	}
	if sm.apparmorDir != "/etc/nexusbox/apparmor" {
		t.Errorf("apparmorDir = %q, want %q", sm.apparmorDir, "/etc/nexusbox/apparmor")
	}
	if sm.cgroupRoot != "/sys/fs/cgroup" {
		t.Errorf("cgroupRoot = %q, want %q", sm.cgroupRoot, "/sys/fs/cgroup")
	}
}
