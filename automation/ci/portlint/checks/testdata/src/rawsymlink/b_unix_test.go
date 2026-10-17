package rawsymlink

import (
	"os"
	"testing"
)

func TestUnixOnly(t *testing.T) {
	_ = os.Symlink("a", "b")
}
