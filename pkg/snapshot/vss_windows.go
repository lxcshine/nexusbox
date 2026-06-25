//go:build windows

package snapshot

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/klog/v2"
)

// vssBackendName is the backend identifier for the Windows VSS backend.
const vssBackendName = "vss"

// vssBackend uses the Windows Volume Shadow Copy Service (VSS) to create
// crash-consistent, volume-level snapshots of a sandbox workspace.
//
// VSS is invoked via `vssadmin create shadow /For=<volume>`, which requires
// Administrator privileges. After the shadow copy is created, the workspace
// tree is mirrored from the shadow device path into the snapshot directory
// using hardlinks (the shadow copy is read-only, so we cannot hardlink into
// it directly; we copy and then drop the shadow copy).
//
// If VSS is unavailable (non-admin, missing binary, etc.), the backend falls
// back to the cross-platform filesystemBackend so snapshots still work.
type vssBackend struct {
	fallback SnapshotBackend
}

func newVSSBackend() SnapshotBackend {
	return &vssBackend{fallback: filesystemBackend{}}
}

func (b *vssBackend) Name() string { return vssBackendName }

// Create attempts a VSS snapshot and falls back to the filesystem backend
// when VSS is not available (e.g. non-elevated process or missing vssadmin).
func (b *vssBackend) Create(ctx context.Context, sourcePath, snapshotDir string) (*SnapshotArtifacts, error) {
	abs, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	volume, err := volumeForPath(abs)
	if err != nil {
		klog.Warningf("VSS: cannot resolve volume for %s: %v; falling back to filesystem backend", abs, err)
		return b.fallback.Create(ctx, sourcePath, snapshotDir)
	}

	if !vssAvailable() {
		klog.Warningf("VSS: vssadmin not available; falling back to filesystem backend for %s", abs)
		return b.fallback.Create(ctx, sourcePath, snapshotDir)
	}

	shadowDevice, shadowID, err := createVSSShadow(ctx, volume)
	if err != nil {
		klog.Warningf("VSS: create shadow failed for volume %s: %v; falling back to filesystem backend", volume, err)
		return b.fallback.Create(ctx, sourcePath, snapshotDir)
	}
	klog.Infof("VSS: created shadow copy %s on %s for source %s", shadowID, shadowDevice, abs)

	// Mirror the source subtree from the shadow device into snapshotDir/data.
	dataDir := filepath.Join(snapshotDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		_ = deleteVSSShadow(ctx, shadowID)
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}

	// The path inside the shadow device mirrors the original absolute path.
	// e.g. C:\work\proj -> \\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy1\work\proj
	rel := strings.TrimPrefix(abs, volume)
	shadowSource := shadowDevice + rel

	var size int64
	err = filepath.WalkDir(shadowSource, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(shadowSource, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dataDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// Cannot hardlink from a read-only shadow device reliably; copy.
		if cerr := copyFile(path, target, info.Mode()); cerr != nil {
			return cerr
		}
		size += info.Size()
		return nil
	})

	// Always attempt to delete the shadow copy; we keep the mirror on disk.
	if derr := deleteVSSShadow(ctx, shadowID); derr != nil {
		klog.Warningf("VSS: failed to delete shadow copy %s: %v", shadowID, derr)
	}

	if err != nil {
		return nil, fmt.Errorf("mirror shadow tree: %w", err)
	}

	if err := os.WriteFile(filepath.Join(snapshotDir, "backend.txt"),
		[]byte("vss\n"), 0644); err != nil {
		return nil, err
	}

	return &SnapshotArtifacts{
		Backend: vssBackendName,
		Size:    size,
		Extra: map[string]string{
			"shadowID": shadowID,
			"volume":   volume,
			"layout":   "mirror",
		},
	}, nil
}

// Restore reproduces the source tree from snapshotDir/data into targetPath.
// VSS only writes a mirrored data tree, so restore is identical to the
// filesystem backend.
func (b *vssBackend) Restore(ctx context.Context, snapshotDir, targetPath string) error {
	return filesystemBackend{}.Restore(ctx, snapshotDir, targetPath)
}

// vssAvailable reports whether vssadmin is on PATH.
func vssAvailable() bool {
	_, err := exec.LookPath("vssadmin")
	return err == nil
}

// createVSSShadow creates a VSS shadow copy of the given volume (e.g. "C:").
// Returns the shadow device path (e.g. \\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy1)
// and the shadow copy GUID.
func createVSSShadow(ctx context.Context, volume string) (device, shadowID string, err error) {
	out, err := runCmd(ctx, "vssadmin", "create", "shadow", "/For="+volume)
	if err != nil {
		return "", "", err
	}
	// Output contains lines like:
	//   Shadow Copy Volume Name: \\?\GLOBALROOT\Device\HarddiskVolumeShadowCopy1
	//   Shadow Copy ID: {a1b2c3d4-...}
	deviceRe := regexp.MustCompile(`Shadow Copy Volume Name:\s*(\\.+)$`)
	idRe := regexp.MustCompile(`Shadow Copy ID:\s*(\{[0-9a-fA-F-]+\})`)

	for _, line := range strings.Split(string(out), "\n") {
		if m := deviceRe.FindStringSubmatch(line); m != nil {
			device = strings.TrimSpace(m[1])
		}
		if m := idRe.FindStringSubmatch(line); m != nil {
			shadowID = strings.TrimSpace(m[1])
		}
	}
	if device == "" || shadowID == "" {
		return "", "", fmt.Errorf("could not parse vssadmin output: %s", string(out))
	}
	return device, shadowID, nil
}

// deleteVSSShadow deletes a VSS shadow copy by GUID.
func deleteVSSShadow(ctx context.Context, shadowID string) error {
	if shadowID == "" {
		return nil
	}
	_, err := runCmd(ctx, "vssadmin", "delete", "shadows", "/Shadow="+shadowID, "/Quiet")
	return err
}

// volumeForPath returns the volume root for the given path (e.g. "C:" for
// "C:\Users\foo"). On Windows this is the drive letter; for UNC paths it is
// the \\server\share prefix.
func volumeForPath(p string) (string, error) {
	if len(p) >= 2 && p[1] == ':' {
		return strings.ToUpper(string(p[0]) + ":"), nil
	}
	// UNC path: \\server\share\... -> volume is \\server\share
	if strings.HasPrefix(p, `\\`) {
		parts := strings.SplitN(strings.TrimPrefix(p, `\\`), `\`, 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1], nil
		}
	}
	return "", fmt.Errorf("cannot determine volume for %s", p)
}
