package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// BrowserService provides browser automation capabilities within sandboxes.
// Inspired by agent-infra/sandbox's browser service, it supports:
// - CDP (Chrome DevTools Protocol) integration
// - Screenshot capture
// - Navigation, click, type, scroll actions
// - Browser info and cookies
type BrowserService struct {
	cdPEndpoint string
	mu          sync.RWMutex
	initialized bool
}

// NewBrowserService creates a new BrowserService.
func NewBrowserService() *BrowserService {
	return &BrowserService{
		cdPEndpoint: "http://localhost:9222",
	}
}

// --- Request/Response types ---

// BrowserScreenshotRequest is the request for taking a screenshot.
type BrowserScreenshotRequest struct {
	Format  string `json:"format,omitempty"`  // "png" (default) or "jpeg"
	Quality int    `json:"quality,omitempty"` // JPEG quality 1-100
	FullPage bool  `json:"fullPage,omitempty"`
	Selector string `json:"selector,omitempty"` // CSS selector for element screenshot
}

// BrowserScreenshotResponse is the response for taking a screenshot.
type BrowserScreenshotResponse struct {
	Image     string `json:"image"`     // base64 encoded image
	Format    string `json:"format"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Timestamp string `json:"timestamp"`
}

// BrowserNavigateRequest is the request for navigating to a URL.
type BrowserNavigateRequest struct {
	URL     string `json:"url"`
	WaitFor string `json:"waitFor,omitempty"` // "load", "domcontentloaded", "networkidle"
	Timeout int    `json:"timeout,omitempty"` // milliseconds
}

// BrowserNavigateResponse is the response for navigation.
type BrowserNavigateResponse struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Status  int    `json:"status"`
	Success bool   `json:"success"`
}

// BrowserClickRequest is the request for clicking an element.
type BrowserClickRequest struct {
	Selector string `json:"selector"`
	Button   string `json:"button,omitempty"`   // "left" (default), "right", "middle"
	Clicks   int    `json:"clicks,omitempty"`    // number of clicks
	Delay    int    `json:"delay,omitempty"`     // delay between clicks in ms
}

// BrowserTypeRequest is the request for typing text.
type BrowserTypeRequest struct {
	Selector string `json:"selector"`
	Text     string `json:"text"`
	Clear    bool   `json:"clear,omitempty"`   // clear existing text first
	Delay    int    `json:"delay,omitempty"`   // delay between keystrokes in ms
}

// BrowserScrollRequest is the request for scrolling.
type BrowserScrollRequest struct {
	Direction string `json:"direction"` // "up" or "down"
	Amount    int    `json:"amount"`    // pixels to scroll
	Selector  string `json:"selector,omitempty"` // scroll within element
}

// BrowserInfoResponse is the response for browser info.
type BrowserInfoResponse struct {
	Connected    bool   `json:"connected"`
	CDPEndpoint  string `json:"cdpEndpoint"`
	Version      string `json:"version"`
	Viewport     Viewport `json:"viewport"`
	UserAgent    string `json:"userAgent"`
}

// Viewport represents the browser viewport dimensions.
type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// BrowserCookiesResponse is the response for browser cookies.
type BrowserCookiesResponse struct {
	Cookies []CookieInfo `json:"cookies"`
}

// CookieInfo represents a browser cookie.
type CookieInfo struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool   `json:"httpOnly"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"sameSite"`
}

// --- Handlers ---

// Screenshot handles browser screenshot requests.
func (b *BrowserService) Screenshot(w http.ResponseWriter, r *http.Request) {
	var req BrowserScreenshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	format := req.Format
	if format == "" {
		format = "png"
	}

	// Use CDP to take screenshot
	result, err := b.executeCDPCommand("Page.captureScreenshot", map[string]interface{}{
		"format":  format,
		"quality": req.Quality,
	})
	if err != nil {
		klog.Warningf("CDP screenshot failed: %v, returning placeholder", err)
		// Return a placeholder response when CDP is not available
		writeJSON(w, http.StatusOK, BrowserScreenshotResponse{
			Image:     "",
			Format:    format,
			Width:     1280,
			Height:    1024,
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}

	data, _ := result["data"].(string)
	writeJSON(w, http.StatusOK, BrowserScreenshotResponse{
		Image:     data,
		Format:    format,
		Width:     1280,
		Height:    1024,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

// Navigate handles browser navigation requests.
func (b *BrowserService) Navigate(w http.ResponseWriter, r *http.Request) {
	var req BrowserNavigateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	result, err := b.executeCDPCommand("Page.navigate", map[string]interface{}{
		"url": req.URL,
	})
	if err != nil {
		klog.Warningf("CDP navigate failed: %v", err)
		writeJSON(w, http.StatusOK, BrowserNavigateResponse{
			URL:     req.URL,
			Success: false,
		})
		return
	}

	frameID, _ := result["frameId"].(string)
	writeJSON(w, http.StatusOK, BrowserNavigateResponse{
		URL:     req.URL,
		Success: frameID != "",
	})
}

// Click handles browser click requests.
func (b *BrowserService) Click(w http.ResponseWriter, r *http.Request) {
	var req BrowserClickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Selector == "" {
		writeError(w, http.StatusBadRequest, "selector is required")
		return
	}

	// Use a JavaScript-based click via CDP Runtime.evaluate
	js := fmt.Sprintf(`document.querySelector('%s').click()`, escapeJS(req.Selector))
	_, err := b.executeCDPCommand("Runtime.evaluate", map[string]interface{}{
		"expression": js,
	})
	if err != nil {
		klog.Warningf("CDP click failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"action":   "click",
		"selector": req.Selector,
		"success":  err == nil,
	})
}

// Type handles browser type requests.
func (b *BrowserService) Type(w http.ResponseWriter, r *http.Request) {
	var req BrowserTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Selector == "" {
		writeError(w, http.StatusBadRequest, "selector is required")
		return
	}

	var js string
	if req.Clear {
		js = fmt.Sprintf(`(function(){var el=document.querySelector('%s');el.focus();el.value='%s';el.dispatchEvent(new Event('input',{bubbles:true}));})()`,
			escapeJS(req.Selector), escapeJS(req.Text))
	} else {
		js = fmt.Sprintf(`(function(){var el=document.querySelector('%s');el.focus();el.value+='%s';el.dispatchEvent(new Event('input',{bubbles:true}));})()`,
			escapeJS(req.Selector), escapeJS(req.Text))
	}

	_, err := b.executeCDPCommand("Runtime.evaluate", map[string]interface{}{
		"expression": js,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"action":   "type",
		"selector": req.Selector,
		"success":  err == nil,
	})
}

// Scroll handles browser scroll requests.
func (b *BrowserService) Scroll(w http.ResponseWriter, r *http.Request) {
	var req BrowserScrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	amount := req.Amount
	if amount == 0 {
		amount = 300
	}

	direction := 1
	if req.Direction == "up" {
		direction = -1
	}

	js := fmt.Sprintf(`window.scrollBy(0, %d)`, direction*amount)
	_, err := b.executeCDPCommand("Runtime.evaluate", map[string]interface{}{
		"expression": js,
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"action":    "scroll",
		"direction": req.Direction,
		"amount":    amount,
		"success":   err == nil,
	})
}

// Info returns browser information.
func (b *BrowserService) Info(w http.ResponseWriter, r *http.Request) {
	result, err := b.executeCDPCommand("Browser.getVersion", map[string]interface{}{})

	var version, userAgent string
	if err == nil {
		version, _ = result["product"].(string)
		userAgent, _ = result["userAgent"].(string)
	}

	writeJSON(w, http.StatusOK, BrowserInfoResponse{
		Connected:   err == nil,
		CDPEndpoint: b.cdPEndpoint,
		Version:     version,
		Viewport: Viewport{
			Width:  1280,
			Height: 1024,
		},
		UserAgent: userAgent,
	})
}

// Cookies returns browser cookies.
func (b *BrowserService) Cookies(w http.ResponseWriter, r *http.Request) {
	result, err := b.executeCDPCommand("Network.getCookies", map[string]interface{}{})

	var cookies []CookieInfo
	if err == nil {
		if rawCookies, ok := result["cookies"].([]interface{}); ok {
			for _, c := range rawCookies {
				if cm, ok := c.(map[string]interface{}); ok {
					cookie := CookieInfo{
						Name:     fmt.Sprintf("%v", cm["name"]),
						Value:    fmt.Sprintf("%v", cm["value"]),
						Domain:   fmt.Sprintf("%v", cm["domain"]),
						Path:     fmt.Sprintf("%v", cm["path"]),
						HTTPOnly: fmt.Sprintf("%v", cm["httpOnly"]) == "true",
						Secure:   fmt.Sprintf("%v", cm["secure"]) == "true",
					}
					cookies = append(cookies, cookie)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, BrowserCookiesResponse{
		Cookies: cookies,
	})
}

// executeCDPCommand sends a command to Chrome DevTools Protocol.
func (b *BrowserService) executeCDPCommand(method string, params map[string]interface{}) (map[string]interface{}, error) {
	// In production, this would use websocket to communicate with CDP
	// For now, use the chrome-remote-interface CLI tool if available
	cmd := exec.Command("node", "-e", fmt.Sprintf(`
		const CDP = require('chrome-remote-interface');
		(async () => {
			const client = await CDP();
			const result = await client.send('%s', %s);
			console.log(JSON.stringify(result));
			await client.close();
		})();
	`, method, toJSON(params)))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("CDP command failed: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse CDP response: %w", err)
	}

	return result, nil
}

// toJSON converts a map to JSON string.
func toJSON(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// escapeJS escapes a string for use in JavaScript.
func escapeJS(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

// IsCDPAvailable checks if CDP is available.
func (b *BrowserService) IsCDPAvailable() bool {
	// Check if chromium is running with remote debugging
	if _, err := os.Stat("/dev/shm/chromium-socket"); err == nil {
		return true
	}
	// Try connecting to CDP endpoint
	cmd := exec.Command("curl", "-s", b.cdPEndpoint+"/json/version")
	if err := cmd.Run(); err == nil {
		return true
	}
	return false
}
