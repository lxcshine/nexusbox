/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// PortProxy provides port forwarding and web preview capabilities.
// Inspired by agent-infra/sandbox's websocket proxy, it supports:
// - HTTP reverse proxy to container services
// - Port forwarding for sandbox services
// - Health checking of upstream services
type PortProxy struct {
	forwardings map[int]*Forwarding
	mu          sync.RWMutex
	httpServer  *http.Server
	port        int
	stopCh      chan struct{}
}

// Forwarding represents a port forwarding rule.
type Forwarding struct {
	LocalPort  int    `json:"localPort"`
	RemoteHost string `json:"remoteHost"`
	RemotePort int    `json:"remotePort"`
	Protocol   string `json:"protocol"` // "http" or "tcp"
	Active     bool   `json:"active"`
}

// PortProxyConfig holds configuration for the PortProxy.
type PortProxyConfig struct {
	Port int
}

// NewPortProxy creates a new PortProxy.
func NewPortProxy(config *PortProxyConfig) *PortProxy {
	return &PortProxy{
		forwardings: make(map[int]*Forwarding),
		port:        config.Port,
		stopCh:      make(chan struct{}),
	}
}

// Start starts the port proxy server.
func (p *PortProxy) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/", p.handleProxy)
	mux.HandleFunc("/preview/", p.handlePreview)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	p.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", p.port),
		Handler: mux,
	}

	// Probe port availability before starting the goroutine
	ln, err := net.Listen("tcp", p.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("port-proxy: failed to listen on %s: %w", p.httpServer.Addr, err)
	}

	go func() {
		klog.Infof("Port proxy listening on :%d", p.port)
		if err := p.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Port proxy error: %v", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			p.Shutdown()
		case <-p.stopCh:
			return
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the port proxy.
func (p *PortProxy) Shutdown() {
	close(p.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if p.httpServer != nil {
		p.httpServer.Shutdown(ctx)
	}
}

// AddForwarding adds a port forwarding rule.
func (p *PortProxy) AddForwarding(localPort int, remoteHost string, remotePort int, protocol string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forwardings[localPort] = &Forwarding{
		LocalPort:  localPort,
		RemoteHost: remoteHost,
		RemotePort: remotePort,
		Protocol:   protocol,
		Active:     true,
	}
	klog.Infof("Added port forwarding: %d -> %s:%d (%s)", localPort, remoteHost, remotePort, protocol)
}

// RemoveForwarding removes a port forwarding rule.
func (p *PortProxy) RemoveForwarding(localPort int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.forwardings, localPort)
}

// ListForwardings returns all port forwarding rules.
func (p *PortProxy) ListForwardings() []Forwarding {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]Forwarding, 0, len(p.forwardings))
	for _, f := range p.forwardings {
		result = append(result, *f)
	}
	return result
}

// handleProxy handles proxy requests.
func (p *PortProxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Path format: /proxy/{port}/{path...}
	path := strings.TrimPrefix(r.URL.Path, "/proxy/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "port is required", http.StatusBadRequest)
		return
	}

	var port int
	if _, err := fmt.Sscanf(parts[0], "%d", &port); err != nil {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	p.mu.RLock()
	fwd, ok := p.forwardings[port]
	p.mu.RUnlock()

	if !ok {
		// Default: proxy to localhost on the specified port
		fwd = &Forwarding{
			LocalPort:  port,
			RemoteHost: "localhost",
			RemotePort: port,
			Protocol:   "http",
			Active:     true,
		}
	}

	if !fwd.Active {
		http.Error(w, "forwarding not active", http.StatusServiceUnavailable)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", fwd.RemoteHost, fwd.RemotePort),
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		klog.Warningf("Proxy error for %s: %v", r.URL, err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}

	// Set the path correctly
	if len(parts) > 1 {
		r.URL.Path = "/" + parts[1]
	} else {
		r.URL.Path = "/"
	}

	proxy.ServeHTTP(w, r)
}

// handlePreview handles web preview requests.
func (p *PortProxy) handlePreview(w http.ResponseWriter, r *http.Request) {
	// Path format: /preview/{port}
	path := strings.TrimPrefix(r.URL.Path, "/preview/")
	portStr := strings.TrimSuffix(path, "/")

	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("localhost:%d", port),
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "preview unavailable", http.StatusBadGateway)
	}

	proxy.ServeHTTP(w, r)
}

// CheckPort checks if a port is accessible.
func (p *PortProxy) CheckPort(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// DiagnosePort diagnoses connectivity to a port.
func (p *PortProxy) DiagnosePort(host string, port int) map[string]interface{} {
	result := map[string]interface{}{
		"host": host,
		"port": port,
	}

	start := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	latency := time.Since(start)

	if err != nil {
		result["reachable"] = false
		result["error"] = err.Error()
	} else {
		result["reachable"] = true
		result["latency"] = latency.String()
		conn.Close()

		// Try HTTP check
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://%s:%d/", host, port))
		if err != nil {
			result["http"] = false
			result["httpError"] = err.Error()
		} else {
			result["http"] = true
			result["statusCode"] = resp.StatusCode
			resp.Body.Close()
		}
	}

	return result
}

// TCPTunnel forwards a TCP connection between local and remote.
func (p *PortProxy) TCPTunnel(ctx context.Context, localConn net.Conn, remoteAddr string) {
	remoteConn, err := net.DialTimeout("tcp", remoteAddr, 10*time.Second)
	if err != nil {
		klog.Warningf("Failed to connect to %s: %v", remoteAddr, err)
		localConn.Close()
		return
	}
	defer remoteConn.Close()

	// Bidirectional copy
	go func() {
		io.Copy(remoteConn, localConn)
		remoteConn.Close()
	}()
	go func() {
		io.Copy(localConn, remoteConn)
		localConn.Close()
	}()

	// Wait for context cancellation
	select {
	case <-ctx.Done():
		localConn.Close()
		remoteConn.Close()
	}
}
