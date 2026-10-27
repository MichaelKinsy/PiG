package subprocess

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// buildContext bounds a call that compiles an extension or a cell (a Load, LoadAll or Reload of a Go or Rust source, or
// a BuildGoPackedCell). How long that takes is what the host needs: cargo locks the package's shared target directory
// (internal/testenv.SharedCargoTargetDir), so a Rust build waits behind every other build into it, and a cold SDK
// compile on a slow host outlasts any fixed bound (a Windows run stopped at 120.01 s). The call ends at the go test
// deadline (testbudget.DeadlineContext), not at a literal. The caller defers the returned cancel.
func buildContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(testbudget.DeadlineContext(t))
}
