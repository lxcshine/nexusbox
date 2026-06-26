//go:build !windows


package win32

import "fmt"

// CreateSandboxToken is a stub for non-Windows platforms
func CreateSandboxToken() (uintptr, error) {
	return 0, fmt.Errorf("sandbox token not supported on this platform")
}

// ApplyProcessMitigations is a stub for non-Windows platforms
func ApplyProcessMitigations(process uintptr) error {
	return fmt.Errorf("process mitigations not supported on this platform")
}

// ApplyJobUIRestrictions is a stub for non-Windows platforms
func ApplyJobUIRestrictions(job uintptr) error {
	return fmt.Errorf("job UI restrictions not supported on this platform")
}

// BuildSanitizedEnvironment is a stub for non-Windows platforms
func BuildSanitizedEnvironment(customEnv map[string]string) []string {
	return nil
}
