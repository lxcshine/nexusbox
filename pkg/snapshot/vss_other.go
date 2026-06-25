//go:build !windows


package snapshot

import "context"

// vssBackendName is the backend identifier for the Windows VSS backend. On
// non-Windows platforms VSS is never selected, but the constant is referenced
// by manager.go for backend-name comparisons in DeleteSnapshot/PruneSnapshots.
const vssBackendName = "vss"

// newVSSBackend is a no-op stub on non-Windows; the default backend selection
// (defaultBackend) only calls this on windows, so this stub exists purely to
// satisfy the build on other platforms.
func newVSSBackend() SnapshotBackend { return filesystemBackend{} }

// deleteVSSShadow is a no-op on non-Windows.
func deleteVSSShadow(ctx context.Context, shadowID string) error { return nil }
