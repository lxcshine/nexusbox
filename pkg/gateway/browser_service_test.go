/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestBrowserService() *BrowserService {
	return NewBrowserService()
}

// --- Browser Info ---

func TestBrowserInfo(t *testing.T) {
	svc := newTestBrowserService()

	req := httptest.NewRequest(http.MethodGet, "/v1/browser/info", nil)
	w := httptest.NewRecorder()
	svc.Info(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("info: expected 200, got %d", w.Code)
	}

	var resp BrowserInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.CDPEndpoint != "http://localhost:9222" {
		t.Errorf("expected CDP endpoint http://localhost:9222, got %s", resp.CDPEndpoint)
	}
	if resp.Viewport.Width != 1280 {
		t.Errorf("expected viewport width 1280, got %d", resp.Viewport.Width)
	}
	if resp.Viewport.Height != 1024 {
		t.Errorf("expected viewport height 1024, got %d", resp.Viewport.Height)
	}
}

// --- Browser Screenshot ---

func TestBrowserScreenshot_DefaultFormat(t *testing.T) {
	svc := newTestBrowserService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/screenshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Screenshot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("screenshot: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp BrowserScreenshotResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Format != "png" {
		t.Errorf("expected format=png, got %s", resp.Format)
	}
}

func TestBrowserScreenshot_JPEG(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"format":"jpeg","quality":80}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/screenshot", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Screenshot(w, req)

	var resp BrowserScreenshotResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Format != "jpeg" {
		t.Errorf("expected format=jpeg, got %s", resp.Format)
	}
}

// --- Browser Navigate ---

func TestBrowserNavigate_MissingURL(t *testing.T) {
	svc := newTestBrowserService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/navigate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Navigate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBrowserNavigate_WithURL(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"url":"https://example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/navigate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Navigate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("navigate: expected 200, got %d", w.Code)
	}

	var resp BrowserNavigateResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.URL != "https://example.com" {
		t.Errorf("expected url=https://example.com, got %s", resp.URL)
	}
}

// --- Browser Click ---

func TestBrowserClick_MissingSelector(t *testing.T) {
	svc := newTestBrowserService()

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/click", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Click(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBrowserClick_WithSelector(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"selector":"#submit-btn"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/click", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Click(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("click: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["selector"] != "#submit-btn" {
		t.Errorf("expected selector=#submit-btn, got %v", resp["selector"])
	}
}

// --- Browser Type ---

func TestBrowserType_MissingSelector(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/type", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Type(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBrowserType_WithSelectorAndText(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"selector":"#search","text":"nexusbox"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/type", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Type(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("type: expected 200, got %d", w.Code)
	}
}

// --- Browser Scroll ---

func TestBrowserScroll_DefaultAmount(t *testing.T) {
	svc := newTestBrowserService()

	body := `{"direction":"down"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/browser/scroll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Scroll(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("scroll: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["direction"] != "down" {
		t.Errorf("expected direction=down, got %v", resp["direction"])
	}
}

// --- Browser Cookies ---

func TestBrowserCookies(t *testing.T) {
	svc := newTestBrowserService()

	req := httptest.NewRequest(http.MethodGet, "/v1/browser/cookies", nil)
	w := httptest.NewRecorder()
	svc.Cookies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cookies: expected 200, got %d", w.Code)
	}

	var resp BrowserCookiesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	// Cookies may be empty if no CDP connection, but should not error
}

// --- escapeJS ---

func TestEscapeJS(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"it's", "it\\'s"},
		{"back\\slash", "back\\\\slash"},
		{"line\nbreak", "line\\nbreak"},
		{"carriage\rreturn", "carriage\\rreturn"},
	}

	for _, tt := range tests {
		result := escapeJS(tt.input)
		if result != tt.expected {
			t.Errorf("escapeJS(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// --- toJSON ---

func TestToJSON(t *testing.T) {
	result := toJSON(map[string]string{"key": "value"})
	expected := `{"key":"value"}`
	if result != expected {
		t.Errorf("toJSON() = %s, want %s", result, expected)
	}
}
