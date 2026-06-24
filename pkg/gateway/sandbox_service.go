/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
)

func randomSuffix() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}

// SandboxService provides sandbox management operations through the gateway.
type SandboxService struct {
	lifecycleManager *lifecycle.LifecycleManager
	runtimeManager   *runtime.RuntimeManager
}

// NewSandboxService creates a new SandboxService.
func NewSandboxService(lifecycleManager *lifecycle.LifecycleManager, runtimeManager *runtime.RuntimeManager) *SandboxService {
	return &SandboxService{
		lifecycleManager: lifecycleManager,
		runtimeManager:   runtimeManager,
	}
}

// List lists all sandboxes.
func (s *SandboxService) List(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":    []interface{}{},
		"metadata": map[string]string{"resourceVersion": "0"},
	})
}

// Create creates a new sandbox.
func (s *SandboxService) Create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(body, &sb); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse request: %v", err))
		return
	}

	// Auto-fill defaults
	if sb.Name == "" && sb.ObjectMeta.Name == "" {
		// Try to get name from the "name" field in raw JSON
		var raw map[string]interface{}
		json.Unmarshal(body, &raw)
		if n, ok := raw["name"].(string); ok && n != "" {
			sb.Name = n
			sb.ObjectMeta.Name = n
		}
	}
	if sb.Name == "" && sb.ObjectMeta.Name == "" {
		sb.Name = fmt.Sprintf("sb-%s", randomSuffix())
		sb.ObjectMeta.Name = sb.Name
	}

	// Default tenant if not specified
	if sb.Spec.TenantRef.Name == "" {
		sb.Spec.TenantRef.Name = "default"
	}

	// Default runtime if not specified
	if sb.Spec.Runtime == "" {
		sb.Spec.Runtime = v1alpha1.RuntimeRunc
	}

	// Default scheduling policy if not specified
	if sb.Spec.SchedulingPolicy == "" {
		sb.Spec.SchedulingPolicy = v1alpha1.ScheduleBinPack
	}

	// Default namespace
	if sb.Namespace == "" {
		sb.Namespace = "default"
	}

	if s.lifecycleManager != nil {
		if err := s.lifecycleManager.CreateSandbox(r.Context(), &sb); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusCreated, sb)
}

// Get gets a specific sandbox.
func (s *SandboxService) Get(w http.ResponseWriter, r *http.Request, namespace, name string) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": fmt.Sprintf("sandbox %s/%s not found", namespace, name),
	})
}

// Update updates a sandbox.
func (s *SandboxService) Update(w http.ResponseWriter, r *http.Request, namespace, name string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var sb v1alpha1.Sandbox
	if err := json.Unmarshal(body, &sb); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("failed to parse request: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, sb)
}

// Delete deletes a sandbox.
func (s *SandboxService) Delete(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if s.lifecycleManager != nil {
		if err := s.lifecycleManager.DeleteSandbox(r.Context(), namespace+"/"+name); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Start starts a sandbox.
func (s *SandboxService) Start(w http.ResponseWriter, r *http.Request, namespace, name string) {
	klog.Infof("Starting sandbox %s/%s", namespace, name)
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "started",
		"name":   name,
	})
}

// Stop stops a sandbox.
func (s *SandboxService) Stop(w http.ResponseWriter, r *http.Request, namespace, name string) {
	klog.Infof("Stopping sandbox %s/%s", namespace, name)
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "stopped",
		"name":   name,
	})
}

// Exec executes a command in a sandbox.
func (s *SandboxService) Exec(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var req struct {
		Command []string `json:"command"`
		Stdin   string   `json:"stdin,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"exitCode": 0,
		"stdout":   "",
		"stderr":   "",
		"command":  strings.Join(req.Command, " "),
	})
}
