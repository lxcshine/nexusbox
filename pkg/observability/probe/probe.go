/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package probe implements process-level health probes for sandbox containers.
// Unlike simple PID existence checks, these probes verify the actual liveness
// of business processes inside the sandbox via exec, TCP, or HTTP checks.
//
// Probe types (mirrors Kubernetes probe semantics):
//   - ExecProbe: runs a command inside the sandbox; exit 0 = healthy
//   - TCPSocketProbe: opens a TCP connection; success = healthy
//   - HTTPGetProbe: performs an HTTP GET; 2xx/3xx status = healthy
package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

// ProbeType identifies the kind of probe.
type ProbeType string

const (
	ProbeTypeExec      ProbeType = "exec"
	ProbeTypeTCPSocket ProbeType = "tcp"
	ProbeTypeHTTPGet   ProbeType = "http"
)

// ProbeConfig describes how to probe a sandbox for liveness/readiness.
type ProbeConfig struct {
	// Type is the probe type (exec/tcp/http).
	Type ProbeType `json:"type"`

	// Exec is the command to execute for exec probes.
	// Example: ["/bin/sh", "-c", "pgrep -x nginx"]
	Exec []string `json:"exec,omitempty"`

	// TCPSocket is the target for TCP probes.
	TCPSocket *TCPSocketTarget `json:"tcpSocket,omitempty"`

	// HTTPGet is the target for HTTP probes.
	HTTPGet *HTTPGetTarget `json:"httpGet,omitempty"`

	// InitialDelaySeconds is the delay before the first probe.
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`

	// TimeoutSeconds is the time after which the probe times out.
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// PeriodSeconds is how often (in seconds) to perform the probe.
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`

	// SuccessThreshold is the minimum consecutive successes for the probe
	// to be considered successful after having failed.
	SuccessThreshold int32 `json:"successThreshold,omitempty"`

	// FailureThreshold is the minimum consecutive failures for the probe
	// to be considered failed after having succeeded.
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// TCPSocketTarget defines a TCP probe target.
type TCPSocketTarget struct {
	// Host is the target host (defaults to sandbox IP).
	Host string `json:"host,omitempty"`
	// Port is the target port (1-65535).
	Port int32 `json:"port"`
}

// HTTPGetTarget defines an HTTP probe target.
type HTTPGetTarget struct {
	// Host is the target host (defaults to sandbox IP).
	Host string `json:"host,omitempty"`
	// Port is the target port.
	Port int32 `json:"port"`
	// Path is the HTTP path (defaults to "/").
	Path string `json:"path,omitempty"`
	// Scheme is "http" or "https".
	Scheme string `json:"scheme,omitempty"`
}

// ExecRunner is the interface for executing commands inside a sandbox.
// The containerd Client implements this via ExecInSandbox.
type ExecRunner interface {
	ExecInSandbox(ctx context.Context, sandboxID string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (uint32, error)
}

// ProbeResult is the outcome of a single probe execution.
type ProbeResult struct {
	// Success indicates whether the probe succeeded.
	Success bool `json:"success"`
	// Message is a human-readable description.
	Message string `json:"message,omitempty"`
	// Duration is how long the probe took.
	Duration time.Duration `json:"duration"`
}

// Prober executes health probes against sandboxes.
type Prober struct {
	execRunner ExecRunner
	httpClient *http.Client
}

// NewProber creates a new Prober.
func NewProber(execRunner ExecRunner) *Prober {
	return &Prober{
		execRunner: execRunner,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Probe executes a single probe against the given sandbox.
// sandboxID identifies the sandbox; sandboxIP is used for TCP/HTTP probes
// (may be empty if the probe target is specified in the config).
func (p *Prober) Probe(ctx context.Context, sandboxID, sandboxIP string, cfg *ProbeConfig) ProbeResult {
	if cfg == nil {
		return ProbeResult{Success: false, Message: "no probe config"}
	}

	// Apply defaults
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 1 * time.Second
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var success bool
	var message string

	switch cfg.Type {
	case ProbeTypeExec:
		success, message = p.probeExec(probeCtx, sandboxID, cfg)
	case ProbeTypeTCPSocket:
		success, message = p.probeTCP(probeCtx, sandboxIP, cfg)
	case ProbeTypeHTTPGet:
		success, message = p.probeHTTP(probeCtx, sandboxIP, cfg)
	default:
		success = false
		message = fmt.Sprintf("unknown probe type: %s", cfg.Type)
	}

	return ProbeResult{
		Success:  success,
		Message:  message,
		Duration: time.Since(start),
	}
}

// probeExec runs a command inside the sandbox and checks the exit code.
func (p *Prober) probeExec(ctx context.Context, sandboxID string, cfg *ProbeConfig) (bool, string) {
	if len(cfg.Exec) == 0 {
		return false, "exec probe requires a command"
	}
	if p.execRunner == nil {
		return false, "no exec runner configured"
	}

	var stdout, stderr bytes.Buffer
	exitCode, err := p.execRunner.ExecInSandbox(ctx, sandboxID, cfg.Exec, nil, &stdout, &stderr)
	if err != nil {
		return false, fmt.Sprintf("exec failed: %v (stderr: %s)", err, stderr.String())
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("exec exited with code %d (stderr: %s)", exitCode, stderr.String())
	}
	return true, "exec succeeded"
}

// probeTCP attempts to establish a TCP connection to the target.
func (p *Prober) probeTCP(ctx context.Context, sandboxIP string, cfg *ProbeConfig) (bool, string) {
	if cfg.TCPSocket == nil {
		return false, "tcp probe requires tcpSocket config"
	}

	host := cfg.TCPSocket.Host
	if host == "" {
		host = sandboxIP
	}
	if host == "" {
		return false, "no host for tcp probe (sandbox has no IP and no host specified)"
	}

	addr := net.JoinHostPort(host, strconv.Itoa(int(cfg.TCPSocket.Port)))
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, fmt.Sprintf("tcp dial %s failed: %v", addr, err)
	}
	conn.Close()
	return true, fmt.Sprintf("tcp %s reachable", addr)
}

// probeHTTP performs an HTTP GET request against the target.
func (p *Prober) probeHTTP(ctx context.Context, sandboxIP string, cfg *ProbeConfig) (bool, string) {
	if cfg.HTTPGet == nil {
		return false, "http probe requires httpGet config"
	}

	host := cfg.HTTPGet.Host
	if host == "" {
		host = sandboxIP
	}
	if host == "" {
		return false, "no host for http probe (sandbox has no IP and no host specified)"
	}

	scheme := cfg.HTTPGet.Scheme
	if scheme == "" {
		scheme = "http"
	}
	path := cfg.HTTPGet.Path
	if path == "" {
		path = "/"
	}

	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, strconv.Itoa(int(cfg.HTTPGet.Port))),
		Path:   path,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, fmt.Sprintf("http request build failed: %v", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("http get %s failed: %v", u.String(), err)
	}
	defer resp.Body.Close()

	// 2xx and 3xx are considered healthy
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, fmt.Sprintf("http %s returned %d", u.String(), resp.StatusCode)
	}
	return false, fmt.Sprintf("http %s returned unhealthy status %d", u.String(), resp.StatusCode)
}

// ProbeLoop runs a probe periodically and invokes the callbacks on state changes.
// This is the long-running probe monitor used by the lifecycle manager.
type ProbeLoop struct {
	prober    *Prober
	sandboxID string
	config    *ProbeConfig

	// state
	consecutiveSuccesses int32
	consecutiveFailures  int32
	lastResult           bool

	// callbacks
	onHealthy   func()
	onUnhealthy func(reason string)

	stopCh chan struct{}
}

// NewProbeLoop creates a long-running probe monitor.
func NewProbeLoop(prober *Prober, sandboxID, sandboxIP string, cfg *ProbeConfig,
	onHealthy func(), onUnhealthy func(reason string)) *ProbeLoop {
	return &ProbeLoop{
		prober:      prober,
		sandboxID:   sandboxID,
		config:      cfg,
		onHealthy:   onHealthy,
		onUnhealthy: onUnhealthy,
		stopCh:      make(chan struct{}),
	}
}

// Run starts the probe loop. Blocks until Stop is called or ctx is canceled.
// The sandboxIP may be updated via UpdateIP if the sandbox IP changes.
func (pl *ProbeLoop) Run(ctx context.Context, sandboxIP string) {
	// Apply defaults
	period := time.Duration(pl.config.PeriodSeconds) * time.Second
	if period == 0 {
		period = 10 * time.Second
	}
	initialDelay := time.Duration(pl.config.InitialDelaySeconds) * time.Second
	successThreshold := pl.config.SuccessThreshold
	if successThreshold == 0 {
		successThreshold = 1
	}
	failureThreshold := pl.config.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = 3
	}

	// Wait for initial delay
	select {
	case <-time.After(initialDelay):
	case <-ctx.Done():
		return
	case <-pl.stopCh:
		return
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pl.stopCh:
			return
		case <-ticker.C:
			result := pl.prober.Probe(ctx, pl.sandboxID, sandboxIP, pl.config)
			klog.V(4).Infof("Probe for sandbox %s: success=%v duration=%v msg=%s",
				pl.sandboxID, result.Success, result.Duration, result.Message)

			if result.Success {
				pl.consecutiveSuccesses++
				pl.consecutiveFailures = 0
				if !pl.lastResult && pl.consecutiveSuccesses >= successThreshold {
					pl.lastResult = true
					if pl.onHealthy != nil {
						pl.onHealthy()
					}
				}
			} else {
				pl.consecutiveFailures++
				pl.consecutiveSuccesses = 0
				if pl.lastResult && pl.consecutiveFailures >= failureThreshold {
					pl.lastResult = false
					if pl.onUnhealthy != nil {
						pl.onUnhealthy(result.Message)
					}
				}
			}
		}
	}
}

// Stop signals the probe loop to stop.
func (pl *ProbeLoop) Stop() {
	close(pl.stopCh)
}
