/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package traffic implements bandwidth rate limiting using Linux Traffic Control (tc).
// This provides actual enforcement of network bandwidth limits on sandbox
// veth interfaces, complementing the CNI bandwidth plugin capability flag.
//
// The implementation uses the Hierarchical Token Bucket (HTB) qdisc with
// two classes (ingress/egress) and applies per-sandbox rate limits.
//
// Reference: tc(8), tc-htb(8).
package traffic

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// Shaper applies bandwidth limits to network interfaces using tc.
type Shaper struct {
	mu sync.Mutex

	// tcBin is the path to the tc binary.
	tcBin string
	// activeLimits tracks which interfaces have limits applied.
	activeLimits map[string]*Limit
}

// Limit describes a bandwidth limit for an interface.
type Limit struct {
	// Interface is the host-side veth name (e.g. "veth-abc123").
	Interface string
	// IngressBPS is the ingress (receive) rate in bits per second.
	// 0 means no limit.
	IngressBPS uint64
	// EgressBPS is the egress (transmit) rate in bits per second.
	// 0 means no limit.
	EgressBPS uint64
}

// NewShaper creates a new traffic shaper.
func NewShaper(tcBinPath string) *Shaper {
	if tcBinPath == "" {
		tcBinPath = "tc"
	}
	return &Shaper{
		tcBin:        tcBinPath,
		activeLimits: make(map[string]*Limit),
	}
}

// ApplyLimit applies ingress and egress rate limits to the given interface.
// If both IngressBPS and EgressBPS are 0, the limit is removed.
//
// Implementation:
//   - Egress: HTB qdisc with a class bound to the root, filter on the interface
//   - Ingress: ingress qdisc with a police action (ingress cannot use HTB)
//
// The interface must exist before calling this method.
func (s *Shaper) ApplyLimit(ctx context.Context, limit *Limit) error {
	if limit == nil || limit.Interface == "" {
		return fmt.Errorf("invalid limit: interface required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// If both limits are 0, remove any existing limit
	if limit.IngressBPS == 0 && limit.EgressBPS == 0 {
		return s.removeLimitLocked(ctx, limit.Interface)
	}

	klog.V(2).Infof("Applying traffic shaping on %s: ingress=%d bps, egress=%d bps",
		limit.Interface, limit.IngressBPS, limit.EgressBPS)

	// Apply egress limit using HTB
	if limit.EgressBPS > 0 {
		if err := s.applyEgressLimitLocked(ctx, limit.Interface, limit.EgressBPS); err != nil {
			return fmt.Errorf("egress limit on %s: %w", limit.Interface, err)
		}
	}

	// Apply ingress limit using ingress qdisc + police
	if limit.IngressBPS > 0 {
		if err := s.applyIngressLimitLocked(ctx, limit.Interface, limit.IngressBPS); err != nil {
			return fmt.Errorf("ingress limit on %s: %w", limit.Interface, err)
		}
	}

	s.activeLimits[limit.Interface] = limit
	klog.Infof("Traffic shaping applied on %s (ingress=%d bps, egress=%d bps)",
		limit.Interface, limit.IngressBPS, limit.EgressBPS)
	return nil
}

// RemoveLimit removes all traffic shaping rules from the given interface.
func (s *Shaper) RemoveLimit(ctx context.Context, iface string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLimitLocked(ctx, iface)
}

// applyEgressLimitLocked applies an egress rate limit using HTB qdisc.
// Layout:
//
//	root -> htb qdisc (1:) -> class 1:1 (rate=limit) -> filter match all
func (s *Shaper) applyEgressLimitLocked(ctx context.Context, iface string, bps uint64) error {
	rate := formatRate(bps)

	// Remove any existing qdisc first (ignore errors if none exists)
	s.runTc(ctx, "qdisc", "del", "dev", iface, "root")

	// Add HTB root qdisc
	if err := s.runTc(ctx, "qdisc", "add", "dev", iface, "root", "handle", "1:", "htb"); err != nil {
		return fmt.Errorf("add htb qdisc: %w", err)
	}

	// Add class with the rate limit
	if err := s.runTc(ctx, "class", "add", "dev", iface, "parent", "1:", "classid", "1:1",
		"htb", "rate", rate, "ceil", rate); err != nil {
		return fmt.Errorf("add htb class: %w", err)
	}

	// Add filter to direct all traffic to class 1:1
	if err := s.runTc(ctx, "filter", "add", "dev", iface, "parent", "1:",
		"protocol", "ip", "prio", "1", "u32", "match", "u32", "0", "0", "flowid", "1:1"); err != nil {
		return fmt.Errorf("add u32 filter: %w", err)
	}

	return nil
}

// applyIngressLimitLocked applies an ingress rate limit using ingress qdisc + police.
// Ingress traffic cannot be shaped with HTB; we use a police action instead.
func (s *Shaper) applyIngressLimitLocked(ctx context.Context, iface string, bps uint64) error {
	rate := formatRate(bps)
	burst := formatBurst(bps)

	// Remove any existing ingress qdisc first (ignore errors)
	s.runTc(ctx, "qdisc", "del", "dev", iface, "ingress")

	// Add ingress qdisc
	if err := s.runTc(ctx, "qdisc", "add", "dev", iface, "handle", "ffff:", "ingress"); err != nil {
		return fmt.Errorf("add ingress qdisc: %w", err)
	}

	// Add police filter on the ingress qdisc
	if err := s.runTc(ctx, "filter", "add", "dev", iface, "parent", "ffff:",
		"protocol", "ip", "prio", "1", "u32", "match", "u32", "0", "0",
		"police", "rate", rate, "burst", burst, "drop", "flowid", ":1"); err != nil {
		return fmt.Errorf("add police filter: %w", err)
	}

	return nil
}

// removeLimitLocked removes all qdiscs from the interface.
func (s *Shaper) removeLimitLocked(ctx context.Context, iface string) error {
	// Delete root qdisc (also removes all attached classes/filters)
	if err := s.runTc(ctx, "qdisc", "del", "dev", iface, "root"); err != nil {
		// Non-fatal: may not exist
		klog.V(4).Infof("qdisc del root on %s (may not exist): %v", iface, err)
	}
	// Delete ingress qdisc
	if err := s.runTc(ctx, "qdisc", "del", "dev", iface, "ingress"); err != nil {
		klog.V(4).Infof("qdisc del ingress on %s (may not exist): %v", iface, err)
	}
	delete(s.activeLimits, iface)
	klog.Infof("Traffic shaping removed from %s", iface)
	return nil
}

// runTc executes a tc command with the given arguments.
func (s *Shaper) runTc(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, s.tcBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tc %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// formatRate formats a bits-per-second value as a tc rate string.
// Example: 1000000 -> "1mbit", 50000000 -> "50mbit"
func formatRate(bps uint64) string {
	if bps >= 1_000_000_000 {
		return fmt.Sprintf("%dgbit", bps/1_000_000_000)
	}
	if bps >= 1_000_000 {
		return fmt.Sprintf("%dmbit", bps/1_000_000)
	}
	if bps >= 1_000 {
		return fmt.Sprintf("%dkbit", bps/1_000)
	}
	return fmt.Sprintf("%dbit", bps)
}

// formatBurst formats a burst size based on the rate.
// A common rule of thumb is burst = rate / 8 (1 second of data in bytes).
func formatBurst(bps uint64) string {
	bytesPerSec := bps / 8
	if bytesPerSec >= 1_000_000 {
		return fmt.Sprintf("%dmb", bytesPerSec/1_000_000)
	}
	if bytesPerSec >= 1_000 {
		return fmt.Sprintf("%dkb", bytesPerSec/1_000)
	}
	return fmt.Sprintf("%db", bytesPerSec)
}

// ActiveLimits returns a snapshot of all currently applied limits.
func (s *Shaper) ActiveLimits() map[string]*Limit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*Limit, len(s.activeLimits))
	for k, v := range s.activeLimits {
		cp := *v
		out[k] = &cp
	}
	return out
}
