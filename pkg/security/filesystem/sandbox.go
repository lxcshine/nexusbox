// Package filesystem provides path-based filesystem sandboxing for NexusBox.
// It enforces a whitelist of allowed paths and blocks access to sensitive
// system locations, protecting the host from AI Agent misoperations.
package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ErrPathDenied is returned when a path is outside the allowed whitelist.
var ErrPathDenied = errors.New("path is outside the sandbox whitelist")

// ErrPathTraversal is returned when a path contains traversal sequences.
var ErrPathTraversal = errors.New("path traversal detected")

// SandboxConfig holds the configuration for a filesystem sandbox.
type SandboxConfig struct {
	// WorkspaceRoot is the primary allowed directory (the user's project).
	WorkspaceRoot string

	// AllowedPaths is the whitelist of additional readable paths.
	// These paths are read-only by default unless listed in WritablePaths.
	AllowedPaths []string

	// WritablePaths is the whitelist of writable paths.
	// WorkspaceRoot is implicitly writable.
	WritablePaths []string

	// TempDir is the sandbox temp directory (defaults to OS temp + /nexusbox).
	TempDir string

	// MaxFileSize is the maximum allowed file size for writes (bytes, 0 = unlimited).
	MaxFileSize int64

	// BlockedPaths is an explicit denylist (always denied, even if in whitelist).
	BlockedPaths []string
}

// Sandbox enforces filesystem access rules for a single sandbox.
type Sandbox struct {
	mu            sync.RWMutex
	config        *SandboxConfig
	resolvedRoot  string
	resolvedAllow map[string]bool // resolved path -> true (read allowed)
	resolvedWrite map[string]bool // resolved path -> true (write allowed)
	resolvedDeny  map[string]bool // resolved path -> true (always denied)
}

// NewSandbox creates a new filesystem sandbox with the given configuration.
func NewSandbox(config *SandboxConfig) (*Sandbox, error) {
	if config == nil {
		return nil, errors.New("sandbox config is nil")
	}
	if config.WorkspaceRoot == "" {
		return nil, errors.New("workspaceRoot is required")
	}

	root, err := filepath.Abs(filepath.Clean(config.WorkspaceRoot))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspaceRoot: %w", err)
	}

	s := &Sandbox{
		config:        config,
		resolvedRoot:  root,
		resolvedAllow: make(map[string]bool),
		resolvedWrite: make(map[string]bool),
		resolvedDeny:  make(map[string]bool),
	}

	// Workspace root is implicitly readable + writable.
	s.resolvedAllow[root] = true
	s.resolvedWrite[root] = true

	// Resolve and add allowed paths (read-only).
	for _, p := range config.AllowedPaths {
		rp, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			continue
		}
		s.resolvedAllow[rp] = true
	}

	// Resolve and add writable paths.
	for _, p := range config.WritablePaths {
		rp, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			continue
		}
		s.resolvedAllow[rp] = true
		s.resolvedWrite[rp] = true
	}

	// Set up temp directory.
	tempDir := config.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "nexusbox")
	}
	rp, err := filepath.Abs(filepath.Clean(tempDir))
	if err == nil {
		s.resolvedAllow[rp] = true
		s.resolvedWrite[rp] = true
	}

	// Resolve blocked paths (denylist).
	for _, p := range config.BlockedPaths {
		rp, err := filepath.Abs(filepath.Clean(p))
		if err != nil {
			continue
		}
		s.resolvedDeny[rp] = true
	}

	// On Windows, add default blocked system paths.
	if runtime.GOOS == "windows" {
		for _, sysPath := range defaultWindowsBlockedPaths() {
			s.resolvedDeny[sysPath] = true
		}
	}

	return s, nil
}

// ValidateRead checks if a path can be read.
func (s *Sandbox) ValidateRead(path string) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	return s.checkAccess(resolved, false)
}

// ValidateWrite checks if a path can be written.
func (s *Sandbox) ValidateWrite(path string) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	return s.checkAccess(resolved, true)
}

// ValidateDelete checks if a path can be deleted.
func (s *Sandbox) ValidateDelete(path string) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	return s.checkAccess(resolved, true)
}

// ValidateExec checks if a path can be executed.
func (s *Sandbox) ValidateExec(path string) error {
	// Executable access requires read access.
	// System binaries are allowed for execution even if not in the whitelist
	// (e.g., /usr/bin/python, C:\Windows\System32\cmd.exe), but we still
	// block explicit denylist paths.
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}

	// Check denylist first.
	if s.isDenied(resolved) {
		return fmt.Errorf("%w: %s", ErrPathDenied, path)
	}

	// Check if the path is in the whitelist.
	if s.isReadable(resolved) {
		return nil
	}

	// Allow execution of system binaries (not in denylist).
	// This is a pragmatic choice: AI Agents need to run python/node/etc.
	// which are installed in system paths outside the workspace.
	return nil
}

// WorkspaceRoot returns the resolved workspace root path.
func (s *Sandbox) WorkspaceRoot() string {
	return s.resolvedRoot
}

// IsWithinWorkspace returns true if the path is inside the workspace root.
func (s *Sandbox) IsWithinWorkspace(path string) bool {
	resolved, err := s.resolve(path)
	if err != nil {
		return false
	}
	return strings.HasPrefix(resolved, s.resolvedRoot)
}

// SafeReadFile reads a file after validating access.
func (s *Sandbox) SafeReadFile(path string) ([]byte, error) {
	if err := s.ValidateRead(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// SafeWriteFile writes a file after validating access and size.
func (s *Sandbox) SafeWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := s.ValidateWrite(path); err != nil {
		return err
	}
	// Check file size limit.
	if s.config.MaxFileSize > 0 && int64(len(data)) > s.config.MaxFileSize {
		return fmt.Errorf("file size %d exceeds max %d", len(data), s.config.MaxFileSize)
	}
	// Ensure parent directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return os.WriteFile(path, data, perm)
}

// SafeListDir lists directory entries after validating access.
func (s *Sandbox) SafeListDir(path string) ([]os.DirEntry, error) {
	if err := s.ValidateRead(path); err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

// SafeDelete deletes a file or directory after validating access.
func (s *Sandbox) SafeDelete(path string) error {
	if err := s.ValidateDelete(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

// SafeMkdir creates a directory after validating access.
func (s *Sandbox) SafeMkdir(path string, perm os.FileMode) error {
	if err := s.ValidateWrite(path); err != nil {
		return err
	}
	return os.MkdirAll(path, perm)
}

// resolve resolves and cleans a path, detecting traversal attempts.
func (s *Sandbox) resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}

	// Reject null bytes (path injection).
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%w: null byte in path", ErrPathTraversal)
	}

	// On Windows, Unix-style absolute paths (starting with /) are treated as
	// relative by filepath.IsAbs. We need to handle them as absolute paths
	// rooted at the current drive, so they don't get joined with the workspace.
	if !filepath.IsAbs(path) && !isRootedPath(path) {
		path = filepath.Join(s.resolvedRoot, path)
	}

	// Clean the path to resolve . and .. segments.
	cleaned := filepath.Clean(path)

	// Resolve to absolute path.
	resolved, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	return resolved, nil
}

// isRootedPath returns true if the path is rooted (starts with / or \ on any OS,
// or has a Windows drive letter like C:).
func isRootedPath(path string) bool {
	if len(path) == 0 {
		return false
	}
	// Unix-style absolute path.
	if path[0] == '/' || path[0] == '\\' {
		return true
	}
	// Windows drive letter (e.g., C:\ or C:/).
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		return true
	}
	return false
}

// checkAccess verifies that a resolved path is allowed for the given mode.
func (s *Sandbox) checkAccess(resolvedPath string, write bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check denylist first (always denied).
	if s.isDenied(resolvedPath) {
		return fmt.Errorf("%w: %s", ErrPathDenied, resolvedPath)
	}

	// Check whitelist.
	if write {
		if !s.isWritable(resolvedPath) {
			return fmt.Errorf("%w: %s (write not allowed)", ErrPathDenied, resolvedPath)
		}
	} else {
		if !s.isReadable(resolvedPath) {
			return fmt.Errorf("%w: %s", ErrPathDenied, resolvedPath)
		}
	}

	return nil
}

// isReadable returns true if the path is within a whitelisted readable directory.
func (s *Sandbox) isReadable(resolvedPath string) bool {
	for allowed := range s.resolvedAllow {
		if isPathWithin(resolvedPath, allowed) {
			return true
		}
	}
	return false
}

// isWritable returns true if the path is within a whitelisted writable directory.
func (s *Sandbox) isWritable(resolvedPath string) bool {
	for allowed := range s.resolvedWrite {
		if isPathWithin(resolvedPath, allowed) {
			return true
		}
	}
	return false
}

// isDenied returns true if the path matches any denylist entry.
func (s *Sandbox) isDenied(resolvedPath string) bool {
	for denied := range s.resolvedDeny {
		if isPathWithin(resolvedPath, denied) {
			return true
		}
	}
	return false
}

// isPathWithin returns true if path is equal to or inside the dir.
func isPathWithin(path, dir string) bool {
	if path == dir {
		return true
	}
	// Ensure the path is a subdirectory of dir.
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	// If the relative path starts with "..", it's outside the dir.
	return !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// defaultWindowsBlockedPaths returns Windows system paths that should always be blocked.
func defaultWindowsBlockedPaths() []string {
	var blocked []string
	// Block Windows directory.
	if winDir := os.Getenv("WINDIR"); winDir != "" {
		blocked = append(blocked, filepath.Join(winDir, "System32"))
		blocked = append(blocked, filepath.Join(winDir, "SysWOW64"))
	}
	// Block common system locations.
	for _, drive := range []string{"C:", "D:"} {
		blocked = append(blocked,
			filepath.Join(drive, "\\Windows\\System32"),
			filepath.Join(drive, "\\Windows\\SysWOW64"),
			filepath.Join(drive, "\\Program Files"),
			filepath.Join(drive, "\\Program Files (x86)"),
		)
	}
	// Block boot sectors.
	blocked = append(blocked, "\\\\?\\C:")
	return blocked
}

// DefaultConfig returns a default sandbox configuration for a workspace.
func DefaultConfig(workspaceRoot string) *SandboxConfig {
	return &SandboxConfig{
		WorkspaceRoot: workspaceRoot,
		AllowedPaths: []string{
			// Allow reading common runtime locations.
			"/usr/bin", "/usr/local/bin", "/usr/lib",
			"/bin", "/sbin", "/lib",
			// Python/Node paths (Linux).
			"/usr/local/lib/python*", "/usr/lib/python*",
		},
		WritablePaths: []string{},
		TempDir:       filepath.Join(os.TempDir(), "nexusbox"),
		MaxFileSize:   100 * 1024 * 1024, // 100 MB
		BlockedPaths: []string{
			"/etc/shadow", "/etc/passwd",
			"/root", "/home/*/.ssh",
			"/proc/kcore", "/proc/kmem",
		},
	}
}
