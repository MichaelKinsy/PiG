package rawsymlink

import (
	"os"
	"testing"
)

func TestBad(t *testing.T) {
	_ = os.Symlink("a", "b") // want `os.Symlink in a Windows-built test`
}
