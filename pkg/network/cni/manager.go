/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package cni

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// NetworkManager manages CNI network configuration for sandboxes.
type NetworkManager struct {
	mu         sync.RWMutex
	cniBinDir  string
	cniConfDir string
	ipamStore  *IPAMStore
	networks   map[string]*NetworkConfig
}

// NetworkConfig represents a CNI network configuration.
type NetworkConfig struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Plugin       string          `json:"plugin,omitempty"`
	Subnet       string          `json:"subnet"`
	Gateway      string          `json:"gateway,omitempty"`
	IPRange      string          `json:"ipRange,omitempty"`
	Routes       []RouteConfig   `json:"routes,omitempty"`
	DNS          DNSConfig       `json:"dns,omitempty"`
	Capabilities map[string]bool `json:"capabilities,omitempty"`
}

// RouteConfig represents a network route.
type RouteConfig struct {
	Dst string `json:"dst"`
	GW  string `json:"gw,omitempty"`
}

// DNSConfig represents DNS configuration for CNI.
type DNSConfig struct {
	Nameservers []string `json:"nameservers,omitempty"`
	Domain      string   `json:"domain,omitempty"`
	Search      []string `json:"search,omitempty"`
}

// NetworkResult contains the result of network setup.
type NetworkResult struct {
	InterfaceName string
	IP            net.IP
	Gateway       net.IP
	DNS           DNSConfig
	NetNSPath     string
}

// IPAMStore manages IP address allocation.
type IPAMStore struct {
	mu          sync.Mutex
	allocations map[string]string // IP -> sandboxID
	subnet      *net.IPNet
	nextIP      net.IP
}

// NewNetworkManager creates a new CNI network manager.
func NewNetworkManager(cniBinDir, cniConfDir string, subnet string) (*NetworkManager, error) {
	if cniBinDir == "" {
		cniBinDir = "/opt/cni/bin"
	}
	if cniConfDir == "" {
		cniConfDir = "/etc/cni/net.d"
	}

	ipam, err := newIPAMStore(subnet)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IPAM: %w", err)
	}

	nm := &NetworkManager{
		cniBinDir:  cniBinDir,
		cniConfDir: cniConfDir,
		ipamStore:  ipam,
		networks:   make(map[string]*NetworkConfig),
	}

	// Load existing network configurations
	if err := nm.loadNetworkConfigs(); err != nil {
		klog.Warningf("Failed to load CNI network configs: %v", err)
	}

	return nm, nil
}

// SetupNetwork sets up networking for a sandbox.
func (nm *NetworkManager) SetupNetwork(ctx context.Context, sandboxID string, spec *sandboxv1alpha1.SandboxNetworkSpec) (*NetworkResult, error) {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	if spec == nil || spec.NetworkMode == sandboxv1alpha1.NetworkModeHost {
		return &NetworkResult{}, nil
	}

	if spec.NetworkMode == sandboxv1alpha1.NetworkModeNone {
		return &NetworkResult{}, nil
	}

	// Allocate IP address
	ip, err := nm.ipamStore.Allocate(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to allocate IP for sandbox %s: %w", sandboxID, err)
	}

	// Create network namespace
	netNSPath := fmt.Sprintf("/var/run/netns/%s", sandboxID)
	if err := createNetNS(netNSPath); err != nil {
		nm.ipamStore.Release(sandboxID)
		return nil, fmt.Errorf("failed to create netns for sandbox %s: %w", sandboxID, err)
	}

	// Determine which CNI plugin to use
	plugin := "bridge"
	if spec.NetworkMode == sandboxv1alpha1.NetworkModeCustom {
		plugin = "bridge" // Default, can be extended
	}

	// Build CNI configuration
	networkName := fmt.Sprintf("nexusbox-%s", plugin)
	netConf := &NetworkConfig{
		Name:   networkName,
		Type:   plugin,
		Subnet: nm.ipamStore.subnet.String(),
	}

	// Apply DNS config
	if spec.DNSConfig != nil {
		netConf.DNS = DNSConfig{
			Nameservers: spec.DNSConfig.Nameservers,
			Search:      spec.DNSConfig.Searches,
		}
	}

	// Apply bandwidth limits
	if spec.BandwidthLimit != "" {
		netConf.Capabilities = map[string]bool{"bandwidth": true}
	}

	// Execute CNI ADD
	result, err := nm.cniAdd(ctx, sandboxID, netNSPath, netConf)
	if err != nil {
		nm.ipamStore.Release(sandboxID)
		removeNetNS(netNSPath)
		return nil, fmt.Errorf("CNI ADD failed for sandbox %s: %w", sandboxID, err)
	}

	// Apply network rules (firewall/iptables)
	if len(spec.EgressRules) > 0 || len(spec.IngressRules) > 0 {
		if err := nm.applyNetworkRules(sandboxID, ip, spec); err != nil {
			klog.Warningf("Failed to apply network rules for sandbox %s: %v", sandboxID, err)
		}
	}

	klog.Infof("Network setup complete for sandbox %s: IP=%s, netns=%s", sandboxID, ip, netNSPath)
	return result, nil
}

// TeardownNetwork tears down networking for a sandbox.
func (nm *NetworkManager) TeardownNetwork(ctx context.Context, sandboxID string) error {
	nm.mu.Lock()
	defer nm.mu.Unlock()

	netNSPath := fmt.Sprintf("/var/run/netns/%s", sandboxID)

	// Execute CNI DEL
	if err := nm.cniDel(ctx, sandboxID, netNSPath); err != nil {
		klog.Warningf("CNI DEL failed for sandbox %s: %v", sandboxID, err)
	}

	// Release IP
	nm.ipamStore.Release(sandboxID)

	// Remove network namespace
	if err := removeNetNS(netNSPath); err != nil {
		klog.Warningf("Failed to remove netns for sandbox %s: %v", sandboxID, err)
	}

	// Clean up iptables rules
	cleanIPTablesRules(sandboxID)

	klog.Infof("Network teardown complete for sandbox %s", sandboxID)
	return nil
}

// cniAdd executes CNI ADD command.
func (nm *NetworkManager) cniAdd(ctx context.Context, sandboxID, netNSPath string, netConf *NetworkConfig) (*NetworkResult, error) {
	// Write CNI config to disk
	confPath := filepath.Join(nm.cniConfDir, fmt.Sprintf("10-%s.conflist", netConf.Name))
	if err := writeCNIConfig(confPath, netConf); err != nil {
		return nil, fmt.Errorf("failed to write CNI config: %w", err)
	}

	// Execute the CNI plugin binary
	pluginPath := filepath.Join(nm.cniBinDir, netConf.Type)
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("CNI plugin %s not found at %s", netConf.Type, pluginPath)
	}

	// Build CNI environment variables
	env := []string{
		fmt.Sprintf("CNI_COMMAND=ADD"),
		fmt.Sprintf("CNI_CONTAINERID=%s", sandboxID),
		fmt.Sprintf("CNI_NETNS=%s", netNSPath),
		fmt.Sprintf("CNI_IFNAME=eth0"),
		fmt.Sprintf("CNI_PATH=%s", nm.cniBinDir),
	}

	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Env = append(os.Environ(), env...)

	// Pass network config via stdin
	configData := buildCNIConfig(netConf, sandboxID, netNSPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	go func() {
		stdin.Write(configData)
		stdin.Close()
	}()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("CNI plugin failed: %w, output: %s", err, string(output))
	}

	// Parse CNI result
	ip := nm.ipamStore.GetIP(sandboxID)
	return &NetworkResult{
		InterfaceName: "eth0",
		IP:            ip,
		DNS:           netConf.DNS,
		NetNSPath:     netNSPath,
	}, nil
}

// cniDel executes CNI DEL command.
func (nm *NetworkManager) cniDel(ctx context.Context, sandboxID, netNSPath string) error {
	pluginPath := filepath.Join(nm.cniBinDir, "bridge")
	if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
		return nil // Plugin not available, skip
	}

	env := []string{
		"CNI_COMMAND=DEL",
		fmt.Sprintf("CNI_CONTAINERID=%s", sandboxID),
		fmt.Sprintf("CNI_NETNS=%s", netNSPath),
		"CNI_IFNAME=eth0",
		fmt.Sprintf("CNI_PATH=%s", nm.cniBinDir),
	}

	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Env = append(os.Environ(), env...)
	_, err := cmd.CombinedOutput()
	return err
}

// applyNetworkRules applies egress/ingress rules using iptables.
func (nm *NetworkManager) applyNetworkRules(sandboxID string, ip net.IP, spec *sandboxv1alpha1.SandboxNetworkSpec) error {
	chain := fmt.Sprintf("NEXUSBOX-%s", sandboxID)

	// Create custom chain
	runIPTables("-t", "filter", "-N", chain)
	runIPTables("-t", "filter", "-A", "FORWARD", "-j", chain)

	// Apply egress rules
	for _, rule := range spec.EgressRules {
		args := []string{"-t", "filter", "-A", chain, "-s", ip.String()}
		if rule.CIDR != "" {
			args = append(args, "-d", rule.CIDR)
		}
		if rule.Protocol != sandboxv1alpha1.ProtocolAll {
			args = append(args, "-p", strings.ToLower(string(rule.Protocol)))
		}
		for _, port := range rule.Ports {
			portArgs := make([]string, len(args))
			copy(portArgs, args)
			portArgs = append(portArgs, "--dport", fmt.Sprintf("%d:%d", port.Start, port.End))
			if rule.Action == sandboxv1alpha1.NetworkActionDeny {
				portArgs = append(portArgs, "-j", "DROP")
			} else {
				portArgs = append(portArgs, "-j", "ACCEPT")
			}
			runIPTables(portArgs...)
		}
		if len(rule.Ports) == 0 {
			if rule.Action == sandboxv1alpha1.NetworkActionDeny {
				args = append(args, "-j", "DROP")
			} else {
				args = append(args, "-j", "ACCEPT")
			}
			runIPTables(args...)
		}
	}

	// Apply ingress rules
	for _, rule := range spec.IngressRules {
		args := []string{"-t", "filter", "-A", chain, "-d", ip.String()}
		if rule.CIDR != "" {
			args = append(args, "-s", rule.CIDR)
		}
		if rule.Action == sandboxv1alpha1.NetworkActionDeny {
			args = append(args, "-j", "DROP")
		} else {
			args = append(args, "-j", "ACCEPT")
		}
		runIPTables(args...)
	}

	return nil
}

// loadNetworkConfigs loads existing CNI network configurations from disk.
func (nm *NetworkManager) loadNetworkConfigs() error {
	entries, err := os.ReadDir(nm.cniConfDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		klog.V(4).Infof("Found CNI config: %s", entry.Name())
	}
	return nil
}

// --- IPAM ---

func newIPAMStore(subnet string) (*IPAMStore, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %s: %w", subnet, err)
	}
	// Start allocating from the third IP (first is network, second is gateway)
	ip := make(net.IP, len(ipNet.IP))
	copy(ip, ipNet.IP)
	// Increment by 2 to skip network and gateway addresses
	incrementIP(ip)
	incrementIP(ip)

	return &IPAMStore{
		allocations: make(map[string]string),
		subnet:      ipNet,
		nextIP:      ip,
	}, nil
}

// Allocate allocates an IP address for a sandbox.
func (s *IPAMStore) Allocate(sandboxID string) (net.IP, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := make(net.IP, len(s.nextIP))
	copy(ip, s.nextIP)

	// Find next available IP
	for {
		ipStr := ip.String()
		if _, allocated := s.allocations[ipStr]; !allocated {
			s.allocations[ipStr] = sandboxID
			s.nextIP = make(net.IP, len(ip))
			copy(s.nextIP, ip)
			incrementIP(s.nextIP)
			return ip, nil
		}
		incrementIP(ip)
		if !s.subnet.Contains(ip) {
			return nil, fmt.Errorf("IPAM: no available IPs in subnet %s", s.subnet)
		}
	}
}

// Release releases an IP address allocation.
func (s *IPAMStore) Release(sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ip, id := range s.allocations {
		if id == sandboxID {
			delete(s.allocations, ip)
			return
		}
	}
}

// GetIP returns the allocated IP for a sandbox.
func (s *IPAMStore) GetIP(sandboxID string) net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ip, id := range s.allocations {
		if id == sandboxID {
			return net.ParseIP(ip)
		}
	}
	return nil
}

// --- Helper functions ---

func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func createNetNS(path string) error {
	// Create /var/run/netns directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Use ip netns add to create the namespace
	cmd := exec.Command("ip", "netns", "add", filepath.Base(path))
	return cmd.Run()
}

func removeNetNS(path string) error {
	cmd := exec.Command("ip", "netns", "delete", filepath.Base(path))
	return cmd.Run()
}

func runIPTables(args ...string) error {
	cmd := exec.Command("iptables", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.V(4).Infof("iptables %v: %s (err: %v)", args, string(output), err)
	}
	return err
}

func cleanIPTablesRules(sandboxID string) {
	chain := fmt.Sprintf("NEXUSBOX-%s", sandboxID)
	runIPTables("-t", "filter", "-D", "FORWARD", "-j", chain)
	runIPTables("-t", "filter", "-F", chain)
	runIPTables("-t", "filter", "-X", chain)
}

func writeCNIConfig(path string, conf *NetworkConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data := buildCNIConfigJSON(conf)
	return os.WriteFile(path, data, 0644)
}

func buildCNIConfig(conf *NetworkConfig, containerID, netNS string) []byte {
	return buildCNIConfigJSON(conf)
}

func buildCNIConfigJSON(conf *NetworkConfig) []byte {
	// Build a CNI configuration list
	config := fmt.Sprintf(`{
  "cniVersion": "0.4.0",
  "name": "%s",
  "plugins": [
    {
      "type": "%s",
      "bridge": "nexusbox0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "%s",
        "ranges": [[{"subnet": "%s"}]]
      }
    },
    {
      "type": "bandwidth",
      "capabilities": {"bandwidth": true}
    },
    {
      "type": "portmap",
      "capabilities": {"portMappings": true}
    }
  ]
}`, conf.Name, conf.Type, conf.Subnet, conf.Subnet)
	return []byte(config)
}
