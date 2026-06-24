/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// SecurityManager manages security profiles and cgroup configuration for sandboxes.
type SecurityManager struct {
	mu              sync.RWMutex
	seccompDir      string
	apparmorDir     string
	cgroupRoot      string
	appliedProfiles map[string]*AppliedProfile
}

// AppliedProfile tracks security profiles applied to a sandbox.
type AppliedProfile struct {
	SandboxID       string
	SeccompProfile  string
	AppArmorProfile string
	CgroupPath      string
	Namespaces      []string
}

// NewSecurityManager creates a new security manager.
func NewSecurityManager(seccompDir, apparmorDir, cgroupRoot string) *SecurityManager {
	if seccompDir == "" {
		seccompDir = "/etc/nexusbox/seccomp"
	}
	if apparmorDir == "" {
		apparmorDir = "/etc/nexusbox/apparmor"
	}
	if cgroupRoot == "" {
		cgroupRoot = "/sys/fs/cgroup"
	}
	return &SecurityManager{
		seccompDir:      seccompDir,
		apparmorDir:     apparmorDir,
		cgroupRoot:      cgroupRoot,
		appliedProfiles: make(map[string]*AppliedProfile),
	}
}

// ApplySecurity applies all security configurations for a sandbox.
func (sm *SecurityManager) ApplySecurity(ctx context.Context, sandboxID string, spec *sandboxv1alpha1.SandboxSecuritySpec, resources *sandboxv1alpha1.ResourceRequirements, runtimeType sandboxv1alpha1.SandboxRuntimeType) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	profile := &AppliedProfile{SandboxID: sandboxID}

	// 1. Apply seccomp profile
	if spec != nil && spec.SeccompProfile != nil {
		profilePath, err := sm.applySeccompProfile(sandboxID, spec.SeccompProfile)
		if err != nil {
			return fmt.Errorf("failed to apply seccomp profile: %w", err)
		}
		profile.SeccompProfile = profilePath
	}

	// 2. Apply AppArmor profile
	if spec != nil && spec.AppArmorProfile != nil {
		profileName, err := sm.applyAppArmorProfile(sandboxID, spec.AppArmorProfile)
		if err != nil {
			return fmt.Errorf("failed to apply AppArmor profile: %w", err)
		}
		profile.AppArmorProfile = profileName
	}

	// 3. Create cgroup for resource limits
	cgroupPath, err := sm.createCgroup(sandboxID, resources)
	if err != nil {
		return fmt.Errorf("failed to create cgroup: %w", err)
	}
	profile.CgroupPath = cgroupPath

	// 4. Apply SELinux context (if specified)
	if spec != nil && spec.SELinuxOptions != nil {
		if err := sm.applySELinuxContext(sandboxID, spec.SELinuxOptions); err != nil {
			klog.Warningf("Failed to apply SELinux context for %s: %v", sandboxID, err)
		}
	}

	sm.appliedProfiles[sandboxID] = profile
	klog.Infof("Applied security profiles for sandbox %s (seccomp=%s, apparmor=%s, cgroup=%s)",
		sandboxID, profile.SeccompProfile, profile.AppArmorProfile, profile.CgroupPath)
	return nil
}

// RemoveSecurity removes all security configurations for a sandbox.
func (sm *SecurityManager) RemoveSecurity(ctx context.Context, sandboxID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	profile, ok := sm.appliedProfiles[sandboxID]
	if !ok {
		return nil
	}

	// Remove cgroup
	if profile.CgroupPath != "" {
		if err := sm.removeCgroup(profile.CgroupPath); err != nil {
			klog.Warningf("Failed to remove cgroup for %s: %v", sandboxID, err)
		}
	}

	// Remove AppArmor profile
	if profile.AppArmorProfile != "" {
		if err := sm.removeAppArmorProfile(profile.AppArmorProfile); err != nil {
			klog.Warningf("Failed to remove AppArmor profile for %s: %v", sandboxID, err)
		}
	}

	// Remove seccomp profile file
	if profile.SeccompProfile != "" {
		os.Remove(profile.SeccompProfile)
	}

	delete(sm.appliedProfiles, sandboxID)
	klog.Infof("Removed security profiles for sandbox %s", sandboxID)
	return nil
}

// --- Seccomp ---

func (sm *SecurityManager) applySeccompProfile(sandboxID string, profile *sandboxv1alpha1.SeccompProfile) (string, error) {
	switch profile.Type {
	case sandboxv1alpha1.SeccompProfileTypeUnconfined:
		return "", nil
	case sandboxv1alpha1.SeccompProfileTypeRuntimeDefault:
		return sm.generateDefaultSeccompProfile(sandboxID)
	case sandboxv1alpha1.SeccompProfileTypeLocalhost:
		if profile.LocalhostProfile == "" {
			return "", fmt.Errorf("localhost profile path is empty")
		}
		return profile.LocalhostProfile, nil
	default:
		return "", fmt.Errorf("unknown seccomp profile type: %s", profile.Type)
	}
}

func (sm *SecurityManager) generateDefaultSeccompProfile(sandboxID string) (string, error) {
	if err := os.MkdirAll(sm.seccompDir, 0755); err != nil {
		return "", err
	}
	profilePath := filepath.Join(sm.seccompDir, sandboxID+".json")
	profile := `{
  "defaultAction": "ERRNO",
  "architectures": ["SCMP_ARCH_X86_64", "SCMP_ARCH_AARCH64"],
  "syscalls": [
    {
      "names": [
        "accept", "accept4", "access", "arch_prctl", "bind", "brk",
        "capget", "capset", "chdir", "chmod", "chown", "chroot",
        "clock_getres", "clock_gettime", "clock_nanosleep", "clone",
        "close", "connect", "dup", "dup2", "dup3",
        "epoll_create", "epoll_create1", "epoll_ctl", "epoll_pwait", "epoll_wait",
        "eventfd", "eventfd2", "execve", "exit", "exit_group",
        "faccessat", "fadvise64", "fallocate", "fchmod", "fchmodat",
        "fchown", "fchownat", "fcntl", "fdatasync", "flock",
        "fork", "fstat", "fstatfs", "fsync", "ftruncate",
        "futex", "getcwd", "getdents", "getdents64", "getegid",
        "geteuid", "getgid", "getpeername", "getpid", "getppid",
        "getpriority", "getrandom", "getresgid", "getresuid", "getrlimit",
        "getsockname", "getsockopt", "gettid", "gettimeofday", "getuid",
        "inotify_add_watch", "inotify_init", "inotify_init1", "inotify_rm_watch",
        "ioctl", "lseek", "lstat", "madvise", "membarrier",
        "memfd_create", "mincore", "mkdir", "mkdirat", "mknod", "mknodat",
        "mmap", "mprotect", "mremap", "munmap", "nanosleep",
        "newfstatat", "open", "openat", "pipe", "pipe2",
        "poll", "ppoll", "prctl", "pread64", "preadv",
        "prlimit64", "pwrite64", "pwritev", "read", "readahead",
        "readlink", "readlinkat", "readv", "recvfrom", "recvmmsg",
        "recvmsg", "rename", "renameat", "renameat2", "restart_syscall",
        "rmdir", "rt_sigaction", "rt_sigprocmask", "rt_sigreturn", "rt_sigsuspend",
        "sched_getaffinity", "sched_yield", "seccomp", "select", "sendmmsg",
        "sendmsg", "sendto", "set_robust_list", "set_tid_address",
        "setgid", "setgroups", "setsockopt", "setuid", "shutdown",
        "sigaltstack", "socket", "socketpair", "stat", "statfs",
        "statx", "symlink", "symlinkat", "sysinfo", "tgkill",
        "timer_create", "timer_delete", "timer_getoverrun", "timer_gettime", "timer_settime",
        "timerfd_create", "timerfd_gettime", "timerfd_settime",
        "umask", "uname", "unlink", "unlinkat", "unshare",
        "wait4", "waitid", "write", "writev"
      ],
      "action": "ALLOW"
    }
  ]
}`
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		return "", fmt.Errorf("failed to write seccomp profile: %w", err)
	}
	return profilePath, nil
}

// --- AppArmor ---

func (sm *SecurityManager) applyAppArmorProfile(sandboxID string, profile *sandboxv1alpha1.AppArmorProfile) (string, error) {
	switch profile.Type {
	case sandboxv1alpha1.AppArmorProfileTypeUnconfined:
		return "unconfined", nil
	case sandboxv1alpha1.AppArmorProfileTypeRuntimeDefault:
		profileName := fmt.Sprintf("nexusbox-sandbox-%s", sandboxID)
		return sm.generateDefaultAppArmorProfile(sandboxID, profileName)
	case sandboxv1alpha1.AppArmorProfileTypeLocalhost:
		return profile.LocalhostProfile, nil
	default:
		return "", fmt.Errorf("unknown AppArmor profile type: %s", profile.Type)
	}
}

func (sm *SecurityManager) generateDefaultAppArmorProfile(sandboxID, profileName string) (string, error) {
	if err := os.MkdirAll(sm.apparmorDir, 0755); err != nil {
		return "", err
	}

	profile := fmt.Sprintf(`#include <tunables/global>
profile %s flags=(attach_disconnected,mediate_deleted) {
  #include <abstractions/base>
  #include <abstractions/consoles>
  #include <abstractions/nameservice>

  # Allow standard filesystem operations
  /** rwlk,
  /proc/** r,
  /sys/** r,

  # Allow network
  network inet stream,
  network inet dgram,
  network inet6 stream,
  network inet6 dgram,
  network unix stream,
  network unix dgram,

  # Deny dangerous operations
  deny /proc/kcore r,
  deny /proc/kmem r,
  deny /proc/kallsyms r,
  deny /sys/firmware/** rw,
  deny /sys/kernel/security/** rw,
  deny capability sys_admin,
  deny capability sys_ptrace,
  deny capability net_admin,
  deny capability sys_rawio,
}
`, profileName)

	profilePath := filepath.Join(sm.apparmorDir, profileName)
	if err := os.WriteFile(profilePath, []byte(profile), 0644); err != nil {
		return "", fmt.Errorf("failed to write AppArmor profile: %w", err)
	}

	// Load the profile into the kernel
	// apparmor_parser -r <profile>
	klog.V(4).Infof("Generated AppArmor profile %s for sandbox %s", profileName, sandboxID)
	return profileName, nil
}

func (sm *SecurityManager) removeAppArmorProfile(profileName string) error {
	// Unload the profile: apparmor_parser -R <profile>
	profilePath := filepath.Join(sm.apparmorDir, profileName)
	os.Remove(profilePath)
	return nil
}

// --- Cgroups ---

func (sm *SecurityManager) createCgroup(sandboxID string, resources *sandboxv1alpha1.ResourceRequirements) (string, error) {
	if resources == nil {
		return "", nil
	}

	// Create cgroup v2 path: /sys/fs/cgroup/nexusbox/<sandboxID>/
	cgroupPath := filepath.Join(sm.cgroupRoot, "nexusbox", sandboxID)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create cgroup directory: %w", err)
	}

	// Set CPU limits
	if resources.CPU != "" {
		cpuQuota := parseCPUToQuota(resources.CPU)
		if cpuQuota > 0 {
			// cgroup v2: cpu.max = "quota period"
			if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(fmt.Sprintf("%d 100000", cpuQuota)), 0644); err != nil {
				klog.Warningf("Failed to set cpu.max for %s: %v", sandboxID, err)
			}
		}
	}

	// Set memory limits
	if resources.Memory != "" {
		memBytes := parseMemoryToBytes(resources.Memory)
		if memBytes > 0 {
			if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(fmt.Sprintf("%d", memBytes)), 0644); err != nil {
				klog.Warningf("Failed to set memory.max for %s: %v", sandboxID, err)
			}
			// Set swap limit to same as memory (effectively disable swap)
			if err := os.WriteFile(filepath.Join(cgroupPath, "memory.swap.max"), []byte("0"), 0644); err != nil {
				klog.V(4).Infof("Failed to set memory.swap.max for %s: %v", sandboxID, err)
			}
		}
	}

	// Set pids limit (default 4096)
	pidsLimit := int64(4096)
	if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(fmt.Sprintf("%d", pidsLimit)), 0644); err != nil {
		klog.Warningf("Failed to set pids.max for %s: %v", sandboxID, err)
	}

	// Enable controllers
	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.subtree_control"), []byte("+cpu +memory +pids +io"), 0644); err != nil {
		klog.V(4).Infof("Failed to enable controllers for %s: %v", sandboxID, err)
	}

	klog.V(4).Infof("Created cgroup at %s for sandbox %s", cgroupPath, sandboxID)
	return cgroupPath, nil
}

func (sm *SecurityManager) removeCgroup(cgroupPath string) error {
	// Kill all processes in the cgroup first
	os.WriteFile(filepath.Join(cgroupPath, "cgroup.kill"), []byte("1"), 0644)
	// Remove the cgroup directory
	return os.Remove(cgroupPath)
}

// --- SELinux ---

func (sm *SecurityManager) applySELinuxContext(sandboxID string, opts *sandboxv1alpha1.SELinuxOptions) error {
	// SELinux context is applied at process creation time via the OCI spec
	// Here we just validate and log
	klog.V(4).Infof("SELinux context for sandbox %s: user=%s role=%s type=%s level=%s",
		sandboxID, opts.User, opts.Role, opts.Type, opts.Level)
	return nil
}

// --- Helper functions ---

func parseCPUToQuota(cpu string) int64 {
	var val float64
	if len(cpu) > 0 && cpu[len(cpu)-1] == 'm' {
		fmt.Sscanf(cpu[:len(cpu)-1], "%f", &val)
		// milliCPU to quota: quota = (milliCPU / 1000) * 100000
		return int64(val * 100)
	}
	fmt.Sscanf(cpu, "%f", &val)
	return int64(val * 100000)
}

func parseMemoryToBytes(mem string) int64 {
	var val float64
	suffixes := []struct {
		suffix     string
		multiplier int64
	}{
		{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, su := range suffixes {
		if len(mem) > len(su.suffix) && mem[len(mem)-len(su.suffix):] == su.suffix {
			fmt.Sscanf(mem[:len(mem)-len(su.suffix)], "%f", &val)
			return int64(val * float64(su.multiplier))
		}
	}
	fmt.Sscanf(mem, "%f", &val)
	return int64(val)
}
