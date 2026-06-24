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
	"strings"
	"testing"
)

func newTestCodeService() *CodeService {
	return NewCodeService()
}

// --- Code Info ---

func TestCodeInfo(t *testing.T) {
	svc := newTestCodeService()

	req := httptest.NewRequest(http.MethodGet, "/v1/code/info", nil)
	w := httptest.NewRecorder()
	svc.Info(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("info: expected 200, got %d", w.Code)
	}

	var resp CodeInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.Languages) != 2 {
		t.Fatalf("expected 2 languages, got %d", len(resp.Languages))
	}

	// Check Python entry
	pyFound := false
	nodeFound := false
	for _, lang := range resp.Languages {
		if lang.Language == "python" {
			pyFound = true
		}
		if lang.Language == "nodejs" {
			nodeFound = true
		}
	}
	if !pyFound {
		t.Error("python language not found in info")
	}
	if !nodeFound {
		t.Error("nodejs language not found in info")
	}
}

// --- Code Execute: missing code ---

func TestCodeExecute_MissingCode(t *testing.T) {
	svc := newTestCodeService()

	body := `{"language":"python"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Code Execute: unsupported language ---

func TestCodeExecute_UnsupportedLanguage(t *testing.T) {
	svc := newTestCodeService()

	body := `{"language":"rust","code":"fn main() {}"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported language, got %d", w.Code)
	}
}

// --- Code Execute: Python ---

func TestCodeExecute_Python(t *testing.T) {
	svc := newTestCodeService()

	if svc.pythonPath == "" {
		t.Skip("python not available on this system")
	}

	body := `{"language":"python","code":"print('hello from python')"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute python: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello from python") {
		t.Errorf("expected stdout to contain 'hello from python', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "python" {
		t.Errorf("expected runtime=python, got %s", resp.Runtime)
	}
}

// --- Code Execute: Python with error ---

func TestCodeExecute_PythonError(t *testing.T) {
	svc := newTestCodeService()

	if svc.pythonPath == "" {
		t.Skip("python not available on this system")
	}

	body := `{"language":"python","code":"import sys; sys.exit(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ExitCode != 1 {
		t.Errorf("expected exitCode=1, got %d", resp.ExitCode)
	}
}

// --- Code Execute: Node.js ---

func TestCodeExecute_NodeJS(t *testing.T) {
	svc := newTestCodeService()

	if svc.nodePath == "" {
		t.Skip("node not available on this system")
	}

	body := `{"language":"nodejs","code":"console.log('hello from node')"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute nodejs: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello from node") {
		t.Errorf("expected stdout to contain 'hello from node', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "nodejs" {
		t.Errorf("expected runtime=nodejs, got %s", resp.Runtime)
	}
}

// --- Code Execute: Node.js with error ---

func TestCodeExecute_NodeJSError(t *testing.T) {
	svc := newTestCodeService()

	if svc.nodePath == "" {
		t.Skip("node not available on this system")
	}

	body := `{"language":"nodejs","code":"process.exit(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ExitCode != 1 {
		t.Errorf("expected exitCode=1, got %d", resp.ExitCode)
	}
}

// --- Code Execute: language aliases ---

func TestCodeExecute_LanguageAliases(t *testing.T) {
	svc := newTestCodeService()

	aliases := []string{"nodejs", "node", "javascript", "js"}
	for _, alias := range aliases {
		if svc.nodePath == "" {
			t.Skip("node not available")
		}
		body := `{"language":"` + alias + `","code":"console.log(1)"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svc.Execute(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("language alias '%s': expected 200, got %d", alias, w.Code)
		}
	}
}

// --- findExecutable ---

func TestFindExecutable_Existing(t *testing.T) {
	// On most systems, "go" should be available
	path := findExecutable("go")
	if path == "" {
		t.Skip("go executable not found in PATH")
	}
	if path == "" {
		t.Error("expected to find 'go' executable")
	}
}

func TestFindExecutable_NonExisting(t *testing.T) {
	path := findExecutable("nonexistent_binary_12345")
	if path != "" {
		t.Errorf("expected empty path for nonexistent binary, got %s", path)
	}
}
