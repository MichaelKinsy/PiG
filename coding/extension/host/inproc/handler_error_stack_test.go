package inproc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// stackedError carries its own stack, as an error returned by a subprocess
// extension carries the thrown JavaScript error's `stack`.
type stackedError struct{ stack string }

func (e stackedError) Error() string      { return "boom" }
func (e stackedError) ErrorStack() string { return e.stack }

// Upstream reports a failing handler's own `err.stack` (runner.ts emitError),
// which interactive mode prints dimmed under the error line. The host's
// dispatch stack says nothing about the extension, so PiG never reports it.
func TestHandlerErrorsReportTheFailuresOwnStack(t *testing.T) {
	const jsStack = "TypeError: boom\n    at onTurnStart (file:///ext/index.js:10:5)"
	handlers := map[string]extension.HandlerFn{
		"returned": func(...any) (any, error) { return nil, errors.New("boom") },
		"stacked":  func(...any) (any, error) { return nil, stackedError{stack: jsStack} },
		"panicked": func(...any) (any, error) { panic("boom") },
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			ext := extension.Extension{
				Path:     "exts/failing.ts",
				Handlers: map[string][]extension.HandlerFn{"agent_start": {handler}},
			}
			runner := NewRunner([]extension.Extension{ext}, t.TempDir())
			var got []*extension.ExtensionError
			runner.AddErrorListener(func(err *extension.ExtensionError) { got = append(got, err) })
			_, _ = runner.Emit(context.Background(), extension.AgentStartEvent{Type: "agent_start"})
			if len(got) != 1 {
				t.Fatalf("reported %d errors, want 1", len(got))
			}
			stack := got[0].Stack
			if strings.Contains(stack, "(*Runner).recordHandlerError") {
				t.Fatalf("stack was captured at the host's report site:\n%s", stack)
			}
			switch name {
			case "returned":
				if stack != "" {
					t.Fatalf("a plain returned error has no stack, got:\n%s", stack)
				}
			case "stacked":
				if stack != jsStack {
					t.Fatalf("stack = %q, want the error's own %q", stack, jsStack)
				}
			case "panicked":
				first, rest, _ := strings.Cut(stack, "\n")
				if first != got[0].Error || !strings.Contains(rest, "handler_error_stack_test.go") {
					t.Fatalf("panic stack must start with the message and hold the panicking frame:\n%s", stack)
				}
			}
		})
	}
}
