package coding

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi agent-session-runtime.ts:167-192 drains all outgoing work before shutdown
// and awaits rebind. A refused before hook neither drains nor replaces it.
func TestSessionReplacementModeHooksAreAwaited(t *testing.T) {
	var trace []string
	cancelled := true
	runner := inproc.NewRunner([]extension.Extension{{Path: "/hooks", Handlers: map[string][]extension.HandlerFn{
		"session_before_switch": {func(...any) (any, error) {
			trace = append(trace, "before")
			return extension.SessionBeforeSwitchResult{Cancel: cancelled}, nil
		}},
		"session_shutdown": {func(...any) (any, error) { trace = append(trace, "shutdown"); return nil, nil }},
		"session_start":    {func(...any) (any, error) { trace = append(trace, "unexpected start"); return nil, nil }},
	}}}, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	failure := errors.New("rebind rejected")
	session.SetBeforeSessionReplacement(func(context.Context) error { trace = append(trace, "drain"); return nil })
	session.SetRebindSession(func(context.Context, extension.SessionStartEvent) error {
		trace = append(trace, "rebind")
		return failure
	})
	id := session.ID()
	result, err := session.ExtensionCommandActions().NewSessionContext(t.Context(), nil)
	if err != nil || !result.Cancelled || session.ID() != id || !slices.Equal(trace, []string{"before"}) {
		t.Fatalf("cancel: %+v %v %v", result, err, trace)
	}
	cancelled, trace = false, nil
	result, err = session.ExtensionCommandActions().NewSessionContext(t.Context(), nil)
	if !errors.Is(err, failure) || result.Cancelled || session.ID() == id || !slices.Equal(trace, []string{"before", "drain", "shutdown", "rebind"}) {
		t.Fatalf("rebind: %+v %v %v", result, err, trace)
	}
}
