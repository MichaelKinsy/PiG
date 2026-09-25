package codingagent

import (
	"runtime"
	"testing"
)

// requireStandaloneSelfUpdateTier skips a test of the standalone tier on
// Windows, where a standalone pig.exe resolves to the unsupported tier and is
// never replaced in place (D39).
func requireStandaloneSelfUpdateTier(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("D39: a standalone Windows binary is not replaced in place")
	}
}
