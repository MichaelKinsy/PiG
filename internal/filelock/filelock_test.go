package filelock

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestUnsupportedRecognisesAFileSystemThatRefusesFileLocks(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"no lock manager":       {&os.PathError{Op: "flock", Path: "x", Err: syscall.ENOLCK}, true},
		"no flock":              {syscall.ENOSYS, true},
		"operation unsupported": {&os.PathError{Op: "flock", Path: "x", Err: syscall.EOPNOTSUPP}, true},
		"wrapped":               {errors.Join(errors.New("flock"), syscall.ENOLCK), true},
		"contended":             {syscall.EWOULDBLOCK, false},
		"deadline":              {context.DeadlineExceeded, false},
		"permission":            {&os.PathError{Op: "open", Path: "x", Err: syscall.EACCES}, false},
		"none":                  {nil, false},
	} {
		if got := Unsupported(tc.err); got != tc.want {
			t.Errorf("%s: Unsupported(%v) = %t, want %t", name, tc.err, got, tc.want)
		}
	}
}

func TestExplainNamesTheDirectoryAndTheRemedyOnlyForUnsupportedLocks(t *testing.T) {
	unsupported := &os.PathError{Op: "flock", Path: "x", Err: syscall.ENOLCK}
	explained := Explain("/mnt/home/.pig/cache/cells/go", unsupported)
	if !errors.Is(explained, syscall.ENOLCK) {
		t.Fatalf("explained error %q does not wrap the lock failure", explained)
	}
	for _, want := range []string{"/mnt/home/.pig/cache/cells/go", "does not support file locks", "PIG_HOME"} {
		if !strings.Contains(explained.Error(), want) {
			t.Errorf("explained error %q does not contain %q", explained, want)
		}
	}
	for _, other := range []error{syscall.EWOULDBLOCK, context.DeadlineExceeded} {
		if got := Explain("/mnt", other); !errors.Is(got, other) || got.Error() != other.Error() {
			t.Errorf("Explain(%v) = %v, want the error unchanged", other, got)
		}
	}
	if got := Explain("/mnt", nil); got != nil {
		t.Errorf("Explain(nil) = %v, want nil", got)
	}
}
