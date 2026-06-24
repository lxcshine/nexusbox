/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
*/

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHub() *Hub {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-mcp-test-*")
	// Note: tmpDir is intentionally not cleaned up here because the hub
	// needs it to exist during tests. Go's test framework cleans up temp dirs.
	return NewHub(&HubConfig{Port: 0, Workspace: tmpDir})
}

// --- Hub: RegisterServer / ListServers ---

func TestHub_RegisterAndListServers(t *testing.T) {
	hub := newTestHub()

	servers := hub.ListServers()
	if len(servers) != 4 {
		t.Errorf("expected 4 servers, got %d", len(servers))
	}

	expected := map[string]bool{"shell": true, "file": true, "code": true, "browser": true}
	for _, name := range servers {
		if !expected[name] {
			t.Errorf("unexpected server: %s", name)
		}
	}
}

func TestHub_UnregisterServer(t *testing.T) {
	hub := newTestHub()

	hub.UnregisterServer("shell")
	servers := hub.ListServers()
	if len(servers) != 3 {
		t.Errorf("expected 3 servers after unregister, got %d", len(servers))
	}
}

// --- Hub: JSON-RPC initialize ---

func TestHub_Initialize(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"initialize","id":1}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initialize: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp JSONRPCMessage
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %s", resp.JSONRPC)
	}

	var result map[string]interface{}
	json.Unmarshal(resp.Result, &result)

	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "nexusbox-mcp-hub" {
		t.Errorf("expected server name=nexusbox-mcp-hub, got %v", serverInfo["name"])
	}

	capabilities, _ := result["capabilities"].(map[string]interface{})
	if _, ok := capabilities["tools"]; !ok {
		t.Error("expected tools capability")
	}
}

// --- Hub: JSON-RPC tools/list ---

func TestHub_ToolsList(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"tools/list","id":2}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("tools/list: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	var result map[string]interface{}
	json.Unmarshal(resp.Result, &result)

	tools, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("tools not found or wrong type")
	}
	if len(tools) == 0 {
		t.Error("expected at least one tool")
	}
}

// --- Hub: JSON-RPC tools/call ---

func TestHub_ToolsCall(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"shell_exec","arguments":{"command":"echo test"}},"id":3}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("tools/call: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	var result CallToolResult
	json.Unmarshal(resp.Result, &result)

	if len(result.Content) == 0 {
		t.Error("expected content in tool result")
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected text content type, got %s", result.Content[0].Type)
	}
	// Verify the command actually executed (should contain "test" in output)
	if !strings.Contains(result.Content[0].Text, "test") {
		t.Errorf("expected output to contain 'test', got: %s", result.Content[0].Text)
	}
}

// --- Hub: JSON-RPC tools/call unknown tool ---

func TestHub_ToolsCall_UnknownTool(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"nonexistent_tool","arguments":{}},"id":4}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with error, got %d", w.Code)
	}

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == nil {
		t.Error("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected error code -32602, got %d", resp.Error.Code)
	}
}

// --- Hub: JSON-RPC method not found ---

func TestHub_MethodNotFound(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"nonexistent/method","id":5}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == nil {
		t.Error("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

// --- Hub: JSON-RPC invalid version ---

func TestHub_InvalidVersion(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"1.0","method":"initialize","id":6}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == nil {
		t.Error("expected error for invalid jsonrpc version")
	}
}

// --- Hub: JSON-RPC parse error ---

func TestHub_ParseError(t *testing.T) {
	hub := newTestHub()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Error == nil {
		t.Error("expected parse error")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("expected error code -32700, got %d", resp.Error.Code)
	}
}

// --- Hub: JSON-RPC ping ---

func TestHub_Ping(t *testing.T) {
	hub := newTestHub()

	body := `{"jsonrpc":"2.0","method":"ping","id":7}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ping: expected 200, got %d", w.Code)
	}
}

// --- Hub: Method not allowed ---

func TestHub_MethodNotAllowed(t *testing.T) {
	hub := newTestHub()

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	w := httptest.NewRecorder()

	hub.handleMCP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- Shell MCP Server ---

func TestShellMCPServer_ListTools(t *testing.T) {
	srv := NewShellMCPServer(os.TempDir())

	tools, err := srv.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) == 0 {
		t.Error("expected at least one tool")
	}

	found := false
	for _, tool := range tools {
		if tool.Name == "shell_exec" {
			found = true
			if tool.InputSchema.Type != "object" {
				t.Errorf("expected object schema, got %s", tool.InputSchema.Type)
			}
			if len(tool.InputSchema.Required) == 0 {
				t.Error("expected at least one required field")
			}
		}
	}
	if !found {
		t.Error("shell_exec tool not found")
	}
}

func TestShellMCPServer_CallTool(t *testing.T) {
	srv := NewShellMCPServer(os.TempDir())

	result, err := srv.CallTool(context.Background(), "shell_exec", map[string]interface{}{
		"command": "echo hello_world",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(result.Content) == 0 {
		t.Error("expected content in result")
	}
	// Verify the command actually executed
	if !strings.Contains(result.Content[0].Text, "hello_world") {
		t.Errorf("expected output to contain 'hello_world', got: %s", result.Content[0].Text)
	}
}

func TestShellMCPServer_CallTool_Unknown(t *testing.T) {
	srv := NewShellMCPServer(os.TempDir())

	_, err := srv.CallTool(context.Background(), "unknown_tool", nil)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

// --- File MCP Server ---

func TestFileMCPServer_ListTools(t *testing.T) {
	srv := NewFileMCPServer(os.TempDir())

	tools, err := srv.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"file_read", "file_write", "file_list", "file_search", "file_replace"}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("expected tool %s not found", name)
		}
	}
}

func TestFileMCPServer_ReadWrite(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-file-test-*")
	defer os.RemoveAll(tmpDir)

	srv := NewFileMCPServer(tmpDir)

	// Write a file
	result, err := srv.CallTool(context.Background(), "file_write", map[string]interface{}{
		"path":    "test.txt",
		"content": "hello from mcp",
	})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "Wrote") {
		t.Errorf("expected write confirmation, got: %s", result.Content[0].Text)
	}

	// Read it back
	result, err = srv.CallTool(context.Background(), "file_read", map[string]interface{}{
		"path": "test.txt",
	})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if result.Content[0].Text != "hello from mcp" {
		t.Errorf("expected 'hello from mcp', got: %s", result.Content[0].Text)
	}
}

func TestFileMCPServer_PathTraversal(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-file-traversal-*")
	defer os.RemoveAll(tmpDir)

	srv := NewFileMCPServer(tmpDir)

	// Attempt path traversal
	result, err := srv.CallTool(context.Background(), "file_read", map[string]interface{}{
		"path": "../../../etc/passwd",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for path traversal attempt")
	}
}

// --- Code MCP Server ---

func TestCodeMCPServer_ListTools(t *testing.T) {
	srv := NewCodeMCPServer(os.TempDir())

	tools, err := srv.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"code_run", "code_install"}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("expected tool %s not found", name)
		}
	}
}

func TestCodeMCPServer_RunPython(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-code-test-*")
	defer os.RemoveAll(tmpDir)

	srv := NewCodeMCPServer(tmpDir)

	result, err := srv.CallTool(context.Background(), "code_run", map[string]interface{}{
		"language": "python",
		"code":     "print(2 + 3)",
	})
	if err != nil {
		t.Fatalf("RunCode failed: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "5") {
		t.Errorf("expected output to contain '5', got: %s", result.Content[0].Text)
	}
}

func TestCodeMCPServer_RunNodeJS(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-code-test-*")
	defer os.RemoveAll(tmpDir)

	srv := NewCodeMCPServer(tmpDir)

	result, err := srv.CallTool(context.Background(), "code_run", map[string]interface{}{
		"language": "nodejs",
		"code":     "console.log(2 + 3)",
	})
	if err != nil {
		t.Fatalf("RunCode failed: %v", err)
	}
	if !strings.Contains(result.Content[0].Text, "5") {
		t.Errorf("expected output to contain '5', got: %s", result.Content[0].Text)
	}
}

// --- Browser MCP Server ---

func TestBrowserMCPServer_ListTools(t *testing.T) {
	srv := NewBrowserMCPServer(os.TempDir())

	tools, err := srv.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"browser_navigate", "browser_screenshot", "browser_click", "browser_type", "browser_eval"}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("expected tool %s not found", name)
		}
	}
}

func TestBrowserMCPServer_Navigate_NoChromium(t *testing.T) {
	srv := NewBrowserMCPServer(os.TempDir())

	// Without Chromium running, should return error gracefully
	result, err := srv.CallTool(context.Background(), "browser_navigate", map[string]interface{}{
		"url": "https://example.com",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	// Should not panic, should return error text
	if len(result.Content) == 0 {
		t.Error("expected content in result")
	}
}

// --- Integration: File operations through MCP Hub ---

func TestHub_FileWriteAndRead(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "nexusbox-hub-integration-*")
	defer os.RemoveAll(tmpDir)

	hub := NewHub(&HubConfig{Port: 0, Workspace: tmpDir})

	// Write a file via MCP
	writeBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"file_write","arguments":{"path":"hello.txt","content":"from MCP hub"}},"id":1}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	hub.handleMCP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write: expected 200, got %d", w.Code)
	}

	// Verify file exists on disk
	data, err := os.ReadFile(filepath.Join(tmpDir, "hello.txt"))
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(data) != "from MCP hub" {
		t.Errorf("expected 'from MCP hub', got '%s'", string(data))
	}

	// Read it back via MCP
	readBody := `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"file_read","arguments":{"path":"hello.txt"}},"id":2}`
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	hub.handleMCP(w, req)

	var resp JSONRPCMessage
	json.NewDecoder(w.Body).Decode(&resp)

	var result CallToolResult
	json.Unmarshal(resp.Result, &result)

	if result.Content[0].Text != "from MCP hub" {
		t.Errorf("expected 'from MCP hub', got: %s", result.Content[0].Text)
	}
}
