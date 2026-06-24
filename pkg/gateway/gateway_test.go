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

// newTestGateway creates a Gateway for testing without real dependencies.
func newTestGateway() *Gateway {
	return NewGateway(&GatewayConfig{
		Port:      0, // random port, not used in direct handler tests
		Workspace: "/tmp/nexusbox-test",
	})
}

// testHandler creates a test request against the gateway's handler chain.
func (g *Gateway) testHandler() http.Handler {
	mux := http.NewServeMux()
	g.registerRoutes(mux)
	return corsMiddleware(authMiddleware(mux))
}

// --- Health endpoints ---

func TestHealthz(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz: expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Errorf("healthz: expected 'ok', got '%s'", body)
	}
}

func TestReadyz(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("readyz: expected 200, got %d", w.Code)
	}
}

// --- System env ---

func TestSystemEnv(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/system/env", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("system/env: expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["workspace"] != "/tmp/nexusbox-test" && result["workspace"] != "D:\\tmp\\nexusbox-test" {
		t.Errorf("expected workspace=/tmp/nexusbox-test, got %v", result["workspace"])
	}
	if result["apiVersion"] != "v1" {
		t.Errorf("expected apiVersion=v1, got %v", result["apiVersion"])
	}
}

// --- CORS middleware ---

func TestCORSMiddleware(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	// Preflight request
	req := httptest.NewRequest(http.MethodOptions, "/v1/shell/exec", nil)
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("CORS preflight: expected 204, got %d", w.Code)
	}
	if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Errorf("CORS origin: expected '*', got '%s'", origin)
	}
	if methods := w.Header().Get("Access-Control-Allow-Methods"); methods == "" {
		t.Error("CORS methods header missing")
	}
}

// --- Method not allowed ---

func TestMethodNotAllowed(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	endpoints := []struct {
		path   string
		method string
	}{
		{"/v1/shell/exec", http.MethodGet},
		{"/v1/file/read", http.MethodGet},
		{"/v1/browser/screenshot", http.MethodGet},
		{"/v1/code/execute", http.MethodGet},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: expected 405, got %d", ep.method, ep.path, w.Code)
		}
	}
}

// --- Sandbox endpoints ---

func TestListSandboxes(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list sandboxes: expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	items, ok := result["items"].([]interface{})
	if !ok {
		t.Fatal("items not found or wrong type")
	}
	if len(items) != 0 {
		t.Errorf("expected empty items, got %d", len(items))
	}
}

// --- Tenant endpoints ---

func TestCreateTenant(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	body := `{"name":"test-tenant","quota":{"cpu":4,"memory":"8Gi"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create tenant: expected 201, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["name"] != "test-tenant" {
		t.Errorf("expected name=test-tenant, got %v", result["name"])
	}
}

func TestGetTenant(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/my-tenant", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get tenant: expected 200, got %d", w.Code)
	}
}

// --- Proxy endpoints ---

func TestProxyHealth(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/proxy/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("proxy health: expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", result["status"])
	}
}

// --- X-Sandbox-ID header propagation ---

func TestSandboxIDHeaderPropagation(t *testing.T) {
	g := newTestGateway()
	handler := g.testHandler()

	req := httptest.NewRequest(http.MethodGet, "/v1/system/env", nil)
	req.Header.Set("X-Sandbox-ID", "sb-12345")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should succeed (auth middleware passes through with sandbox ID in context)
	if w.Code != http.StatusOK {
		t.Errorf("sandbox ID header: expected 200, got %d", w.Code)
	}
}

// --- writeJSON / writeError helpers ---

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"hello": "world"})

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected application/json content type, got %s", w.Header().Get("Content-Type"))
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["hello"] != "world" {
		t.Errorf("expected hello=world, got %s", result["hello"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad request")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if result["error"] != "bad request" {
		t.Errorf("expected error='bad request', got '%s'", result["error"])
	}
}
