/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer creates a mock HTTP server for SDK testing.
func newTestServer(handler http.Handler) *httptest.Server {
	return httptest.NewServer(handler)
}

// mockHandler returns a handler that responds with the given status and body.
func mockHandler(statusCode int, body interface{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if body != nil {
			json.NewEncoder(w).Encode(body)
		}
	})
}

// --- Client creation ---

func TestNewClient(t *testing.T) {
	client := NewClient(&ClientConfig{
		BaseURL:   "http://localhost:8080",
		APIKey:    "test-key",
		SandboxID: "sb-123",
	})

	if client.baseURL != "http://localhost:8080" {
		t.Errorf("expected baseURL=http://localhost:8080, got %s", client.baseURL)
	}
	if client.apiKey != "test-key" {
		t.Errorf("expected apiKey=test-key, got %s", client.apiKey)
	}
	if client.sandboxID != "sb-123" {
		t.Errorf("expected sandboxID=sb-123, got %s", client.sandboxID)
	}
	if client.Sandbox == nil {
		t.Error("Sandbox service should not be nil")
	}
	if client.Shell == nil {
		t.Error("Shell service should not be nil")
	}
	if client.File == nil {
		t.Error("File service should not be nil")
	}
	if client.Browser == nil {
		t.Error("Browser service should not be nil")
	}
	if client.Code == nil {
		t.Error("Code service should not be nil")
	}
	if client.MCP == nil {
		t.Error("MCP service should not be nil")
	}
}

func TestNewClient_TrailingSlash(t *testing.T) {
	client := NewClient(&ClientConfig{
		BaseURL: "http://localhost:8080/",
	})
	if client.baseURL != "http://localhost:8080" {
		t.Errorf("trailing slash should be trimmed, got %s", client.baseURL)
	}
}

// --- Shell service ---

func TestShellService_Exec(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusOK, ExecResponse{
		ExitCode: 0,
		Stdout:   "hello",
		Stderr:   "",
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.Shell.Exec(context.Background(), &ExecRequest{
		Command: "echo hello",
	})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if resp.Stdout != "hello" {
		t.Errorf("expected stdout=hello, got %s", resp.Stdout)
	}
}

func TestShellService_CreateSession(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusCreated, SessionInfo{
		ID:     "session-1",
		Name:   "test",
		Shell:  "/bin/bash",
		Active: true,
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.Shell.CreateSession(context.Background(), &CreateSessionRequest{
		Name: "test",
	})
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	if resp.ID != "session-1" {
		t.Errorf("expected id=session-1, got %s", resp.ID)
	}
}

// --- File service ---

func TestFileService_Read(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusOK, FileReadResponse{
		Path:    "test.txt",
		Content: "file content",
		Size:    12,
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.File.Read(context.Background(), &FileReadRequest{
		Path: "test.txt",
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if resp.Content != "file content" {
		t.Errorf("expected content='file content', got '%s'", resp.Content)
	}
}

// --- Browser service ---

func TestBrowserService_Navigate(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusOK, NavigateResponse{
		URL:     "https://example.com",
		Success: true,
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.Browser.Navigate(context.Background(), &NavigateRequest{
		URL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestBrowserService_Screenshot(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusOK, ScreenshotResponse{
		Image:  "base64data",
		Format: "png",
		Width:  1280,
		Height: 1024,
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.Browser.Screenshot(context.Background(), &ScreenshotRequest{
		Format: "png",
	})
	if err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	if resp.Format != "png" {
		t.Errorf("expected format=png, got %s", resp.Format)
	}
}

// --- Code service ---

func TestCodeService_Execute(t *testing.T) {
	server := newTestServer(mockHandler(http.StatusOK, CodeExecuteResponse{
		ExitCode: 0,
		Stdout:   "output",
		Runtime:  "python",
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	resp, err := client.Code.Execute(context.Background(), &CodeExecuteRequest{
		Language: "python",
		Code:     "print('output')",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if resp.Runtime != "python" {
		t.Errorf("expected runtime=python, got %s", resp.Runtime)
	}
}

// --- Error handling ---

func TestClient_APIError(t *testing.T) {
	server := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{BaseURL: server.URL})
	_, err := client.Shell.Exec(context.Background(), &ExecRequest{
		Command: "test",
	})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

// --- Auth header ---

func TestClient_AuthHeader(t *testing.T) {
	var receivedAuth string
	var receivedSandboxID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedSandboxID = r.Header.Get("X-Sandbox-ID")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(ExecResponse{})
	}))
	defer server.Close()

	client := NewClient(&ClientConfig{
		BaseURL:   server.URL,
		APIKey:    "my-api-key",
		SandboxID: "sb-456",
	})
	client.Shell.Exec(context.Background(), &ExecRequest{Command: "test"})

	if receivedAuth != "Bearer my-api-key" {
		t.Errorf("expected Authorization=Bearer my-api-key, got %s", receivedAuth)
	}
	if receivedSandboxID != "sb-456" {
		t.Errorf("expected X-Sandbox-ID=sb-456, got %s", receivedSandboxID)
	}
}
