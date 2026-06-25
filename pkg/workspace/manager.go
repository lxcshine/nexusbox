// Package workspace provides multi-project workspace isolation so that
// multiple AI agent sessions can run in parallel without interfering with
// each other's files, environment, or resource budgets.
//
// Each workspace is a self-contained directory tree rooted at
// <baseRoot>/<workspaceID> with its own data/, tmp/, and caches/ subdirs,
// plus an optional resource quota (CPU/memory/disk). The WorkspaceManager
// hands out Workspace handles that the sandbox layer binds to a single AI
// session; a session scoped to workspace A physically cannot resolve a path
// inside workspace B because all path operations are confined to the
// workspace root.
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Errors returned by the workspace manager.
var (
	// ErrWorkspaceNotFound is returned when an operation targets a workspace
	// ID that does not exist (or has already been released).
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrWorkspaceExists is returned when creating a workspace whose ID is
	// already in use.
	ErrWorkspaceExists = errors.New("workspace already exists")
	// ErrPathOutsideWorkspace is returned when a caller tries to resolve a
	// path that escapes the workspace root via traversal or absolute redirects.
	ErrPathOutsideWorkspace = errors.New("path escapes the workspace root")
)

// Quota limits the resources a single workspace (and thus the AI session
// bound to it) can consume. A zero value means "no limit"; callers should set
// at least DiskLimitBytes to prevent one session from filling the disk.
type Quota struct {
	// DiskLimitBytes is the maximum bytes the workspace may consume on disk.
	// Enforced best-effort via the filesystem where supported.
	DiskLimitBytes int64
	// MaxFileCount is the maximum number of files in the workspace.
	MaxFileCount int64
	// MaxProcs is the maximum number of concurrent processes the AI session
	// may spawn inside this workspace (advisory; enforced by the runtime layer).
	MaxProcs int
	// MemoryLimitBytes is the memory budget for processes in this workspace.
	MemoryLimitBytes int64
}

// Workspace is an isolated project directory owned by one AI session.
type Workspace struct {
	// ID is the unique workspace identifier (also the on-disk directory name).
	ID string
	// Root is the absolute path of the workspace root.
	Root string
	// DataDir is the workspace's primary data directory (<root>/data).
	DataDir string
	// TmpDir is the workspace-private temp directory (<root>/tmp).
	TmpDir string
	// CacheDir is the workspace-private cache directory (<root>/caches).
	CacheDir string
	// Quota is the resource quota for this workspace.
	Quota Quota
	// OwnerSession is the AI session ID that owns this workspace (empty if
	// unbound; binding is optional but recommended for accounting).
	OwnerSession string
	// CreatedAt is when the workspace was created.
	CreatedAt time.Time
	// Labels are arbitrary key/value tags for the workspace.
	Labels map[string]string
}

// Manager allocates, tracks, and reclaims isolated workspaces. It is safe
// for concurrent use by multiple AI sessions.
type Manager struct {
	mu        sync.RWMutex
	baseRoot  string
	workspaces map[string]*Workspace
}

// ManagerConfig holds configuration for the workspace manager.
type ManagerConfig struct {
	// BaseRoot is the parent directory under which all workspaces are created.
	// If empty, defaults to a platform-appropriate location.
	BaseRoot string
}

// NewManager creates a new workspace manager. The base root is created if it
// does not exist.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	base := cfg.BaseRoot
	if base == "" {
		if runtime.GOOS == "windows" {
			base = filepath.Join(os.Getenv("ProgramData"), "NexusBox", "workspaces")
		} else {
			base = "/var/lib/nexusbox/workspaces"
		}
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, fmt.Errorf("create workspace base root %s: %w", base, err)
	}
	return &Manager{
		baseRoot:   base,
		workspaces: make(map[string]*Workspace),
	}, nil
}

// BaseRoot returns the base directory under which workspaces are created.
func (m *Manager) BaseRoot() string { return m.baseRoot }

// CreateOpts configures workspace creation.
type CreateOpts struct {
	// ID is the workspace ID. If empty, a random ID is generated.
	ID string
	// OwnerSession optionally binds the workspace to an AI session.
	OwnerSession string
	// Quota sets resource limits for the workspace.
	Quota Quota
	// Labels are arbitrary key/value tags.
	Labels map[string]string
}

// Create allocates a new isolated workspace directory and returns a handle.
// If opts.ID is empty, a random 12-char hex ID is generated. Returns
// ErrWorkspaceExists if opts.ID is already in use.
func (m *Manager) Create(opts CreateOpts) (*Workspace, error) {
	id := opts.ID
	if id == "" {
		id = randomID(6) // 12 hex chars
	}

	m.mu.Lock()
	if _, exists := m.workspaces[id]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrWorkspaceExists, id)
	}
	root := filepath.Join(m.baseRoot, id)
	// Reserve the slot before doing filesystem work so a concurrent Create
	// with the same ID cannot race.
	m.workspaces[id] = &Workspace{ID: id, Root: root, CreatedAt: time.Now()}
	m.mu.Unlock()

	// Create the directory tree. If this fails, roll back the reservation.
	w := &Workspace{
		ID:          id,
		Root:        root,
		DataDir:     filepath.Join(root, "data"),
		TmpDir:      filepath.Join(root, "tmp"),
		CacheDir:    filepath.Join(root, "caches"),
		Quota:       opts.Quota,
		OwnerSession: opts.OwnerSession,
		CreatedAt:   time.Now(),
		Labels:      opts.Labels,
	}
	for _, dir := range []string{w.DataDir, w.TmpDir, w.CacheDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			m.mu.Lock()
			delete(m.workspaces, id)
			m.mu.Unlock()
			return nil, fmt.Errorf("create workspace dir %s: %w", dir, err)
		}
	}

	m.mu.Lock()
	m.workspaces[id] = w
	m.mu.Unlock()

	klog.Infof("workspace: created %s at %s (owner=%s)", id, root, opts.OwnerSession)
	return w, nil
}

// Get returns the workspace with the given ID, or ErrWorkspaceNotFound.
func (m *Manager) Get(id string) (*Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, id)
	}
	return w, nil
}

// List returns all workspaces, optionally filtered by owner session.
func (m *Manager) List(ownerSession string) []*Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Workspace, 0, len(m.workspaces))
	for _, w := range m.workspaces {
		if ownerSession != "" && w.OwnerSession != ownerSession {
			continue
		}
		out = append(out, w)
	}
	return out
}

// Release deletes the workspace directory and removes it from the manager.
// It is safe to call Release on an already-released workspace (returns
// ErrWorkspaceNotFound).
func (m *Manager) Release(id string) error {
	m.mu.Lock()
	w, ok := m.workspaces[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrWorkspaceNotFound, id)
	}
	delete(m.workspaces, id)
	m.mu.Unlock()

	if err := os.RemoveAll(w.Root); err != nil {
		return fmt.Errorf("remove workspace %s: %w", id, err)
	}
	klog.Infof("workspace: released %s", id)
	return nil
}

// ResolvePath converts a possibly-relative path into an absolute path inside
// the workspace root, rejecting any path that escapes the workspace via
// traversal (../) or absolute redirects. The returned path is always cleaned
// and is guaranteed to be within <root>.
//
// This is the core isolation primitive: an AI session bound to workspace A
// cannot reach workspace B's files because ResolvePath confines every path
// to A's root.
func (w *Workspace) ResolvePath(p string) (string, error) {
	if p == "" {
		return w.Root, nil
	}
	// Strip any volume/absolute prefix so the path is interpreted relative to
	// the workspace root regardless of what the caller supplied.
	cleaned := filepath.Clean(p)
	cleaned = strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned))
	if filepath.IsAbs(cleaned) {
		cleaned = strings.TrimPrefix(cleaned, string(filepath.Separator))
	}

	joined := filepath.Join(w.Root, cleaned)
	// Final guard: the cleaned result must be within Root.
	rel, err := filepath.Rel(w.Root, joined)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideWorkspace, p)
	}
	return joined, nil
}

// ResolveData, ResolveTmp and ResolveCache are convenience wrappers that
// resolve a path inside the respective workspace subdirectory.
func (w *Workspace) ResolveData(p string) (string, error) {
	return w.resolveSubdir(w.DataDir, p)
}
func (w *Workspace) ResolveTmp(p string) (string, error) {
	return w.resolveSubdir(w.TmpDir, p)
}
func (w *Workspace) ResolveCache(p string) (string, error) {
	return w.resolveSubdir(w.CacheDir, p)
}

func (w *Workspace) resolveSubdir(base, p string) (string, error) {
	if p == "" {
		return base, nil
	}
	cleaned := filepath.Clean(p)
	cleaned = strings.TrimPrefix(cleaned, filepath.VolumeName(cleaned))
	if filepath.IsAbs(cleaned) {
		cleaned = strings.TrimPrefix(cleaned, string(filepath.Separator))
	}
	joined := filepath.Join(base, cleaned)
	rel, err := filepath.Rel(base, joined)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideWorkspace, p)
	}
	return joined, nil
}

// randomID returns n*2 hex characters of cryptographic randomness.
func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// rand.Read should never fail on supported platforms; fall back to
		// a time-based id so we never block workspace creation.
		return fmt.Sprintf("ws%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
