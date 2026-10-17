//go:build linux

package cli

import (
	"os"
	"testing"
)

// openModePTY allocates a Linux pseudo-terminal pair.
func openModePTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	return openPTY(t, 24, 80)
}
