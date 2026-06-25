// Package filestore provides file-based persistence for NexusBox.
// It uses atomic JSON file writes to ensure crash-safe state recovery.
// This is the default persistence backend for single-machine deployments;
// etcd can be used for clustered deployments.
package filestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// Store provides file-based persistence for sandbox state.
type Store struct {
	mu       sync.RWMutex
	baseDir  string
	dataPath string
	data     *storeData
	stopCh   chan struct{}
}

// storeData is the in-memory representation persisted to disk.
type storeData struct {
	Sandboxes  map[string]*sandboxv1alpha1.Sandbox   `json:"sandboxes"`
	Templates  map[string]*sandboxv1alpha1.SandboxTemplate `json:"templates"`
	Tenants    map[string]*sandboxv1alpha1.Tenant     `json:"tenants"`
	Snapshots  map[string]*SnapshotRecord             `json:"snapshots"`
	UpdatedAt  time.Time                              `json:"updated_at"`
}

// SnapshotRecord stores snapshot metadata.
type SnapshotRecord struct {
	ID          string    `json:"id"`
	SandboxID   string    `json:"sandbox_id"`
	Path        string    `json:"path"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description"`
}

// NewStore creates a new file-based store.
func NewStore(baseDir string) (*Store, error) {
	if baseDir == "" {
		baseDir = ".nexusbox"
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	s := &Store{
		baseDir:  baseDir,
		dataPath: filepath.Join(baseDir, "state.json"),
		data: &storeData{
			Sandboxes: make(map[string]*sandboxv1alpha1.Sandbox),
			Templates: make(map[string]*sandboxv1alpha1.SandboxTemplate),
			Tenants:   make(map[string]*sandboxv1alpha1.Tenant),
			Snapshots: make(map[string]*SnapshotRecord),
			UpdatedAt: time.Now(),
		},
		stopCh: make(chan struct{}),
	}

	// Load existing state if available.
	if err := s.load(); err != nil {
		klog.Warningf("Failed to load state: %v (starting fresh)", err)
	}

	return s, nil
}

// Start starts the periodic flush goroutine.
func (s *Store) Start(stopCh <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.Flush(); err != nil {
				klog.Warningf("Failed to flush state: %v", err)
			}
		case <-stopCh:
			// Final flush on shutdown.
			if err := s.Flush(); err != nil {
				klog.Warningf("Failed to flush state on shutdown: %v", err)
			}
			return
		case <-s.stopCh:
			return
		}
	}
}

// Stop stops the store.
func (s *Store) Stop() {
	close(s.stopCh)
}

// Flush writes the in-memory state to disk atomically.
func (s *Store) Flush() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.data.UpdatedAt = time.Now()
	return s.writeAtomic()
}

// load reads the state from disk.
func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing state, start fresh.
		}
		return err
	}

	var loaded storeData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	if loaded.Sandboxes == nil {
		loaded.Sandboxes = make(map[string]*sandboxv1alpha1.Sandbox)
	}
	if loaded.Templates == nil {
		loaded.Templates = make(map[string]*sandboxv1alpha1.SandboxTemplate)
	}
	if loaded.Tenants == nil {
		loaded.Tenants = make(map[string]*sandboxv1alpha1.Tenant)
	}
	if loaded.Snapshots == nil {
		loaded.Snapshots = make(map[string]*SnapshotRecord)
	}

	s.data = &loaded
	klog.Infof("Loaded state from %s: %d sandboxes, %d templates", s.dataPath, len(loaded.Sandboxes), len(loaded.Templates))
	return nil
}

// writeAtomic writes the state to disk atomically using a temp file + rename.
func (s *Store) writeAtomic() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to a temp file first, then rename for atomicity.
	tmpPath := s.dataPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	if err := os.Rename(tmpPath, s.dataPath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}

// --- Sandbox CRUD ---

// CreateSandbox persists a sandbox.
func (s *Store) CreateSandbox(sb *sandboxv1alpha1.Sandbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sandboxes[sb.Name] = sb
	return nil
}

// GetSandbox retrieves a sandbox by name.
func (s *Store) GetSandbox(name string) (*sandboxv1alpha1.Sandbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sb, ok := s.data.Sandboxes[name]
	if !ok {
		return nil, fmt.Errorf("sandbox %s not found", name)
	}
	return sb, nil
}

// ListSandboxes returns all sandboxes.
func (s *Store) ListSandboxes() ([]*sandboxv1alpha1.Sandbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*sandboxv1alpha1.Sandbox, 0, len(s.data.Sandboxes))
	for _, sb := range s.data.Sandboxes {
		result = append(result, sb)
	}
	return result, nil
}

// UpdateSandbox updates an existing sandbox.
func (s *Store) UpdateSandbox(sb *sandboxv1alpha1.Sandbox) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.data.Sandboxes[sb.Name]; !exists {
		return fmt.Errorf("sandbox %s not found", sb.Name)
	}
	s.data.Sandboxes[sb.Name] = sb
	return nil
}

// DeleteSandbox removes a sandbox.
func (s *Store) DeleteSandbox(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sandboxes, name)
	return nil
}

// --- Template CRUD ---

// CreateTemplate persists a template.
func (s *Store) CreateTemplate(t *sandboxv1alpha1.SandboxTemplate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Templates[t.Name] = t
	return nil
}

// GetTemplate retrieves a template by name.
func (s *Store) GetTemplate(name string) (*sandboxv1alpha1.SandboxTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.data.Templates[name]
	if !ok {
		return nil, fmt.Errorf("template %s not found", name)
	}
	return t, nil
}

// ListTemplates returns all templates.
func (s *Store) ListTemplates() ([]*sandboxv1alpha1.SandboxTemplate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*sandboxv1alpha1.SandboxTemplate, 0, len(s.data.Templates))
	for _, t := range s.data.Templates {
		result = append(result, t)
	}
	return result, nil
}

// DeleteTemplate removes a template.
func (s *Store) DeleteTemplate(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Templates, name)
	return nil
}

// --- Snapshot CRUD ---

// SaveSnapshot persists a snapshot record.
func (s *Store) SaveSnapshot(snapshot *SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Snapshots[snapshot.ID] = snapshot
	return nil
}

// GetSnapshot retrieves a snapshot by ID.
func (s *Store) GetSnapshot(id string) (*SnapshotRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.data.Snapshots[id]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", id)
	}
	return snap, nil
}

// ListSnapshots returns all snapshots for a sandbox.
func (s *Store) ListSnapshots(sandboxID string) ([]*SnapshotRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*SnapshotRecord
	for _, snap := range s.data.Snapshots {
		if snap.SandboxID == sandboxID {
			result = append(result, snap)
		}
	}
	return result, nil
}

// DeleteSnapshot removes a snapshot record.
func (s *Store) DeleteSnapshot(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Snapshots, id)
	return nil
}

// --- Tenant CRUD ---

// CreateTenant persists a tenant.
func (s *Store) CreateTenant(t *sandboxv1alpha1.Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Tenants[t.Name] = t
	return nil
}

// GetTenant retrieves a tenant by name.
func (s *Store) GetTenant(name string) (*sandboxv1alpha1.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.data.Tenants[name]
	if !ok {
		return nil, fmt.Errorf("tenant %s not found", name)
	}
	return t, nil
}

// ListTenants returns all tenants.
func (s *Store) ListTenants() ([]*sandboxv1alpha1.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*sandboxv1alpha1.Tenant, 0, len(s.data.Tenants))
	for _, t := range s.data.Tenants {
		result = append(result, t)
	}
	return result, nil
}

// DeleteTenant removes a tenant.
func (s *Store) DeleteTenant(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Tenants, name)
	return nil
}
