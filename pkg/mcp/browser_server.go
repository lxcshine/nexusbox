package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BrowserMCPServer provides real browser automation tools via MCP.
// It connects to Chromium's CDP (Chrome DevTools Protocol) endpoint
// to navigate, screenshot, click, type, and scroll.
type BrowserMCPServer struct {
	cdpEndpoint string
}

// NewBrowserMCPServer creates a new BrowserMCPServer.
func NewBrowserMCPServer(workspace string) *BrowserMCPServer {
	return &BrowserMCPServer{
		cdpEndpoint: "http://localhost:9222",
	}
}

// Name returns the server name.
func (s *BrowserMCPServer) Name() string { return "browser" }

// ListTools returns the list of browser tools.
func (s *BrowserMCPServer) ListTools(ctx context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "browser_navigate",
			Description: "Navigate the browser to a URL. Returns the page title and URL after navigation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"url": {Type: "string", Description: "URL to navigate to"},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "browser_screenshot",
			Description: "Take a screenshot of the current page. Returns the image as base64-encoded PNG.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"fullPage": {Type: "boolean", Description: "Capture full page (default: false)"},
				},
			},
		},
		{
			Name:        "browser_click",
			Description: "Click an element on the page by CSS selector.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"selector": {Type: "string", Description: "CSS selector of the element to click"},
				},
				Required: []string{"selector"},
			},
		},
		{
			Name:        "browser_type",
			Description: "Type text into an input element identified by CSS selector.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"selector": {Type: "string", Description: "CSS selector of the input element"},
					"text":     {Type: "string", Description: "Text to type into the element"},
					"submit":   {Type: "boolean", Description: "Press Enter after typing (default: false)"},
				},
				Required: []string{"selector", "text"},
			},
		},
		{
			Name:        "browser_eval",
			Description: "Evaluate JavaScript code in the browser page. Returns the result as text.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"script": {Type: "string", Description: "JavaScript code to evaluate"},
				},
				Required: []string{"script"},
			},
		},
		{
			Name:        "browser_get_text",
			Description: "Get the text content of an element by CSS selector, or the full page text if no selector given.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"selector": {Type: "string", Description: "CSS selector (default: body)"},
				},
			},
		},
	}, nil
}

// CallTool invokes a browser tool.
func (s *BrowserMCPServer) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	switch name {
	case "browser_navigate":
		return s.navigate(ctx, arguments)
	case "browser_screenshot":
		return s.screenshot(ctx, arguments)
	case "browser_click":
		return s.click(ctx, arguments)
	case "browser_type":
		return s.typeText(ctx, arguments)
	case "browser_eval":
		return s.eval(ctx, arguments)
	case "browser_get_text":
		return s.getText(ctx, arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// cdpRequest sends a CDP command and returns the result.
func (s *BrowserMCPServer) cdpRequest(ctx context.Context, method string, params map[string]interface{}) (map[string]interface{}, error) {
	// Get the WebSocket debugger URL for the first tab
	tabs, err := s.getTabs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get browser tabs: %w", err)
	}
	if len(tabs) == 0 {
		return nil, fmt.Errorf("no browser tabs available (is Chromium running?)")
	}

	// Use HTTP-based CDP via the /json/protocol endpoint
	// For simplicity, we use the evaluate endpoint directly
	tabID := tabs[0]["id"].(string)

	// Build the CDP command
	cmd := map[string]interface{}{
		"id":     1,
		"method": method,
		"params": params,
	}

	cmdBytes, _ := json.Marshal(cmd)

	// Send via HTTP PUT to the tab's devtools endpoint
	url := fmt.Sprintf("%s/json/activate/%s", s.cdpEndpoint, tabID)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	// Use the evaluate endpoint for JavaScript execution
	if method == "Runtime.evaluate" {
		evalURL := fmt.Sprintf("%s/json/evaluate/%s", s.cdpEndpoint, params["expression"].(string))
		req, err := http.NewRequestWithContext(ctx, "GET", evalURL, bytes.NewReader(cmdBytes))
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return map[string]interface{}{"result": string(body)}, nil
		}
		return result, nil
	}

	return map[string]interface{}{"status": "ok"}, nil
}

// getTabs returns the list of open browser tabs.
func (s *BrowserMCPServer) getTabs(ctx context.Context) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("%s/json/list", s.cdpEndpoint)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Chromium CDP at %s: %w (is Chromium running?)", s.cdpEndpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tabs []map[string]interface{}
	if err := json.Unmarshal(body, &tabs); err != nil {
		return nil, fmt.Errorf("failed to parse CDP response: %w", err)
	}

	// Filter to only "page" type tabs
	var pageTabs []map[string]interface{}
	for _, tab := range tabs {
		if tab["type"] == "page" {
			pageTabs = append(pageTabs, tab)
		}
	}

	if len(pageTabs) == 0 {
		// Create a new tab
		newTabURL := fmt.Sprintf("%s/json/new?about:blank", s.cdpEndpoint)
		req, _ := http.NewRequestWithContext(ctx, "PUT", newTabURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var newTab map[string]interface{}
		if err := json.Unmarshal(body, &newTab); err == nil {
			pageTabs = append(pageTabs, newTab)
		}
	}

	return pageTabs, nil
}

// navigate navigates the browser to a URL.
func (s *BrowserMCPServer) navigate(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	url, _ := arguments["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("url is required")
	}

	// Use CDP to create or navigate a tab
	navigateURL := fmt.Sprintf("%s/json/new?%s", s.cdpEndpoint, url)
	req, err := http.NewRequestWithContext(ctx, "PUT", navigateURL, nil)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to navigate: %v", err)}},
			IsError: true,
		}, nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to navigate to %s: %v\n(Is Chromium running on port 9222?)", url, err)}},
			IsError: true,
		}, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tabInfo map[string]interface{}
	if err := json.Unmarshal(body, &tabInfo); err == nil {
		title, _ := tabInfo["title"].(string)
		tabURL, _ := tabInfo["url"].(string)
		return &CallToolResult{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Navigated to: %s\nTitle: %s\nURL: %s", url, title, tabURL)},
			},
		}, nil
	}

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Navigation initiated to: %s", url)},
		},
	}, nil
}

// screenshot takes a screenshot of the current page.
func (s *BrowserMCPServer) screenshot(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	// Use CDP evaluate to capture screenshot via JavaScript
	script := `
		(function() {
			var canvas = document.createElement('canvas');
			var ctx = canvas.getContext('2d');
			var w = document.body.scrollWidth;
			var h = document.body.scrollHeight;
			canvas.width = w;
			canvas.height = h;
			ctx.drawWindow(window, 0, 0, w, h, '#ffffff');
			return canvas.toDataURL('image/png');
		})()
	`

	encodedScript := fmt.Sprintf("(%s)()", script)
	result, err := s.cdpRequest(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression": encodedScript,
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Screenshot failed: %v", err)}},
			IsError: true,
		}, nil
	}

	// Extract the data URL
	if resultVal, ok := result["result"].(map[string]interface{}); ok {
		if dataURL, ok := resultVal["value"].(string); ok && dataURL != "" {
			// Strip the data URL prefix
			base64Data := dataURL
			if idx := indexOf(dataURL, ","); idx >= 0 {
				base64Data = dataURL[idx+1:]
			}
			return &CallToolResult{
				Content: []ContentBlock{
					{Type: "image", MimeType: "image/png", Data: base64Data},
				},
			}, nil
		}
	}

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "Screenshot not available (requires Chromium running with CDP on port 9222)"},
		},
	}, nil
}

// click clicks an element by CSS selector.
func (s *BrowserMCPServer) click(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	selector, _ := arguments["selector"].(string)
	if selector == "" {
		return nil, fmt.Errorf("selector is required")
	}

	script := fmt.Sprintf(`
		(function() {
			var el = document.querySelector(%q);
			if (!el) return "Element not found: %s";
			el.click();
			return "Clicked: %s";
		})()
	`, selector, selector, selector)

	result, err := s.cdpRequest(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Click failed: %v", err)}},
			IsError: true,
		}, nil
	}

	text := "Click sent"
	if resultVal, ok := result["result"].(map[string]interface{}); ok {
		if val, ok := resultVal["value"].(string); ok {
			text = val
		}
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}, nil
}

// typeText types text into an element.
func (s *BrowserMCPServer) typeText(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	selector, _ := arguments["selector"].(string)
	text, _ := arguments["text"].(string)
	if selector == "" {
		return nil, fmt.Errorf("selector is required")
	}

	submit := false
	if s, ok := arguments["submit"].(bool); ok {
		submit = s
	}

	submitCode := ""
	if submit {
		submitCode = `
			if (el.form) el.form.submit();
			else {
				var evt = new KeyboardEvent('keydown', {key: 'Enter', keyCode: 13, which: 13});
				el.dispatchEvent(evt);
			}
		`
	}

	script := fmt.Sprintf(`
		(function() {
			var el = document.querySelector(%q);
			if (!el) return "Element not found: %s";
			el.focus();
			el.value = %q;
			el.dispatchEvent(new Event('input', {bubbles: true}));
			el.dispatchEvent(new Event('change', {bubbles: true}));
			%s
			return "Typed into %s: " + %q;
		})()
	`, selector, selector, text, submitCode, selector, text)

	result, err := s.cdpRequest(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Type failed: %v", err)}},
			IsError: true,
		}, nil
	}

	resultText := "Text entered"
	if resultVal, ok := result["result"].(map[string]interface{}); ok {
		if val, ok := resultVal["value"].(string); ok {
			resultText = val
		}
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: resultText}},
	}, nil
}

// eval evaluates JavaScript in the browser.
func (s *BrowserMCPServer) eval(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	script, _ := arguments["script"].(string)
	if script == "" {
		return nil, fmt.Errorf("script is required")
	}

	result, err := s.cdpRequest(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
		"returnByValue": true,
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Eval failed: %v", err)}},
			IsError: true,
		}, nil
	}

	resultText := fmt.Sprintf("%v", result)
	if resultVal, ok := result["result"].(map[string]interface{}); ok {
		if val, ok := resultVal["value"].(string); ok {
			resultText = val
		} else if val, ok := resultVal["description"].(string); ok {
			resultText = val
		}
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: resultText}},
	}, nil
}

// getText gets the text content of an element.
func (s *BrowserMCPServer) getText(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	selector, _ := arguments["selector"].(string)
	if selector == "" {
		selector = "body"
	}

	script := fmt.Sprintf(`
		(function() {
			var el = document.querySelector(%q);
			if (!el) return "Element not found: %s";
			return el.innerText || el.textContent || "";
		})()
	`, selector, selector)

	result, err := s.cdpRequest(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression": script,
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Get text failed: %v", err)}},
			IsError: true,
		}, nil
	}

	resultText := ""
	if resultVal, ok := result["result"].(map[string]interface{}); ok {
		if val, ok := resultVal["value"].(string); ok {
			resultText = val
		}
	}

	if resultText == "" {
		resultText = "(no text content found)"
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: resultText}},
	}, nil
}

// indexOf returns the index of the first occurrence of substr in s.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
