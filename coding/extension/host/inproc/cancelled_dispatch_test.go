package inproc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A handler interrupted because the host cancelled the dispatch (shutdown, a
// termination signal) did not fail. Upstream never cancels a running handler;
// its SIGTERM path disposes the runtime and exits without reporting the
// interrupted work, so only real handler failures reach error listeners.
func TestCancelledDispatchIsNotReportedAsAHandlerError(t *testing.T) {
	var handlerErr error
	var commandErr error
	exts := []extension.Extension{{
		Path: "cancel-probe",
		Handlers: map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) {
			return nil, handlerErr
		}}},
		Commands: map[string]extension.RegisteredCommand{"probe": {Name: "probe", Handler: func(context.Context, string) error {
			return commandErr
		}}},
		CommandOrder: []string{"probe"},
	}}
	r := inproc.NewRunner(exts, ".")
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Event+": "+err.Error) })

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	interrupted := fmt.Errorf("event session_start: %w", context.Canceled)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want []string
	}{
		{"interrupted by the host", cancelled, interrupted, nil},
		{"extension stopped by the host", context.Background(), fmt.Errorf("%w: %w", extension.ErrHandlerStopped, interrupted), nil},
		{"failure after cancellation", cancelled, errors.New("boom"), []string{"session_start: boom", "command: boom"}},
		{"cancellation the host did not ask for", context.Background(), interrupted, []string{
			"session_start: event session_start: context canceled", "command: event session_start: context canceled",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reported = nil
			handlerErr, commandErr = tc.err, tc.err
			_, _ = r.Emit(tc.ctx, extension.SessionStartEvent{Type: "session_start"})
			r.ExecuteCommand(tc.ctx, "probe", "")
			if fmt.Sprint(reported) != fmt.Sprint(tc.want) {
				t.Fatalf("reported %q, want %q", reported, tc.want)
			}
		})
	}
}
