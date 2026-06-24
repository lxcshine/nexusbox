/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateTestKeyPair generates an RSA key pair for testing.
func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	pubKeyPEM := string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	}))

	return privKey, pubKeyPEM
}

// generateTestToken creates a signed JWT token for testing.
func generateTestToken(privKey *rsa.PrivateKey, claims *Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(privKey)
}

// --- JWT Authenticator creation ---

func TestNewJWTAuthenticator_ValidKey(t *testing.T) {
	_, pubKeyPEM := generateTestKeyPair(t)

	auth, err := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
		Issuer:       "nexusbox-test",
		Audience:     "nexusbox-api",
	})
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}
	if auth.publicKey == nil {
		t.Error("public key should not be nil")
	}
}

func TestNewJWTAuthenticator_NoKey(t *testing.T) {
	auth, err := NewJWTAuthenticator(&JWTConfig{})
	if err != nil {
		t.Fatalf("failed to create authenticator without key: %v", err)
	}
	if auth.publicKey != nil {
		t.Error("public key should be nil when not configured")
	}
}

func TestNewJWTAuthenticator_InvalidKey(t *testing.T) {
	_, err := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: "not a valid PEM",
	})
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
}

// --- JWT Token validation ---

func TestAuthenticate_ValidToken(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, err := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
		Issuer:       "nexusbox-test",
		Audience:     "nexusbox-api",
	})
	if err != nil {
		t.Fatalf("failed to create authenticator: %v", err)
	}

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   "nexusbox-test",
			Audience: []string{"nexusbox-api"},
			Subject:  "user-123",
		},
		TenantID:   "tenant-1",
		TenantName: "TestTenant",
		Roles:      []string{"admin", "user"},
	}

	tokenString, err := generateTestToken(privKey, claims)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)

	authInfo, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("authentication failed: %v", err)
	}

	if authInfo.TenantID != "tenant-1" {
		t.Errorf("expected tenantId=tenant-1, got %s", authInfo.TenantID)
	}
	if authInfo.TenantName != "TestTenant" {
		t.Errorf("expected tenantName=TestTenant, got %s", authInfo.TenantName)
	}
	if authInfo.Subject != "user-123" {
		t.Errorf("expected subject=user-123, got %s", authInfo.Subject)
	}
	if !authInfo.IsAdmin() {
		t.Error("expected admin role")
	}
}

func TestAuthenticate_NoToken(t *testing.T) {
	_, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for missing token")
	}
}

func TestAuthenticate_InvalidToken(t *testing.T) {
	_, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestAuthenticate_WrongIssuer(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
		Issuer:       "nexusbox-test",
	})

	// Create token with wrong issuer
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "wrong-issuer",
		},
	}
	tokenString, _ := generateTestToken(privKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for wrong issuer")
	}
}

func TestAuthenticate_ExpiredToken(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		},
	}
	tokenString, _ := generateTestToken(privKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

// --- Token revocation ---

func TestRevokeToken(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-123",
		},
	}
	tokenString, _ := generateTestToken(privKey, claims)

	// Authenticate successfully first
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	_, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("initial authentication should succeed: %v", err)
	}

	// Revoke the token
	auth.RevokeToken(tokenString)

	// Try again - should fail
	req2 := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req2.Header.Set("Authorization", "Bearer "+tokenString)
	_, err = auth.Authenticate(req2)
	if err == nil {
		t.Error("expected error for revoked token")
	}
}

// --- AuthInfo role checks ---

func TestAuthInfo_HasRole(t *testing.T) {
	info := &AuthInfo{
		Roles: []string{"admin", "developer"},
	}

	if !info.HasRole("admin") {
		t.Error("expected admin role")
	}
	if !info.HasRole("developer") {
		t.Error("expected developer role")
	}
	if info.HasRole("viewer") {
		t.Error("should not have viewer role")
	}
}

func TestAuthInfo_IsAdmin(t *testing.T) {
	tests := []struct {
		roles []string
		want  bool
	}{
		{[]string{"admin"}, true},
		{[]string{"administrator"}, true},
		{[]string{"user"}, false},
		{[]string{"admin", "user"}, true},
		{[]string{}, false},
	}

	for _, tt := range tests {
		info := &AuthInfo{Roles: tt.roles}
		if info.IsAdmin() != tt.want {
			t.Errorf("IsAdmin(%v) = %v, want %v", tt.roles, info.IsAdmin(), tt.want)
		}
	}
}

func TestAuthInfo_CanAccessSandbox(t *testing.T) {
	tests := []struct {
		name      string
		info      *AuthInfo
		sandboxID string
		canAccess bool
	}{
		{
			name:      "admin can access any sandbox",
			info:      &AuthInfo{Roles: []string{"admin"}, SandboxID: "sb-1"},
			sandboxID: "sb-2",
			canAccess: true,
		},
		{
			name:      "user can access own sandbox",
			info:      &AuthInfo{Roles: []string{"user"}, SandboxID: "sb-1"},
			sandboxID: "sb-1",
			canAccess: true,
		},
		{
			name:      "user cannot access other sandbox",
			info:      &AuthInfo{Roles: []string{"user"}, SandboxID: "sb-1"},
			sandboxID: "sb-2",
			canAccess: false,
		},
		{
			name:      "user without sandbox ID",
			info:      &AuthInfo{Roles: []string{"user"}, SandboxID: ""},
			sandboxID: "sb-1",
			canAccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.info.CanAccessSandbox(tt.sandboxID) != tt.canAccess {
				t.Errorf("CanAccessSandbox() = %v, want %v", !tt.canAccess, tt.canAccess)
			}
		})
	}
}

// --- Middleware ---

func TestMiddleware_HealthBypass(t *testing.T) {
	_, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := auth.Middleware(next)

	// Health endpoint should bypass auth
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should be called for healthz")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_NoKeySkipsAuth(t *testing.T) {
	auth, _ := NewJWTAuthenticator(&JWTConfig{})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := auth.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should be called when no key configured")
	}
}

func TestMiddleware_ValidToken(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	var receivedAuthInfo *AuthInfo
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthInfo, _ = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := auth.Middleware(next)

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-123",
		},
		TenantID: "tenant-1",
		Roles:    []string{"user"},
	}
	tokenString, _ := generateTestToken(privKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if receivedAuthInfo == nil {
		t.Error("auth info should be in context")
	}
	if receivedAuthInfo.TenantID != "tenant-1" {
		t.Errorf("expected tenantId=tenant-1, got %s", receivedAuthInfo.TenantID)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	_, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := auth.Middleware(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("next handler should not be called for invalid token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Token from query parameter ---

func TestAuthenticate_TokenFromQuery(t *testing.T) {
	privKey, pubKeyPEM := generateTestKeyPair(t)

	auth, _ := NewJWTAuthenticator(&JWTConfig{
		PublicKeyPEM: pubKeyPEM,
	})

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-123",
		},
	}
	tokenString, _ := generateTestToken(privKey, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/test?token="+tokenString, nil)
	_, err := auth.Authenticate(req)
	if err != nil {
		t.Fatalf("authentication with query token failed: %v", err)
	}
}

// --- Context helpers ---

func TestContextHelpers(t *testing.T) {
	info := &AuthInfo{
		TenantID: "tenant-1",
		Roles:    []string{"admin"},
	}

	ctx := WithAuthInfo(context.Background(), info)
	retrieved, ok := FromContext(ctx)
	if !ok {
		t.Error("expected to find auth info in context")
	}
	if retrieved.TenantID != "tenant-1" {
		t.Errorf("expected tenantId=tenant-1, got %s", retrieved.TenantID)
	}

	// Empty context
	_, ok = FromContext(context.Background())
	if ok {
		t.Error("should not find auth info in empty context")
	}
}
