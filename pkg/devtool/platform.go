package devtool

import "os"

// getEnvOS reads an environment variable (cross-platform).
func getEnvOS(key string) string {
	return os.Getenv(key)
}

// fileExistsOS checks if a file exists (cross-platform).
func fileExistsOS(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
