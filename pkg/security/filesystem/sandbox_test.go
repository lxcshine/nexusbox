package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSandbox(t *testing.T) {
	tmpDir := t.TempDir()
	sb, err := NewSandbox(&SandboxConfig{
		WorkspaceRoot: tmpDir,
	})
	if err != nil {
		t.Fatalf("NewSandbox failed: %v", err)
	}
	if sb.WorkspaceRoot() != tmpDir {
		t.Errorf("WorkspaceRoot = %s, want %s", sb.WorkspaceRoot(), tmpDir)
	}
}

func TestNewSandbox_NilConfig(t *testing.T) {
	_, err := NewSandbox(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewSandbox_EmptyRoot(t *testing.T) {
	_, err := NewSandbox(&SandboxConfig{})
	if err == nil {
		t.Fatal("expected error for empty workspaceRoot")
	}
}

func TestValidateRead_WithinWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	file := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(file, []byte("hello"), 0644)

	if err := sb.ValidateRead(file); err != nil {
		t.Errorf("ValidateRead(%s) failed: %v", file, err)
	}
}

func TestValidateRead_OutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	// Try to read a system file.
	systemFile := "/etc/hostname"
	if _, err := os.Stat(systemFile); err != nil {
		t.Skip("system file not available")
	}
	if err := sb.ValidateRead(systemFile); err == nil {
		t.Error("expected error reading outside workspace")
	}
}

func TestValidateWrite_WithinWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	file := filepath.Join(tmpDir, "new.txt")
	if err := sb.ValidateWrite(file); err != nil {
		t.Errorf("ValidateWrite(%s) failed: %v", file, err)
	}
}

func TestValidateWrite_OutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	// Try to write to a system directory.
	systemFile := "/etc/test-nexusbox-write"
	if err := sb.ValidateWrite(systemFile); err == nil {
		t.Error("expected error writing outside workspace")
	}
}

func TestPathTraversal_DotDot(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	// Path traversal attempt: workspace/../../etc/passwd
	malicious := filepath.Join(tmpDir, "..", "..", "etc", "passwd")
	if err := sb.ValidateRead(malicious); err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestPathTraversal_NullByte(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	malicious := tmpDir + "/\x00/etc/passwd"
	if err := sb.ValidateRead(malicious); err == nil {
		t.Error("expected error for null byte injection")
	}
}

func TestBlockedPaths(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{
		WorkspaceRoot: tmpDir,
		BlockedPaths: []string{
			filepath.Join(tmpDir, "secret"),
		},
	})

	blocked := filepath.Join(tmpDir, "secret", "key.txt")
	if err := sb.ValidateRead(blocked); err == nil {
		t.Error("expected error for blocked path")
	}
}

func TestAllowedPaths(t *testing.T) {
	tmpDir := t.TempDir()
	systemBin := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{
		WorkspaceRoot: tmpDir,
		AllowedPaths:  []string{systemBin},
	})

	allowedFile := filepath.Join(systemBin, "python")
	if err := sb.ValidateRead(allowedFile); err != nil {
		t.Errorf("ValidateRead(%s) failed: %v", allowedFile, err)
	}

	// Writing to read-only path should fail.
	if err := sb.ValidateWrite(allowedFile); err == nil {
		t.Error("expected error writing to read-only path")
	}
}

func TestSafeReadFile(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	file := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(file, []byte("hello world"), 0644)

	data, err := sb.SafeReadFile(file)
	if err != nil {
		t.Fatalf("SafeReadFile failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("got %q, want %q", data, "hello world")
	}
}

func TestSafeWriteFile(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	file := filepath.Join(tmpDir, "subdir", "new.txt")
	if err := sb.SafeWriteFile(file, []byte("created"), 0644); err != nil {
		t.Fatalf("SafeWriteFile failed: %v", err)
	}

	data, _ := os.ReadFile(file)
	if string(data) != "created" {
		t.Errorf("got %q, want %q", data, "created")
	}
}

func TestSafeWriteFile_MaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{
		WorkspaceRoot: tmpDir,
		MaxFileSize:   10,
	})

	file := filepath.Join(tmpDir, "big.txt")
	largeData := make([]byte, 100)
	if err := sb.SafeWriteFile(file, largeData, 0644); err == nil {
		t.Error("expected error for file exceeding max size")
	}
}

func TestSafeListDir(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	// Create some files.
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644)

	entries, err := sb.SafeListDir(tmpDir)
	if err != nil {
		t.Fatalf("SafeListDir failed: %v", err)
	}
	if len(entries) < 2 {
		t.Errorf("expected at least 2 entries, got %d", len(entries))
	}
}

func TestSafeDelete(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	file := filepath.Join(tmpDir, "delete.txt")
	os.WriteFile(file, []byte("bye"), 0644)

	if err := sb.SafeDelete(file); err != nil {
		t.Fatalf("SafeDelete failed: %v", err)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestIsWithinWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	sb, _ := NewSandbox(&SandboxConfig{WorkspaceRoot: tmpDir})

	if !sb.IsWithinWorkspace(filepath.Join(tmpDir, "subdir", "file.txt")) {
		t.Error("expected true for path within workspace")
	}
	if sb.IsWithinWorkspace("/etc/passwd") {
		t.Error("expected false for path outside workspace")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig("/workspace")
	if config.WorkspaceRoot != "/workspace" {
		t.Errorf("WorkspaceRoot = %s, want /workspace", config.WorkspaceRoot)
	}
	if config.MaxFileSize == 0 {
		t.Error("MaxFileSize should not be 0")
	}
	if len(config.BlockedPaths) == 0 {
		t.Error("BlockedPaths should not be empty")
	}
}
