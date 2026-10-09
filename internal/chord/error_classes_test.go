package chord_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// packages/chord services/errors.ts:18-26 RemoteServiceError and delta/index.ts:133-141,310-317 UnsafePathError/PathError: Error classes whose
// constructors set `name`; the messages are built from the code, path and segment.
func TestChordErrorClassesCarryNameAndMessage(t *testing.T) {
	cases := []struct {
		err           interface{ Error() string }
		name, message string
	}{
		{chord.NewRemoteServiceError(chord.ErrServiceNotFound, "no such service"), "RemoteServiceError", "no such service"},
		{delta.NewPathError([]any{"a", float64(1)}), "PathError", `unresolvable path: ["a",1]`},
		{delta.NewUnsafePathError("__proto__"), "UnsafePathError", "unsafe path segment: __proto__"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		if named.Name() != c.name || c.err.Error() != c.message {
			t.Errorf("%T: name=%q message=%q, want %q %q", c.err, named.Name(), c.err.Error(), c.name, c.message)
		}
	}
}
