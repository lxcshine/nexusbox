/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// HealthChecker manages health and readiness checks.
type HealthChecker struct {
	mu       sync.RWMutex
	checks   map[string]CheckFunc
	live     bool
	ready    bool
	startTime time.Time
}

// CheckFunc is a function that performs a health check.
type CheckFunc func(ctx context.Context) error

// CheckResult represents the result of a health check.
type CheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HealthResponse is the response for health endpoints.
type HealthResponse struct {
	Status    string        `json:"status"`
	Checks    []CheckResult `json:"checks,omitempty"`
	Uptime    string        `json:"uptime,omitempty"`
	Timestamp string        `json:"timestamp"`
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		checks:    make(map[string]CheckFunc),
		live:      true,
		ready:     false,
		startTime: time.Now(),
	}
}

// AddCheck adds a health check.
func (h *HealthChecker) AddCheck(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// SetReady marks the service as ready.
func (h *HealthChecker) SetReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
	if ready {
		klog.Info("Service is now ready")
	} else {
		klog.Info("Service is no longer ready")
	}
}

// SetLive marks the service as alive.
func (h *HealthChecker) SetLive(live bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.live = live
}

// HealthzHandler returns an HTTP handler for the /healthz endpoint.
func (h *HealthChecker) HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		live := h.live
		h.mu.RUnlock()

		if !live {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(HealthResponse{
				Status:    "unhealthy",
				Timestamp: time.Now().Format(time.RFC3339),
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    "healthy",
			Uptime:    time.Since(h.startTime).String(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

// ReadyzHandler returns an HTTP handler for the /readyz endpoint.
func (h *HealthChecker) ReadyzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		h.mu.RLock()
		ready := h.ready
		checks := make(map[string]CheckFunc, len(h.checks))
		for k, v := range h.checks {
			checks[k] = v
		}
		h.mu.RUnlock()

		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(HealthResponse{
				Status:    "not_ready",
				Timestamp: time.Now().Format(time.RFC3339),
			})
			return
		}

		// Run all checks
		var results []CheckResult
		allHealthy := true
		for name, fn := range checks {
			result := CheckResult{Name: name, Status: "healthy"}
			if err := fn(ctx); err != nil {
				result.Status = "unhealthy"
				result.Error = err.Error()
				allHealthy = false
			}
			results = append(results, result)
		}

		status := "ready"
		code := http.StatusOK
		if !allHealthy {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}

		w.WriteHeader(code)
		json.NewEncoder(w).Encode(HealthResponse{
			Status:    status,
			Checks:    results,
			Uptime:    time.Since(h.startTime).String(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
	}
}

// LivezHandler returns an HTTP handler for the /livez endpoint.
func (h *HealthChecker) LivezHandler() http.HandlerFunc {
	return h.HealthzHandler()
}
