package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestCodeService() *CodeService {
	return NewCodeService()
}

// --- Code Info ---

func TestCodeInfo(t *testing.T) {
	svc := newTestCodeService()

	req := httptest.NewRequest(http.MethodGet, "/v1/code/info", nil)
	w := httptest.NewRecorder()
	svc.Info(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("info: expected 200, got %d", w.Code)
	}

	var resp CodeInfoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.Languages) != 4 {
		t.Fatalf("expected 4 languages, got %d", len(resp.Languages))
	}

	// Check entries
	pyFound := false
	nodeFound := false
	goFound := false
	javaFound := false
	for _, lang := range resp.Languages {
		if lang.Language == "python" {
			pyFound = true
		}
		if lang.Language == "nodejs" {
			nodeFound = true
		}
		if lang.Language == "go" {
			goFound = true
		}
		if lang.Language == "java" {
			javaFound = true
		}
	}
	if !pyFound {
		t.Error("python language not found in info")
	}
	if !nodeFound {
		t.Error("nodejs language not found in info")
	}
	if !goFound {
		t.Error("go language not found in info")
	}
	if !javaFound {
		t.Error("java language not found in info")
	}
}

// --- Code Execute: missing code ---

func TestCodeExecute_MissingCode(t *testing.T) {
	svc := newTestCodeService()

	body := `{"language":"python"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Code Execute: unsupported language ---

func TestCodeExecute_UnsupportedLanguage(t *testing.T) {
	svc := newTestCodeService()

	body := `{"language":"rust","code":"fn main() {}"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported language, got %d", w.Code)
	}
}

// --- Code Execute: Python ---

func TestCodeExecute_Python(t *testing.T) {
	svc := newTestCodeService()

	if svc.pythonPath == "" {
		t.Skip("python not available on this system")
	}

	body := `{"language":"python","code":"print('hello from python')"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute python: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello from python") {
		t.Errorf("expected stdout to contain 'hello from python', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "python" {
		t.Errorf("expected runtime=python, got %s", resp.Runtime)
	}
}

// --- Code Execute: Python with error ---

func TestCodeExecute_PythonError(t *testing.T) {
	svc := newTestCodeService()

	if svc.pythonPath == "" {
		t.Skip("python not available on this system")
	}

	body := `{"language":"python","code":"import sys; sys.exit(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ExitCode != 1 {
		t.Errorf("expected exitCode=1, got %d", resp.ExitCode)
	}
}

// --- Code Execute: Node.js ---

func TestCodeExecute_NodeJS(t *testing.T) {
	svc := newTestCodeService()

	if svc.nodePath == "" {
		t.Skip("node not available on this system")
	}

	body := `{"language":"nodejs","code":"console.log('hello from node')"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute nodejs: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d", resp.ExitCode)
	}
	if !strings.Contains(resp.Stdout, "hello from node") {
		t.Errorf("expected stdout to contain 'hello from node', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "nodejs" {
		t.Errorf("expected runtime=nodejs, got %s", resp.Runtime)
	}
}

// --- Code Execute: Node.js with error ---

func TestCodeExecute_NodeJSError(t *testing.T) {
	svc := newTestCodeService()

	if svc.nodePath == "" {
		t.Skip("node not available on this system")
	}

	body := `{"language":"nodejs","code":"process.exit(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.ExitCode != 1 {
		t.Errorf("expected exitCode=1, got %d", resp.ExitCode)
	}
}

// --- Code Execute: language aliases ---

func TestCodeExecute_LanguageAliases(t *testing.T) {
	svc := newTestCodeService()

	aliases := []string{"nodejs", "node", "javascript", "js"}
	for _, alias := range aliases {
		if svc.nodePath == "" {
			t.Skip("node not available")
		}
		body := `{"language":"` + alias + `","code":"console.log(1)"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		svc.Execute(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("language alias '%s': expected 200, got %d", alias, w.Code)
		}
	}
}

// --- Code Execute: Go ---

func TestCodeExecute_Go(t *testing.T) {
	svc := newTestCodeService()

	if svc.goPath == "" {
		t.Skip("go not available on this system")
	}

	body := `{"language":"go","code":"package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello from go\") }"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute go: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d (stderr=%s)", resp.ExitCode, resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "hello from go") {
		t.Errorf("expected stdout to contain 'hello from go', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "go" {
		t.Errorf("expected runtime=go, got %s", resp.Runtime)
	}
}

// TestCodeExecute_GoWithoutPackage verifies the service wraps bare code with
// `package main` so callers can submit a main() body without ceremony.
func TestCodeExecute_GoWithoutPackage(t *testing.T) {
	svc := newTestCodeService()
	if svc.goPath == "" {
		t.Skip("go not available on this system")
	}

	body := `{"language":"go","code":"func main() { println(42) }"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute go: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d (stderr=%s)", resp.ExitCode, resp.Stderr)
	}
}

func TestCodeExecute_GoAlias(t *testing.T) {
	svc := newTestCodeService()
	if svc.goPath == "" {
		t.Skip("go not available on this system")
	}
	body := `{"language":"golang","code":"package main\nfunc main() {}"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("alias golang: expected 200, got %d", w.Code)
	}
}

// --- Code Execute: Java ---

func TestCodeExecute_Java(t *testing.T) {
	svc := newTestCodeService()
	if svc.javaPath == "" || svc.javacPath == "" {
		t.Skip("java/javac not available on this system")
	}

	body := `{"language":"java","code":"public class Main { public static void main(String[] args) { System.out.println(\"hello from java\"); } }"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute java: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var resp CodeExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d (stderr=%s)", resp.ExitCode, resp.Stderr)
	}
	if !strings.Contains(resp.Stdout, "hello from java") {
		t.Errorf("expected stdout to contain 'hello from java', got '%s'", resp.Stdout)
	}
	if resp.Runtime != "java" {
		t.Errorf("expected runtime=java, got %s", resp.Runtime)
	}
}

// TestCodeExecute_JavaClassRename verifies that a public class with a
// different name gets rewritten to Main so the file name (Main.java) matches.
func TestCodeExecute_JavaClassRename(t *testing.T) {
	svc := newTestCodeService()
	if svc.javaPath == "" || svc.javacPath == "" {
		t.Skip("java/javac not available on this system")
	}

	body := `{"language":"java","code":"public class HelloWorld { public static void main(String[] args) { System.out.println(123); } }"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.Execute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute java: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var resp CodeExecuteResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.ExitCode != 0 {
		t.Errorf("expected exitCode=0, got %d (stderr=%s)", resp.ExitCode, resp.Stderr)
	}
}

// --- findExecutable ---

func TestFindExecutable_Existing(t *testing.T) {
	// On most systems, "go" should be available
	path := findExecutable("go")
	if path == "" {
		t.Skip("go executable not found in PATH")
	}
	if path == "" {
		t.Error("expected to find 'go' executable")
	}
}

func TestFindExecutable_NonExisting(t *testing.T) {
	path := findExecutable("nonexistent_binary_12345")
	if path != "" {
		t.Errorf("expected empty path for nonexistent binary, got %s", path)
	}
}
