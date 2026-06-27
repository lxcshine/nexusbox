package devtool

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/klog/v2"
)

// DevToolProxy is a WebSocket-compatible reverse proxy for dev tools.
// It handles:
//   - WebSocket upgrade (Jupyter and code-server both use WS)
//   - Path rewriting based on sandbox ID
//   - Header injection for auth forwarding
//
// URL format: /v1/devtools/proxy/{type}/{sandboxID}/{path...}
// Example:    /v1/devtools/proxy/jupyter/sb-abc123/lab
//
//	/v1/devtools/proxy/code-server/sb-abc123/
type DevToolProxy struct {
	manager *DevToolManager
}

// NewDevToolProxy creates a new proxy handler.
func NewDevToolProxy(m *DevToolManager) *DevToolProxy {
	return &DevToolProxy{manager: m}
}

// ServeHTTP proxies requests to the appropriate dev tool instance.
func (p *DevToolProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse path: /v1/devtools/proxy/{type}/{sandboxID}/{path...}
	pathParts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/v1/devtools/proxy/"), "/", 3)
	if len(pathParts) < 2 {
		http.Error(w, `{"error":"invalid proxy path, expected /v1/devtools/proxy/{type}/{sandboxID}/..."}`, http.StatusBadRequest)
		return
	}

	toolTypeStr := pathParts[0]
	sandboxID := pathParts[1]
	subPath := ""
	if len(pathParts) >= 3 {
		subPath = "/" + pathParts[2]
	}

	// Map type string to DevToolType
	var toolType DevToolType
	switch toolTypeStr {
	case "jupyter":
		toolType = DevToolJupyterLab
	case "code-server":
		toolType = DevToolCodeServer
	default:
		http.Error(w, fmt.Sprintf(`{"error":"unknown dev tool type: %s"}`, toolTypeStr), http.StatusBadRequest)
		return
	}

	// Find the instance for this sandbox
	inst, ok := p.manager.GetBySandboxAndType(sandboxID, toolType)
	if !ok {
		http.Error(w, fmt.Sprintf(`{"error":"no %s instance found for sandbox %s"}`, toolTypeStr, sandboxID), http.StatusNotFound)
		return
	}

	if inst.Status != DevToolStatusRunning && inst.Status != DevToolStatusPending {
		http.Error(w, fmt.Sprintf(`{"error":"dev tool %s is not running (status: %s)"}`, inst.ID, inst.Status), http.StatusServiceUnavailable)
		return
	}

	// Build the target URL
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", inst.Port),
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Customize the request
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Rewrite path: keep the sub-path after sandboxID
		req.URL.Path = subPath
		if req.URL.RawQuery == "" && r.URL.RawQuery != "" {
			req.URL.RawQuery = r.URL.RawQuery
		}
		// Set forwarded headers
		req.Header.Set("X-Forwarded-For", r.RemoteAddr)
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-Sandbox-ID", sandboxID)
	}

	// Rewrite redirect Location headers so the client stays on the proxy path
	// e.g. Jupyter redirects "/" -> "/lab?" which needs to become
	// "/v1/devtools/proxy/jupyter/{sandboxID}/lab?"
	proxyPrefix := fmt.Sprintf("/v1/devtools/proxy/%s/%s/", toolTypeStr, sandboxID)
	proxy.ModifyResponse = func(resp *http.Response) error {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return nil
		}
		// Parse the location URL
		locURL, err := url.Parse(loc)
		if err != nil {
			return nil
		}
		// Only rewrite relative paths (no scheme/host)
		if locURL.IsAbs() {
			return nil
		}
		// Rewrite: prepend the proxy prefix, avoiding double slashes
		newPath := locURL.Path
		if strings.HasPrefix(newPath, "/") {
			newPath = strings.TrimPrefix(newPath, "/")
		}
		// Rebuild the Location header
		locURL.Path = proxyPrefix + newPath
		resp.Header.Set("Location", locURL.String())
		return nil
	}

	// WebSocket support: upgrade the connection
	if isWebSocketRequest(r) {
		p.handleWebSocket(w, r, target, subPath, sandboxID)
		return
	}

	klog.V(4).Infof("DevTool proxy: %s %s -> 127.0.0.1:%d%s", r.Method, r.URL.Path, inst.Port, subPath)
	proxy.ServeHTTP(w, r)
}

// isWebSocketRequest checks if the request is a WebSocket upgrade.
func isWebSocketRequest(r *http.Request) bool {
	return strings.ToLower(r.Header.Get("Upgrade")) == "websocket" &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// handleWebSocket proxies a WebSocket connection by tunneling at the HTTP level.
// Since httputil.ReverseProxy does not natively support WebSocket, we use a
// manual approach: hijack the client connection and pipe to the backend.
func (p *DevToolProxy) handleWebSocket(w http.ResponseWriter, r *http.Request, target *url.URL, subPath string, sandboxID string) {
	// Use the standard reverse proxy's transport which handles WS
	// by setting the Upgrade header properly
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = subPath
		req.Host = target.Host
		// Preserve WebSocket headers
		req.Header.Set("X-Forwarded-For", r.RemoteAddr)
		req.Header.Set("X-Sandbox-ID", sandboxID)
	}
	// The default transport supports WebSocket via "Upgrade" handling
	proxy.ServeHTTP(w, r)
}
