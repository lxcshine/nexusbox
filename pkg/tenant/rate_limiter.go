/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tenant

import (
	"sync"
	"time"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// RateLimiter provides per-tenant rate limiting functionality.
// It implements a token bucket algorithm for each tenant and operation type.
type RateLimiter struct {
	mu sync.RWMutex
	// buckets maps tenant+operation to token buckets.
	buckets map[string]*TokenBucket
	// configs maps tenant name to rate limit configuration.
	configs map[string]*RateLimitConfigInternal
}

// TokenBucket implements a token bucket rate limiter.
type TokenBucket struct {
	// tokens is the current number of available tokens.
	tokens float64
	// maxTokens is the maximum number of tokens (burst size).
	maxTokens float64
	// refillRate is the number of tokens added per second.
	refillRate float64
	// lastRefill is the last time tokens were refilled.
	lastRefill time.Time
}

// RateLimitConfigInternal holds the internal rate limit configuration for a tenant.
type RateLimitConfigInternal struct {
	// SandboxCreateBucket is the token bucket for sandbox creation.
	SandboxCreateBucket *TokenBucket
	// APICallBucket is the token bucket for API calls.
	APICallBucket *TokenBucket
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*TokenBucket),
		configs: make(map[string]*RateLimitConfigInternal),
	}
}

// RegisterTenant registers rate limit configuration for a tenant.
func (rl *RateLimiter) RegisterTenant(tenantName string, config *sandboxv1alpha1.RateLimitConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	internalConfig := &RateLimitConfigInternal{
		SandboxCreateBucket: &TokenBucket{
			tokens:     float64(config.SandboxCreateBurst),
			maxTokens:  float64(config.SandboxCreateBurst),
			refillRate: float64(config.SandboxCreateLimit) / 60.0, // per minute -> per second
			lastRefill: now,
		},
	}

	if config.APICallLimit > 0 {
		burst := config.APICallBurst
		if burst <= 0 {
			burst = config.APICallLimit
		}
		internalConfig.APICallBucket = &TokenBucket{
			tokens:     float64(burst),
			maxTokens:  float64(burst),
			refillRate: float64(config.APICallLimit) / 60.0,
			lastRefill: now,
		}
	}

	rl.configs[tenantName] = internalConfig

	klog.V(4).Infof("Registered rate limits for tenant %s: sandboxCreate=%d/min, apiCall=%d/min",
		tenantName, config.SandboxCreateLimit, config.APICallLimit)
}

// UnregisterTenant removes rate limit configuration for a tenant.
func (rl *RateLimiter) UnregisterTenant(tenantName string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	delete(rl.configs, tenantName)

	// Clean up related buckets
	prefix := tenantName + ":"
	for key := range rl.buckets {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(rl.buckets, key)
		}
	}
}

// Allow checks if a request is allowed for the given tenant and operation.
func (rl *RateLimiter) Allow(tenantName, operation string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	config, exists := rl.configs[tenantName]
	if !exists {
		// No rate limit configured, allow by default
		return true
	}

	var bucket *TokenBucket
	switch operation {
	case "CreateSandbox":
		if config.SandboxCreateBucket == nil {
			return true
		}
		bucket = config.SandboxCreateBucket
	case "APICall":
		if config.APICallBucket == nil {
			return true
		}
		bucket = config.APICallBucket
	default:
		// Use sandbox create bucket as default
		if config.SandboxCreateBucket == nil {
			return true
		}
		bucket = config.SandboxCreateBucket
	}

	return bucket.consume()
}

// Wait blocks until a request is allowed for the given tenant and operation.
func (rl *RateLimiter) Wait(tenantName, operation string) {
	for {
		if rl.Allow(tenantName, operation) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// consume takes one token from the bucket.
func (tb *TokenBucket) consume() bool {
	tb.refill()

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// refill adds tokens based on elapsed time.
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()

	if elapsed > 0 {
		tb.tokens += elapsed * tb.refillRate
		if tb.tokens > tb.maxTokens {
			tb.tokens = tb.maxTokens
		}
		tb.lastRefill = now
	}
}

// Cleanup removes stale bucket entries.
func (rl *RateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Remove buckets for tenants that no longer have configs
	for key := range rl.buckets {
		parts := key
		tenantFound := false
		for tenantName := range rl.configs {
			if len(parts) > len(tenantName) && parts[:len(tenantName)] == tenantName {
				tenantFound = true
				break
			}
		}
		if !tenantFound {
			delete(rl.buckets, key)
		}
	}
}

// GetRemainingTokens returns the remaining tokens for a tenant operation.
func (rl *RateLimiter) GetRemainingTokens(tenantName, operation string) float64 {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	config, exists := rl.configs[tenantName]
	if !exists {
		return -1 // No limit configured
	}

	var bucket *TokenBucket
	switch operation {
	case "CreateSandbox":
		bucket = config.SandboxCreateBucket
	case "APICall":
		bucket = config.APICallBucket
	default:
		bucket = config.SandboxCreateBucket
	}

	if bucket == nil {
		return -1
	}

	bucket.refill()
	return bucket.tokens
}
