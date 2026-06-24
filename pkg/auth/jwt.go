/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"k8s.io/klog/v2"
)

// JWTAuthenticator provides JWT-based authentication and multi-tenant isolation.
// Inspired by agent-infra/sandbox's auth backend, it supports:
// - JWT token validation with RSA public key
// - Tenant extraction from tokens
// - Role-based access control
// - Token refresh
type JWTAuthenticator struct {
	publicKey *rsa.PublicKey
	issuer    string
	audience  string

	revokedTokens map[string]time.Time
	mu            sync.RWMutex
}

// JWTConfig holds configuration for JWT authentication.
type JWTConfig struct {
	PublicKeyPEM string
	Issuer       string
	Audience     string
}

// Claims represents the JWT claims with tenant and role information.
type Claims struct {
	jwt.RegisteredClaims
	TenantID   string   `json:"tid,omitempty"`
	TenantName string   `json:"tn,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	SandboxID  string   `json:"sid,omitempty"`
}

// AuthInfo contains the authenticated user/tenant information.
type AuthInfo struct {
	TenantID   string   `json:"tenantId"`
	TenantName string   `json:"tenantName"`
	Roles      []string `json:"roles"`
	Scope      string   `json:"scope"`
	SandboxID  string   `json:"sandboxId"`
	Subject    string   `json:"subject"`
}

// NewJWTAuthenticator creates a new JWTAuthenticator.
func NewJWTAuthenticator(config *JWTConfig) (*JWTAuthenticator, error) {
	a := &JWTAuthenticator{
		issuer:        config.Issuer,
		audience:      config.Audience,
		revokedTokens: make(map[string]time.Time),
	}

	if config.PublicKeyPEM != "" {
		pubKey, err := parseRSAPublicKey(config.PublicKeyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}
		a.publicKey = pubKey
	}

	return a, nil
}

// Middleware returns an HTTP middleware that validates JWT tokens.
func (a *JWTAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health endpoints
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}

		// If no public key configured, skip auth
		if a.publicKey == nil {
			next.ServeHTTP(w, r)
			return
		}

		authInfo, err := a.Authenticate(r)
		if err != nil {
			klog.V(2).Infof("Auth failed: %v", err)
			http.Error(w, `{"error":"unauthorized","message":"`+err.Error()+`"}`, http.StatusUnauthorized)
			return
		}

		// Add auth info to context
		ctx := WithAuthInfo(r.Context(), authInfo)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate validates the JWT token from the request and returns AuthInfo.
func (a *JWTAuthenticator) Authenticate(r *http.Request) (*AuthInfo, error) {
	tokenString, err := extractToken(r)
	if err != nil {
		return nil, err
	}

	// Check if token is revoked
	a.mu.RLock()
	if revTime, ok := a.revokedTokens[tokenString]; ok {
		a.mu.RUnlock()
		return nil, fmt.Errorf("token revoked at %v", revTime)
	}
	a.mu.RUnlock()

	claims := &Claims{}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
	}
	if a.issuer != "" {
		opts = append(opts, jwt.WithIssuer(a.issuer))
	}
	if a.audience != "" {
		opts = append(opts, jwt.WithAudience(a.audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.publicKey, nil
	}, opts...)

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	if !token.Valid {
		return nil, errors.New("invalid token")
	}

	return &AuthInfo{
		TenantID:   claims.TenantID,
		TenantName: claims.TenantName,
		Roles:      claims.Roles,
		Scope:      claims.Scope,
		SandboxID:  claims.SandboxID,
		Subject:    claims.Subject,
	}, nil
}

// RevokeToken revokes a JWT token.
func (a *JWTAuthenticator) RevokeToken(tokenString string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revokedTokens[tokenString] = time.Now()

	// Clean up old revoked tokens
	cutoff := time.Now().Add(-24 * time.Hour)
	for tok, t := range a.revokedTokens {
		if t.Before(cutoff) {
			delete(a.revokedTokens, tok)
		}
	}
}

// HasRole checks if the auth info has a specific role.
func (a *AuthInfo) HasRole(role string) bool {
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsAdmin checks if the auth info has admin role.
func (a *AuthInfo) IsAdmin() bool {
	return a.HasRole("admin") || a.HasRole("administrator")
}

// CanAccessSandbox checks if the auth info can access a specific sandbox.
func (a *AuthInfo) CanAccessSandbox(sandboxID string) bool {
	if a.IsAdmin() {
		return true
	}
	if a.SandboxID != "" && a.SandboxID == sandboxID {
		return true
	}
	return false
}

// --- Context helpers ---

type authInfoKey struct{}

// WithAuthInfo adds AuthInfo to the context.
func WithAuthInfo(ctx context.Context, info *AuthInfo) context.Context {
	return context.WithValue(ctx, authInfoKey{}, info)
}

// FromContext extracts AuthInfo from the context.
func FromContext(ctx context.Context) (*AuthInfo, bool) {
	info, ok := ctx.Value(authInfoKey{}).(*AuthInfo)
	return info, ok
}

// --- Utility ---

// extractToken extracts the JWT token from the request.
func extractToken(r *http.Request) (string, error) {
	// Check Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1]), nil
		}
	}

	// Check query parameter
	if token := r.URL.Query().Get("token"); token != "" {
		return token, nil
	}

	return "", errors.New("no token provided")
}

// parseRSAPublicKey parses a PEM-encoded RSA public key.
func parseRSAPublicKey(pemData string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	// Try PKIX format first
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("not an RSA public key")
		}
		return rsaPub, nil
	}

	// Try PKCS1 format
	rsaPub, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA public key: %w", err)
	}

	return rsaPub, nil
}
