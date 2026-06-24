/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"
)

func newTestFileService(t *testing.T) *FileService {
	tmpDir := t.TempDir()
	return NewFileService(tmpDir)
}

func (f *FileService) workspaceDir() string {
	return f.workspace
}

// --- File Write + Read round-trip ---

func TestFileWriteAndRead(t *testing.T) {
	svc := newTestFileService(t)

	// Write a file
	writeBody := `{"path":"test.txt","content":"hello nexusbox","createDirs":true}`
	writeReq := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(writeBody))
	writeReq.Header.Set("Content-Type", "application/json")
	writeW := httptest.NewRecorder()
	svc.Write(writeW, writeReq)

	if writeW.Code != http.StatusOK {
		t.Fatalf("write: expected 200, got %d; body: %s", writeW.Code, writeW.Body.String())
	}

	var writeResp FileWriteResponse
	if err := json.NewDecoder(writeW.Body).Decode(&writeResp); err != nil {
		t.Fatalf("failed to decode write response: %v", err)
	}
	if writeResp.Status != "ok" {
		t.Errorf("expected status=ok, got %s", writeResp.Status)
	}

	// Read it back
	readBody := `{"path":"test.txt"}`
	readReq := httptest.NewRequest(http.MethodPost, "/v1/file/read", strings.NewReader(readBody))
	readReq.Header.Set("Content-Type", "application/json")
	readW := httptest.NewRecorder()
	svc.Read(readW, readReq)

	if readW.Code != http.StatusOK {
		t.Fatalf("read: expected 200, got %d; body: %s", readW.Code, readW.Body.String())
	}

	var readResp FileReadResponse
	if err := json.NewDecoder(readW.Body).Decode(&readResp); err != nil {
		t.Fatalf("failed to decode read response: %v", err)
	}
	if readResp.Content != "hello nexusbox" {
		t.Errorf("expected content='hello nexusbox', got '%s'", readResp.Content)
	}
	if readResp.Encoding != "utf-8" {
		t.Errorf("expected encoding=utf-8, got %s", readResp.Encoding)
	}
}

// --- File Read: missing path ---

func TestFileRead_MissingPath(t *testing.T) {
	svc := newTestFileService(t)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Read(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- File Read: not found ---

func TestFileRead_NotFound(t *testing.T) {
	svc := newTestFileService(t)

	body := `{"path":"nonexistent.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Read(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- File Read: directory ---

func TestFileRead_Directory(t *testing.T) {
	svc := newTestFileService(t)

	body := `{"path":"/"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Read(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for directory read, got %d", w.Code)
	}
}

// --- File Write: missing path ---

func TestFileWrite_MissingPath(t *testing.T) {
	svc := newTestFileService(t)

	body := `{"content":"data"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Write(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- File Write: append mode ---

func TestFileWrite_Append(t *testing.T) {
	svc := newTestFileService(t)

	// Initial write
	writeBody1 := `{"path":"append.txt","content":"line1\n"}`
	req1 := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(writeBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	svc.Write(w1, req1)

	// Append
	writeBody2 := `{"path":"append.txt","content":"line2\n","append":true}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(writeBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	svc.Write(w2, req2)

	// Read back
	readBody := `{"path":"append.txt"}`
	readReq := httptest.NewRequest(http.MethodPost, "/v1/file/read", strings.NewReader(readBody))
	readReq.Header.Set("Content-Type", "application/json")
	readW := httptest.NewRecorder()
	svc.Read(readW, readReq)

	var readResp FileReadResponse
	json.NewDecoder(readW.Body).Decode(&readResp)

	expected := "line1\nline2\n"
	if readResp.Content != expected {
		t.Errorf("expected '%s', got '%s'", expected, readResp.Content)
	}
}

// --- File Write: base64 encoding ---

func TestFileWrite_Base64(t *testing.T) {
	svc := newTestFileService(t)

	// Write base64 content (binary data: 0x00 0x01 0x02 0x03)
	writeBody := `{"path":"binary.bin","content":"AAECAw==","encoding":"base64"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Write(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write base64: expected 200, got %d", w.Code)
	}

	// Verify the file content directly
	data, err := os.ReadFile(filepath.Join(svc.workspaceDir(), "binary.bin"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if len(data) != 4 {
		t.Errorf("expected 4 bytes, got %d", len(data))
	}
	for i, b := range data {
		if int(b) != i {
			t.Errorf("byte %d: expected %d, got %d", i, i, b)
		}
	}
}

// --- File Write: createDirs ---

func TestFileWrite_CreateDirs(t *testing.T) {
	svc := newTestFileService(t)

	writeBody := `{"path":"deep/nested/dir/file.txt","content":"nested","createDirs":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", strings.NewReader(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Write(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write with createDirs: expected 200, got %d", w.Code)
	}

	// Verify the file exists
	if _, err := os.Stat(filepath.Join(svc.workspaceDir(), "deep", "nested", "dir", "file.txt")); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}

// --- File List ---

func TestFileList(t *testing.T) {
	svc := newTestFileService(t)

	// Create some test files
	os.WriteFile(filepath.Join(svc.workspaceDir(), "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(svc.workspaceDir(), "b.txt"), []byte("b"), 0644)
	os.MkdirAll(filepath.Join(svc.workspaceDir(), "subdir"), 0755)

	body := `{"path":"/"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp FileListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.Entries) < 2 {
		t.Errorf("expected at least 2 entries, got %d", len(resp.Entries))
	}

	// Check that both files and directories are present
	foundFile := false
	foundDir := false
	for _, e := range resp.Entries {
		if e.Name == "a.txt" && !e.IsDir {
			foundFile = true
		}
		if e.Name == "subdir" && e.IsDir {
			foundDir = true
		}
	}
	if !foundFile {
		t.Error("expected to find a.txt file entry")
	}
	if !foundDir {
		t.Error("expected to find subdir directory entry")
	}
}

// --- File List: not a directory ---

func TestFileList_NotDirectory(t *testing.T) {
	svc := newTestFileService(t)

	os.WriteFile(filepath.Join(svc.workspaceDir(), "file.txt"), []byte("data"), 0644)

	body := `{"path":"file.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.List(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-directory, got %d", w.Code)
	}
}

// --- File Find ---

func TestFileFind(t *testing.T) {
	svc := newTestFileService(t)

	// Create test files
	os.WriteFile(filepath.Join(svc.workspaceDir(), "test.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(svc.workspaceDir(), "test.txt"), []byte("text"), 0644)

	body := `{"path":"/","pattern":"*.go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/find", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Find(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("find: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	files, ok := resp["files"].([]interface{})
	if !ok {
		t.Fatal("files not found")
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d", len(files))
	}
}

// --- File Grep ---

func TestFileGrep(t *testing.T) {
	svc := newTestFileService(t)

	os.WriteFile(filepath.Join(svc.workspaceDir(), "code.go"), []byte("package main\nfunc hello() {}\n"), 0644)
	os.WriteFile(filepath.Join(svc.workspaceDir(), "readme.txt"), []byte("hello world\n"), 0644)

	body := `{"path":"/","pattern":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/grep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Grep(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("grep: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	count, _ := resp["count"].(float64)
	if count < 2 {
		t.Errorf("expected at least 2 matches, got %v", count)
	}
}

// --- File Grep with include filter ---

func TestFileGrep_WithInclude(t *testing.T) {
	svc := newTestFileService(t)

	os.WriteFile(filepath.Join(svc.workspaceDir(), "code.go"), []byte("package main\nfunc hello() {}\n"), 0644)
	os.WriteFile(filepath.Join(svc.workspaceDir(), "readme.txt"), []byte("hello world\n"), 0644)

	body := `{"path":"/","pattern":"hello","include":"*.go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/grep", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Grep(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	count, _ := resp["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 match with include filter, got %v", count)
	}
}

// --- File Glob ---

func TestFileGlob(t *testing.T) {
	svc := newTestFileService(t)

	os.WriteFile(filepath.Join(svc.workspaceDir(), "test.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(svc.workspaceDir(), "test.txt"), []byte("text"), 0644)

	body := `{"path":"/","pattern":"*.go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/glob", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Glob(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("glob: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	count, _ := resp["count"].(float64)
	if count != 1 {
		t.Errorf("expected 1 glob match, got %v", count)
	}
}

// --- resolvePath ---

func TestResolvePath_Absolute(t *testing.T) {
	// Use a platform-appropriate absolute path
	var absPath string
	if goRuntime.GOOS == "windows" {
		absPath = "C:\\absolute\\path"
	} else {
		absPath = "/absolute/path"
	}
	svc := NewFileService(absPath)
	result := svc.resolvePath(absPath)
	// On Windows, resolvePath may canonicalize; compare cleaned paths
	if filepath.Clean(result) != filepath.Clean(absPath) {
		t.Errorf("expected %s, got %s", absPath, result)
	}
}

func TestResolvePath_Relative(t *testing.T) {
	// Use a workspace that exists relative to the test
	svc := NewFileService("workspace")
	result := svc.resolvePath("relative/path")
	// Compare normalized paths (forward slashes for cross-platform)
	resultNorm := strings.ReplaceAll(result, "\\", "/")
	if !strings.HasSuffix(resultNorm, "workspace/relative/path") {
		t.Errorf("expected path ending with workspace/relative/path, got %s", result)
	}
}
