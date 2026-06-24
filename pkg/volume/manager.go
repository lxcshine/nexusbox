package volume

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// VolumeManager manages volume mounting for sandboxes.
type VolumeManager struct {
	mu         sync.RWMutex
	baseDir    string
	mounts     map[string][]MountInfo // sandboxID -> mounts
}

// MountInfo tracks a mounted volume.
type MountInfo struct {
	Name      string
	Source    string
	Target    string
	Type      string
	ReadOnly  bool
}

// NewVolumeManager creates a new volume manager.
func NewVolumeManager(baseDir string) *VolumeManager {
	if baseDir == "" {
		baseDir = "/var/lib/nexusbox/volumes"
	}
	return &VolumeManager{
		baseDir: baseDir,
		mounts:  make(map[string][]MountInfo),
	}
}

// MountVolumes mounts all volumes for a sandbox.
func (vm *VolumeManager) MountVolumes(ctx context.Context, sandboxID string, spec *sandboxv1alpha1.SandboxStorageSpec) error {
	if spec == nil || len(spec.Volumes) == 0 {
		return nil
	}

	vm.mu.Lock()
	defer vm.mu.Unlock()

	var mountInfos []MountInfo
	for _, vol := range spec.Volumes {
		source, mountType, err := vm.prepareVolumeSource(sandboxID, &vol)
		if err != nil {
			// Rollback already mounted volumes
			for _, mi := range mountInfos {
				vm.unmountVolume(mi.Source)
			}
			return fmt.Errorf("failed to prepare volume %s: %w", vol.Name, err)
		}

		mountInfo := MountInfo{
			Name:     vol.Name,
			Source:   source,
			Target:   vol.MountPath,
			Type:     mountType,
			ReadOnly: vol.ReadOnly,
		}

		// Perform the mount
		if err := vm.mountVolume(source, sandboxID, vol.MountPath, mountType, vol.ReadOnly); err != nil {
			// Rollback
			for _, mi := range mountInfos {
				vm.unmountVolume(mi.Source)
			}
			return fmt.Errorf("failed to mount volume %s: %w", vol.Name, err)
		}

		mountInfos = append(mountInfos, mountInfo)
		klog.V(4).Infof("Mounted volume %s for sandbox %s at %s", vol.Name, sandboxID, vol.MountPath)
	}

	vm.mounts[sandboxID] = mountInfos
	return nil
}

// UnmountVolumes unmounts all volumes for a sandbox.
func (vm *VolumeManager) UnmountVolumes(ctx context.Context, sandboxID string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	mountInfos, ok := vm.mounts[sandboxID]
	if !ok {
		return nil
	}

	for _, mi := range mountInfos {
		if err := vm.unmountVolume(mi.Source); err != nil {
			klog.Warningf("Failed to unmount volume %s for sandbox %s: %v", mi.Name, sandboxID, err)
		}
	}

	delete(vm.mounts, sandboxID)
	klog.V(4).Infof("Unmounted all volumes for sandbox %s", sandboxID)
	return nil
}

// GetMounts returns the mount info for a sandbox.
func (vm *VolumeManager) GetMounts(sandboxID string) []MountInfo {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.mounts[sandboxID]
}

// prepareVolumeSource prepares the source directory/file for a volume.
func (vm *VolumeManager) prepareVolumeSource(sandboxID string, vol *sandboxv1alpha1.SandboxVolume) (string, string, error) {
	switch {
	case vol.VolumeSource.HostPath != nil:
		path := vol.VolumeSource.HostPath.Path
		if err := os.MkdirAll(path, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create host path %s: %w", path, err)
		}
		return path, "bind", nil

	case vol.VolumeSource.EmptyDir != nil:
		dir := filepath.Join(vm.baseDir, "emptydir", sandboxID, vol.Name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create emptydir %s: %w", dir, err)
		}
		// If memory-backed, mount tmpfs
		if vol.VolumeSource.EmptyDir.Medium == sandboxv1alpha1.StorageMediumMemory {
			return dir, "tmpfs", nil
		}
		return dir, "bind", nil

	case vol.VolumeSource.PVC != nil:
		// PVC volumes are handled by the CSI driver
		pvcPath := filepath.Join(vm.baseDir, "pvc", vol.VolumeSource.PVC.ClaimName)
		if err := os.MkdirAll(pvcPath, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create PVC mount %s: %w", pvcPath, err)
		}
		return pvcPath, "bind", nil

	case vol.VolumeSource.Secret != nil:
		secretDir := filepath.Join(vm.baseDir, "secrets", sandboxID, vol.Name)
		if err := os.MkdirAll(secretDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create secret dir %s: %w", secretDir, err)
		}
		// Write secret data as files
		// (In production, this would read from Kubernetes Secrets API)
		return secretDir, "bind", nil

	case vol.VolumeSource.ConfigMap != nil:
		cmDir := filepath.Join(vm.baseDir, "configmaps", sandboxID, vol.Name)
		if err := os.MkdirAll(cmDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create configmap dir %s: %w", cmDir, err)
		}
		return cmDir, "bind", nil

	default:
		return "", "", fmt.Errorf("no volume source specified for volume %s", vol.Name)
	}
}

func (vm *VolumeManager) mountVolume(source, sandboxID, target, mountType string, readOnly bool) error {
	targetPath := filepath.Join("/var/lib/nexusbox/sandboxes", sandboxID, "rootfs", target)
	if err := os.MkdirAll(targetPath, 0755); err != nil {
		return err
	}

	args := []string{"-t", mountType}
	if readOnly {
		args = append(args, "-o", "ro")
	}
	args = append(args, source, targetPath)

	cmd := exec.Command("mount", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mount failed: %w, output: %s", err, string(output))
	}
	return nil
}

func (vm *VolumeManager) unmountVolume(source string) error {
	cmd := exec.Command("umount", source)
	return cmd.Run()
}
