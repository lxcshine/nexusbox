//go:build !windows

package runtime

import (
	"fmt"
)

// RegisterJobObjectProvider returns an error on non-Windows platforms.
// On Linux, the runc/kata/gvisor providers should be used instead.
// This stub ensures the code compiles cross-platform.
func RegisterJobObjectProvider(rm *RuntimeManager) error {
	return fmt.Errorf("Windows Job Object provider is only available on Windows; use runc/kata/gvisor providers on Linux")
}
