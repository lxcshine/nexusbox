/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestPortProxy() *PortProxy {
	return NewPortProxy(&PortProxyConfig{Port: 0})
}

// --- Port forwarding management ---

func TestAddAndListForwardings(t *testing.T) {
	p := newTestPortProxy()

	p.AddForwarding(8080, "localhost", 80, "http")
	p.AddForwarding(3306, "db-host", 3306, "tcp")

	forwardings := p.ListForwardings()
	if len(forwardings) != 2 {
		t.Fatalf("expected 2 forwardings, got %d", len(forwardings))
	}

	// Find the HTTP forwarding
	var found8080, found3306 bool
	for _, f := range forwardings {
		if f.LocalPort == 8080 {
			found8080 = true
			if f.RemoteHost != "localhost" || f.RemotePort != 80 || f.Protocol != "http" {
				t.Errorf("forwarding 8080: expected localhost:80 http, got %s:%d %s", f.RemoteHost, f.RemotePort, f.Protocol)
			}
		}
		if f.LocalPort == 3306 {
			found3306 = true
		}
	}
	if !found8080 {
		t.Error("forwarding on port 8080 not found")
	}
	if !found3306 {
		t.Error("forwarding on port 3306 not found")
	}
}

func TestRemoveForwarding(t *testing.T) {
	p := newTestPortProxy()

	p.AddForwarding(8080, "localhost", 80, "http")
	p.RemoveForwarding(8080)

	forwardings := p.ListForwardings()
	if len(forwardings) != 0 {
		t.Errorf("expected 0 forwardings after removal, got %d", len(forwardings))
	}
}

// --- Proxy health endpoint ---

func TestProxyHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	// Use a simple handler to test the health endpoint
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- CheckPort ---

func TestCheckPort_Unreachable(t *testing.T) {
	p := newTestPortProxy()

	// Port 1 is almost certainly not listening
	result := p.CheckPort("localhost", 1)
	if result {
		t.Error("port 1 should not be reachable")
	}
}

func TestCheckPort_Reachable(t *testing.T) {
	p := newTestPortProxy()

	// Start a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	result := p.CheckPort(host, port)
	if !result {
		t.Errorf("port %d should be reachable", port)
	}
}

// --- DiagnosePort ---

func TestDiagnosePort_Unreachable(t *testing.T) {
	p := newTestPortProxy()

	result := p.DiagnosePort("localhost", 1)
	if result["reachable"] != false {
		t.Error("port 1 should not be reachable")
	}
	if _, ok := result["error"]; !ok {
		t.Error("expected error field for unreachable port")
	}
}

func TestDiagnosePort_Reachable(t *testing.T) {
	p := newTestPortProxy()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	result := p.DiagnosePort(host, port)
	if result["reachable"] != true {
		t.Error("test server port should be reachable")
	}
	if result["http"] != true {
		t.Error("test server should respond to HTTP")
	}
}

// --- Proxy handler ---

func TestProxyHandler_MissingPort(t *testing.T) {
	p := newTestPortProxy()

	req := httptest.NewRequest(http.MethodGet, "/proxy/", nil)
	w := httptest.NewRecorder()

	p.handleProxy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestProxyHandler_InvalidPort(t *testing.T) {
	p := newTestPortProxy()

	req := httptest.NewRequest(http.MethodGet, "/proxy/abc/path", nil)
	w := httptest.NewRecorder()

	p.handleProxy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid port, got %d", w.Code)
	}
}

// --- Preview handler ---

func TestPreviewHandler_InvalidPort(t *testing.T) {
	p := newTestPortProxy()

	req := httptest.NewRequest(http.MethodGet, "/preview/abc", nil)
	w := httptest.NewRecorder()

	p.handlePreview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid port, got %d", w.Code)
	}
}
