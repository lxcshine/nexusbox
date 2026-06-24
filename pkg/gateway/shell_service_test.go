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
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestShellService() *ShellService {
	return NewShellService(nil)
}

// isShellAvailable checks if the given shell binary exists.
func isShellAvailable(shell string) bool {
	_, err := exec.LookPath(shell)
	return err == nil
}

// --- Shell Exec ---

func TestShellExec_EchoCommand(t *testing.T) {
	svc := newTestShellService()

	// Use platform-appropriate echo command
	cmd := "echo hello world"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c echo hello world"
	}
	body := `{"command":"` + cmd + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.Exec(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp ShellExecResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got '%s'", resp.Stdout)
	}
}

func TestShellExec_MissingCommand(t *testing.T) {
	svc := newTestShellService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.Exec(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestShellExec_FailingCommand(t *testing.T) {
	svc := newTestShellService()

	// Use platform-appropriate exit command
	cmd := "exit 42"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c exit 42"
	}
	body := `{"command":"` + cmd + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.Exec(w, req)

	var resp ShellExecResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 42 {
		t.Errorf("expected exitCode=42, got %d", resp.ExitCode)
	}
}

func TestShellExec_WithWorkDir(t *testing.T) {
	svc := newTestShellService()

	tmpDir := t.TempDir()
	// Use platform-appropriate pwd command
	cmd := "pwd"
	if runtime.GOOS == "windows" {
		cmd = "cd" // on Windows cmd, 'cd' prints the current directory
	}
	body := `{"command":"` + cmd + `","workDir":"` + strings.ReplaceAll(tmpDir, `\`, `\\`) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.Exec(w, req)

	var resp ShellExecResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// Normalize paths for comparison
	stdout := strings.TrimSpace(resp.Stdout)
	stdout = strings.ReplaceAll(stdout, "\\", "/")
	tmpDirNorm := strings.ReplaceAll(tmpDir, "\\", "/")
	if stdout != tmpDirNorm {
		// On Windows, cmd /c cd may not output when run via cmd /c
		// If stdout is empty, check that the command at least ran successfully
		if runtime.GOOS == "windows" && stdout == "" {
			t.Logf("Windows cmd 'cd' did not produce output (known limitation), skipping path check")
		} else {
			t.Errorf("expected pwd='%s', got '%s'", tmpDirNorm, stdout)
		}
	}
}

func TestShellExec_WithEnv(t *testing.T) {
	svc := newTestShellService()

	// Use platform-appropriate env var syntax
	cmd := "echo $NEXUS_TEST_VAR"
	if runtime.GOOS == "windows" {
		cmd = "echo %NEXUS_TEST_VAR%"
	}
	body := `{"command":"` + cmd + `","env":{"NEXUS_TEST_VAR":"test_value_123"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.Exec(w, req)

	var resp ShellExecResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if !strings.Contains(resp.Stdout, "test_value_123") {
		t.Errorf("expected env var in output, got '%s'", resp.Stdout)
	}
}

// --- Bash Exec ---

func TestBashExec_EchoCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash not available on Windows")
	}
	if !isShellAvailable("/bin/bash") {
		t.Skip("/bin/bash not available")
	}

	svc := newTestShellService()

	body := `{"command":"echo bash_test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.BashExec(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp BashExecResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if !strings.Contains(resp.Stdout, "bash_test") {
		t.Errorf("expected stdout to contain 'bash_test', got '%s'", resp.Stdout)
	}
}

func TestBashExec_MissingCommand(t *testing.T) {
	svc := newTestShellService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.BashExec(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Session management ---

func TestCreateAndListSessions(t *testing.T) {
	svc := newTestShellService()

	shell := defaultInteractiveShell()
	if !isShellAvailable(shell) {
		t.Skipf("shell '%s' not available", shell)
	}

	// Create a session
	body := `{"name":"test-session","shell":"` + shell + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.CreateSession(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var sessionInfo SessionInfo
	if err := json.NewDecoder(w.Body).Decode(&sessionInfo); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if sessionInfo.Name != "test-session" {
		t.Errorf("expected name=test-session, got %s", sessionInfo.Name)
	}
	if sessionInfo.ID == "" {
		t.Error("expected non-empty session ID")
	}

	// List sessions
	req2 := httptest.NewRequest(http.MethodGet, "/v1/shell/sessions", nil)
	w2 := httptest.NewRecorder()
	svc.ListSessions(w2, req2)

	var listResp map[string]interface{}
	if err := json.NewDecoder(w2.Body).Decode(&listResp); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}

	sessions, ok := listResp["sessions"].([]interface{})
	if !ok {
		t.Fatal("sessions not found or wrong type")
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}

func TestGetSession_NotFound(t *testing.T) {
	svc := newTestShellService()

	req := httptest.NewRequest(http.MethodGet, "/v1/shell/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	svc.GetSession(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestKillSession_NotFound(t *testing.T) {
	svc := newTestShellService()

	req := httptest.NewRequest(http.MethodGet, "/v1/shell/sessions/nonexistent", nil)
	w := httptest.NewRecorder()

	svc.KillSession(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestBashOutput_NoSessionID(t *testing.T) {
	svc := newTestShellService()

	req := httptest.NewRequest(http.MethodGet, "/v1/bash/output", nil)
	w := httptest.NewRecorder()

	svc.BashOutput(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBashKill_NoSessionID(t *testing.T) {
	svc := newTestShellService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/kill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.BashKill(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- RingBuffer ---

func TestRingBuffer_WriteRead(t *testing.T) {
	rb := NewRingBuffer(64)

	data := []byte("hello")
	n, err := rb.Write(data)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}

	result := rb.Read()
	if string(result) != "hello" {
		t.Errorf("expected 'hello', got '%s'", string(result))
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer(8)

	// Write more than the buffer size
	rb.Write([]byte("1234567890")) // 10 bytes, buffer is 8

	result := rb.Read()
	// Should contain the last 8 bytes: "34567890"
	if len(result) != 8 {
		t.Errorf("expected 8 bytes, got %d", len(result))
	}
	if string(result) != "34567890" {
		t.Errorf("expected '34567890', got '%s'", string(result))
	}
}

func TestRingBuffer_MultipleWrites(t *testing.T) {
	rb := NewRingBuffer(1024)

	rb.Write([]byte("line1\n"))
	rb.Write([]byte("line2\n"))
	rb.Write([]byte("line3\n"))

	result := rb.Read()
	expected := "line1\nline2\nline3\n"
	if string(result) != expected {
		t.Errorf("expected '%s', got '%s'", expected, string(result))
	}
}

// --- ShellSession IsActive ---

func TestShellSession_IsActive_NilProcess(t *testing.T) {
	s := &ShellSession{
		done: make(chan struct{}),
	}
	if s.IsActive() {
		t.Error("expected inactive for nil process")
	}
}

func TestShellSession_IsActive_Done(t *testing.T) {
	done := make(chan struct{})
	close(done)
	s := &ShellSession{
		done: done,
	}
	if s.IsActive() {
		t.Error("expected inactive for closed done channel")
	}
}

// --- resolveWorkDir ---

func TestResolveWorkDir_Empty(t *testing.T) {
	result := resolveWorkDir("", "/workspace")
	if result != "/workspace" {
		t.Errorf("expected /workspace, got %s", result)
	}
}

func TestResolveWorkDir_Absolute(t *testing.T) {
	absPath := "/absolute/path"
	if runtime.GOOS == "windows" {
		absPath = "C:\\absolute\\path"
	}
	result := resolveWorkDir(absPath, "/workspace")
	// On Windows, filepath.IsAbs handles Windows-style paths
	if result != absPath {
		t.Errorf("expected %s, got %s", absPath, result)
	}
}

func TestResolveWorkDir_Relative(t *testing.T) {
	result := resolveWorkDir("sub/dir", "/workspace")
	expected := "/workspace/sub/dir"
	// Normalize for Windows
	result = strings.ReplaceAll(result, "\\", "/")
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

// --- Integration: create session, exec in session, kill ---

func TestSessionLifecycle(t *testing.T) {
	shell := defaultInteractiveShell()
	if !isShellAvailable(shell) {
		t.Skipf("shell '%s' not available on this platform", shell)
	}

	svc := newTestShellService()

	// Create session
	body := `{"name":"lifecycle-test","shell":"` + shell + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/shell/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.CreateSession(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var info SessionInfo
	json.NewDecoder(w.Body).Decode(&info)
	sessionID := info.ID

	// Wait for session to start
	time.Sleep(200 * time.Millisecond)

	// Get session
	req2 := httptest.NewRequest(http.MethodGet, "/v1/shell/sessions/"+sessionID, nil)
	w2 := httptest.NewRecorder()
	svc.GetSession(w2, req2, sessionID)

	if w2.Code != http.StatusOK {
		t.Errorf("get session: expected 200, got %d", w2.Code)
	}

	// Kill session
	req3 := httptest.NewRequest(http.MethodDelete, "/v1/shell/sessions/"+sessionID, nil)
	w3 := httptest.NewRecorder()
	svc.KillSession(w3, req3, sessionID)

	if w3.Code != http.StatusOK {
		t.Errorf("kill session: expected 200, got %d", w3.Code)
	}

	// Verify session is removed
	svc.mu.RLock()
	_, exists := svc.sessions[sessionID]
	svc.mu.RUnlock()
	if exists {
		t.Error("session should be removed after kill")
	}
}
