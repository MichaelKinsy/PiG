//go:build darwin || linux

package ai

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// Writing through the link preserves the target's extended attribute and its value.
func TestR3bExtendedAttributeIsKept(t *testing.T) {
	s1, _, _, _, target := linkedStores(t, `{}`)
	name := "com.pig.fixture"
	if runtime.GOOS == "linux" {
		name = "user.pig.fixture"
	}
	if err := unix.Setxattr(target, name, []byte("x"), 0); err != nil {
		t.Skipf("filesystem refuses extended attributes: %v", err)
	}
	if _, err := unix.Getxattr(target, name, nil); err != nil {
		t.Skipf("attribute not readable: %v", err)
	}
	setKey(t, s1, "p", "fixture-refresh-A")
	buf := make([]byte, 8)
	n, err := unix.Getxattr(target, name, buf)
	if err != nil || string(buf[:n]) != "x" {
		t.Fatalf("attribute after the write = %q, %v; want x", buf[:n], err)
	}
}
