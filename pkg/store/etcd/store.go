package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"go.etcd.io/etcd/client/v3"
	"k8s.io/klog/v2"
)

const (
	keyPrefixSandbox = "/nexusbox/sandboxes/"
	keyPrefixTenant  = "/nexusbox/tenants/"
	keyPrefixNode    = "/nexusbox/nodes/"
)

// Store provides persistent storage backed by etcd.
type Store struct {
	client *clientv3.Client
	prefix string
}

// StoreConfig holds configuration for the etcd store.
type StoreConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
	Prefix      string
}

// DefaultStoreConfig returns a default store configuration.
func DefaultStoreConfig() *StoreConfig {
	return &StoreConfig{
		Endpoints:   []string{"http://127.0.0.1:2379"},
		DialTimeout: 5 * time.Second,
		Prefix:      "/nexusbox",
	}
}

// NewStore creates a new etcd-backed store.
func NewStore(config *StoreConfig) (*Store, error) {
	if config == nil {
		config = DefaultStoreConfig()
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: config.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	klog.Infof("Connected to etcd at %v", config.Endpoints)
	return &Store{client: client, prefix: config.Prefix}, nil
}

// Close closes the etcd client.
func (s *Store) Close() error {
	return s.client.Close()
}

// --- Sandbox CRUD ---

// CreateSandbox persists a sandbox.
func (s *Store) CreateSandbox(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	key := keyPrefixSandbox + sb.Namespace + "/" + sb.Name
	data, err := json.Marshal(sb)
	if err != nil {
		return fmt.Errorf("failed to marshal sandbox: %w", err)
	}
	_, err = s.client.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("failed to put sandbox %s/%s: %w", sb.Namespace, sb.Name, err)
	}
	klog.V(4).Infof("Stored sandbox %s/%s in etcd", sb.Namespace, sb.Name)
	return nil
}

// GetSandbox retrieves a sandbox by namespace and name.
func (s *Store) GetSandbox(ctx context.Context, namespace, name string) (*sandboxv1alpha1.Sandbox, error) {
	key := keyPrefixSandbox + namespace + "/" + name
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox %s/%s: %w", namespace, name, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("sandbox %s/%s not found", namespace, name)
	}
	var sb sandboxv1alpha1.Sandbox
	if err := json.Unmarshal(resp.Kvs[0].Value, &sb); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sandbox: %w", err)
	}
	return &sb, nil
}

// UpdateSandbox updates an existing sandbox.
func (s *Store) UpdateSandbox(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	return s.CreateSandbox(ctx, sb)
}

// DeleteSandbox removes a sandbox from etcd.
func (s *Store) DeleteSandbox(ctx context.Context, namespace, name string) error {
	key := keyPrefixSandbox + namespace + "/" + name
	_, err := s.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to delete sandbox %s/%s: %w", namespace, name, err)
	}
	klog.V(4).Infof("Deleted sandbox %s/%s from etcd", namespace, name)
	return nil
}

// ListSandboxes lists all sandboxes, optionally filtered by namespace.
func (s *Store) ListSandboxes(ctx context.Context, namespace string) ([]*sandboxv1alpha1.Sandbox, error) {
	prefix := keyPrefixSandbox
	if namespace != "" {
		prefix = keyPrefixSandbox + namespace + "/"
	}
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}
	var sandboxes []*sandboxv1alpha1.Sandbox
	for _, kv := range resp.Kvs {
		var sb sandboxv1alpha1.Sandbox
		if err := json.Unmarshal(kv.Value, &sb); err != nil {
			klog.Warningf("Failed to unmarshal sandbox at key %s: %v", string(kv.Key), err)
			continue
		}
		sandboxes = append(sandboxes, &sb)
	}
	return sandboxes, nil
}

// WatchSandboxes watches for changes to sandboxes.
func (s *Store) WatchSandboxes(ctx context.Context, namespace string) clientv3.WatchChan {
	prefix := keyPrefixSandbox
	if namespace != "" {
		prefix = keyPrefixSandbox + namespace + "/"
	}
	return s.client.Watch(ctx, prefix, clientv3.WithPrefix())
}

// --- Tenant CRUD ---

// CreateTenant persists a tenant.
func (s *Store) CreateTenant(ctx context.Context, tn *sandboxv1alpha1.Tenant) error {
	key := keyPrefixTenant + tn.Name
	data, err := json.Marshal(tn)
	if err != nil {
		return fmt.Errorf("failed to marshal tenant: %w", err)
	}
	_, err = s.client.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("failed to put tenant %s: %w", tn.Name, err)
	}
	return nil
}

// GetTenant retrieves a tenant by name.
func (s *Store) GetTenant(ctx context.Context, name string) (*sandboxv1alpha1.Tenant, error) {
	key := keyPrefixTenant + name
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant %s: %w", name, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("tenant %s not found", name)
	}
	var tn sandboxv1alpha1.Tenant
	if err := json.Unmarshal(resp.Kvs[0].Value, &tn); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tenant: %w", err)
	}
	return &tn, nil
}

// UpdateTenant updates an existing tenant.
func (s *Store) UpdateTenant(ctx context.Context, tn *sandboxv1alpha1.Tenant) error {
	return s.CreateTenant(ctx, tn)
}

// DeleteTenant removes a tenant from etcd.
func (s *Store) DeleteTenant(ctx context.Context, name string) error {
	key := keyPrefixTenant + name
	_, err := s.client.Delete(ctx, key)
	return err
}

// ListTenants lists all tenants.
func (s *Store) ListTenants(ctx context.Context) ([]*sandboxv1alpha1.Tenant, error) {
	resp, err := s.client.Get(ctx, keyPrefixTenant, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list tenants: %w", err)
	}
	var tenants []*sandboxv1alpha1.Tenant
	for _, kv := range resp.Kvs {
		var tn sandboxv1alpha1.Tenant
		if err := json.Unmarshal(kv.Value, &tn); err != nil {
			continue
		}
		tenants = append(tenants, &tn)
	}
	return tenants, nil
}

// WatchTenants watches for changes to tenants.
func (s *Store) WatchTenants(ctx context.Context) clientv3.WatchChan {
	return s.client.Watch(ctx, keyPrefixTenant, clientv3.WithPrefix())
}

// --- Node CRUD ---

// CreateNode persists a node.
func (s *Store) CreateNode(ctx context.Context, node *sandboxv1alpha1.SandboxNode) error {
	key := keyPrefixNode + node.Name
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node: %w", err)
	}
	_, err = s.client.Put(ctx, key, string(data))
	return err
}

// GetNode retrieves a node by name.
func (s *Store) GetNode(ctx context.Context, name string) (*sandboxv1alpha1.SandboxNode, error) {
	key := keyPrefixNode + name
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("node %s not found", name)
	}
	var node sandboxv1alpha1.SandboxNode
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

// UpdateNode updates an existing node.
func (s *Store) UpdateNode(ctx context.Context, node *sandboxv1alpha1.SandboxNode) error {
	return s.CreateNode(ctx, node)
}

// DeleteNode removes a node from etcd.
func (s *Store) DeleteNode(ctx context.Context, name string) error {
	key := keyPrefixNode + name
	_, err := s.client.Delete(ctx, key)
	return err
}

// ListNodes lists all nodes.
func (s *Store) ListNodes(ctx context.Context) ([]*sandboxv1alpha1.SandboxNode, error) {
	resp, err := s.client.Get(ctx, keyPrefixNode, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	var nodes []*sandboxv1alpha1.SandboxNode
	for _, kv := range resp.Kvs {
		var node sandboxv1alpha1.SandboxNode
		if err := json.Unmarshal(kv.Value, &node); err != nil {
			continue
		}
		nodes = append(nodes, &node)
	}
	return nodes, nil
}

// WatchNodes watches for changes to nodes.
func (s *Store) WatchNodes(ctx context.Context) clientv3.WatchChan {
	return s.client.Watch(ctx, keyPrefixNode, clientv3.WithPrefix())
}

// --- Transactional Operations ---

// UpdateSandboxStatus atomically updates a sandbox's status.
func (s *Store) UpdateSandboxStatus(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	key := keyPrefixSandbox + sb.Namespace + "/" + sb.Name
	data, err := json.Marshal(sb)
	if err != nil {
		return err
	}
	// Use a transaction to ensure we don't overwrite a newer version
	txn := s.client.Txn(ctx).
		Then(clientv3.OpPut(key, string(data)))
	_, err = txn.Commit()
	return err
}

// LeaseGrant creates a new lease for heartbeat/keepalive.
func (s *Store) LeaseGrant(ctx context.Context, ttl int64) (clientv3.LeaseID, error) {
	resp, err := s.client.Lease.Grant(ctx, ttl)
	if err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// LeaseKeepAlive keeps a lease alive.
func (s *Store) LeaseKeepAlive(ctx context.Context, id clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	return s.client.Lease.KeepAlive(ctx, id)
}

// PutWithLease stores a key with an associated lease (auto-expire).
func (s *Store) PutWithLease(ctx context.Context, key, value string, id clientv3.LeaseID) error {
	_, err := s.client.Put(ctx, key, value, clientv3.WithLease(id))
	return err
}
