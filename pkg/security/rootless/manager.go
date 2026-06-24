/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package rootless implements user namespace uid/gid mapping for rootless
// sandbox execution. This allows the sandbox daemon to run as a non-root user
// while still creating containers that appear to run as root inside their
// own user namespace.
//
// Key concepts:
//   - The host user (e.g. uid 1000) is mapped to uid 0 inside the sandbox
//   - A contiguous range of uids/gids is mapped 1:1 (or with an offset)
//   - /etc/subuid and /etc/subgid define the available mapping ranges
//   - The kernel enforces these mappings; the host user cannot escalate
//     beyond its delegated range
//
// Reference: user_namespaces(7), subuid(5).
package rootless

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// Default mapping sizes
const (
	// defaultSubuidSize is the default number of uids/gids to map.
	// 65536 covers the typical Linux uid range (0-65535).
	defaultSubuidSize = 65536

	// rootlessUID is the uid that maps to root (0) inside the namespace.
	rootlessUID = 0
)

// IDMapping describes a single uid or gid mapping.
type IDMapping struct {
	// ContainerID is the starting id inside the container namespace.
	ContainerID uint32 `json:"containerId"`
	// HostID is the starting id on the host.
	HostID uint32 `json:"hostId"`
	// Size is the number of ids mapped.
	Size uint32 `json:"size"`
}

// UserNamespaceConfig holds the user namespace mapping configuration.
type UserNamespaceConfig struct {
	// Enabled indicates whether user namespace remapping is active.
	Enabled bool `json:"enabled"`
	// UIDMappings are the uid mappings (LinuxNamespace.UIDMappings).
	UIDMappings []IDMapping `json:"uidMappings"`
	// GIDMappings are the gid mappings (LinuxNamespace.GIDMappings).
	GIDMappings []IDMapping `json:"gidMappings"`
}

// Manager handles rootless configuration and uid/gid mapping resolution.
type Manager struct {
	mu sync.RWMutex

	// currentUser is the host user running the daemon.
	currentUser *user.User
	// subuidRange is the delegated uid range from /etc/subuid.
	subuidRange *idRange
	// subgidRange is the delegated gid range from /etc/subgid.
	subgidRange *idRange
	// mappingSize is the number of ids to map.
	mappingSize uint32
}

// idRange represents a contiguous range of ids delegated to a user.
type idRange struct {
	Start uint32
	Size  uint32
}

// NewManager creates a rootless manager for the current host user.
// If /etc/subuid and /etc/subgid are not configured, rootless mode is disabled
// and the manager returns an empty (disabled) config.
func NewManager() (*Manager, error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}

	m := &Manager{
		currentUser: currentUser,
		mappingSize: defaultSubuidSize,
	}

	// Parse /etc/subuid for the delegated uid range
	if r, err := parseSubidFile("/etc/subuid", currentUser.Username); err != nil {
		klog.Warningf("Rootless: /etc/subuid not configured for user %s: %v (rootless disabled)",
			currentUser.Username, err)
	} else {
		m.subuidRange = r
	}

	// Parse /etc/subgid for the delegated gid range
	if r, err := parseSubidFile("/etc/subgid", currentUser.Username); err != nil {
		klog.Warningf("Rootless: /etc/subgid not configured for user %s: %v (rootless disabled)",
			currentUser.Username, err)
	} else {
		m.subgidRange = r
	}

	if m.subuidRange != nil && m.subgidRange != nil {
		m.mappingSize = min(defaultSubuidSize, m.subuidRange.Size, m.subgidRange.Size)
		klog.Infof("Rootless: enabled for user %s (uid range %d-%d, size %d)",
			currentUser.Username, m.subuidRange.Start, m.subuidRange.Start+m.mappingSize-1, m.mappingSize)
	} else {
		klog.Infof("Rootless: disabled (no subuid/subgid delegation for user %s)", currentUser.Username)
	}

	return m, nil
}

// Config returns the user namespace configuration for OCI spec generation.
// Returns a disabled config if rootless is not available.
func (m *Manager) Config() UserNamespaceConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.subuidRange == nil || m.subgidRange == nil {
		return UserNamespaceConfig{Enabled: false}
	}

	// Standard mapping: container uid 0 -> host subuid start
	// This makes the sandbox appear as root inside its namespace while
	// actually running as the delegated unprivileged range on the host.
	return UserNamespaceConfig{
		Enabled: true,
		UIDMappings: []IDMapping{
			{
				ContainerID: rootlessUID,
				HostID:      m.subuidRange.Start,
				Size:        m.mappingSize,
			},
		},
		GIDMappings: []IDMapping{
			{
				ContainerID: rootlessUID,
				HostID:      m.subgidRange.Start,
				Size:        m.mappingSize,
			},
		},
	}
}

// IsEnabled returns whether rootless mode is available.
func (m *Manager) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.subuidRange != nil && m.subgidRange != nil
}

// HostUIDForContainerUID translates a container uid to the host uid.
// Returns 0 and an error if the mapping is not configured or out of range.
func (m *Manager) HostUIDForContainerUID(containerUID uint32) (uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.subuidRange == nil {
		return 0, fmt.Errorf("rootless not configured")
	}
	cfg := m.Config()
	for _, mapping := range cfg.UIDMappings {
		if containerUID >= mapping.ContainerID && containerUID < mapping.ContainerID+mapping.Size {
			return mapping.HostID + (containerUID - mapping.ContainerID), nil
		}
	}
	return 0, fmt.Errorf("container uid %d not in any mapping", containerUID)
}

// HostGIDForContainerGID translates a container gid to the host gid.
func (m *Manager) HostGIDForContainerGID(containerGID uint32) (uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.subgidRange == nil {
		return 0, fmt.Errorf("rootless not configured")
	}
	cfg := m.Config()
	for _, mapping := range cfg.GIDMappings {
		if containerGID >= mapping.ContainerID && containerGID < mapping.ContainerID+mapping.Size {
			return mapping.HostID + (containerGID - mapping.ContainerID), nil
		}
	}
	return 0, fmt.Errorf("container gid %d not in any mapping", containerGID)
}

// parseSubidFile parses /etc/subuid or /etc/subgid for the given user.
// File format: <username>:<start>:<size>
// Example: sandbox:100000:65536
func parseSubidFile(path, username string) (*idRange, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			continue
		}
		if parts[0] != username {
			continue
		}
		start, err := strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid start in %s: %w", path, err)
		}
		size, err := strconv.ParseUint(parts[2], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid size in %s: %w", path, err)
		}
		if size == 0 {
			return nil, fmt.Errorf("zero-size range in %s for user %s", path, username)
		}
		return &idRange{Start: uint32(start), Size: uint32(size)}, nil
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return nil, fmt.Errorf("user %s not found in %s", username, path)
}

// min returns the minimum of multiple uint32 values.
func min(vals ...uint32) uint32 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
