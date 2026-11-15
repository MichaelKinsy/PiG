//go:build linux || darwin

package atomicfile

import (
	"bytes"
	"testing"

	"golang.org/x/sys/unix"
)

// growingAttribute makes getxattr answer for an attribute that is first long at the first call (the size query) and final from the second call on, as
// another process setting it between the calls does. Like the kernel, a call with no room answers the size and a short buffer answers ERANGE.
func growingAttribute(t *testing.T, first, final []byte) {
	t.Helper()
	previous := getxattr
	value := first
	getxattr = func(_, _ string, dest []byte) (int, error) {
		current := value
		value = final
		switch {
		case len(dest) == 0:
			return len(current), nil
		case len(dest) < len(current):
			return -1, unix.ERANGE
		}
		return copy(dest, current), nil
	}
	t.Cleanup(func() { getxattr = previous })
}

// An attribute that grows between the size query and the read is read in full: an empty buffer is a size query again, which must not be sliced beyond its
// capacity, and a buffer that is too short is ERANGE.
func TestGetXattrReadsAnAttributeThatGrowsBetweenTheCalls(t *testing.T) {
	for _, c := range []struct {
		name         string
		first, final []byte
	}{
		{"from empty", nil, []byte("grown")},
		{"from short", []byte("ab"), []byte("grown")},
	} {
		t.Run(c.name, func(t *testing.T) {
			growingAttribute(t, c.first, c.final)
			var got []byte
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("getXattr panicked: %v", r)
					}
				}()
				got, err = getXattr("file", "user.pig-test")
			}()
			if err != nil || !bytes.Equal(got, c.final) {
				t.Fatalf("getXattr = %q, %v; want %q", got, err, c.final)
			}
		})
	}
}
