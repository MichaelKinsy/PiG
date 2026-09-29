package inproc

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestCommandActionsForwardTheRequestContext(t *testing.T) {
	type requestKey struct{}
	request, cancel := context.WithCancel(context.WithValue(t.Context(), requestKey{}, "request-value"))
	cancel()
	var seen []string
	check := func(ctx context.Context, name string) error {
		if ctx.Value(requestKey{}) != "request-value" {
			t.Errorf("%s lost request values", name)
		}
		seen = append(seen, name)
		return ctx.Err()
	}
	runner := NewRunner(nil, t.TempDir())
	runner.BindCommandActions(extension.CommandActions{
		WaitForIdleContext: func(ctx context.Context) error { return check(ctx, "wait") },
		NewSessionContext: func(ctx context.Context, _ *extension.NewSessionOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{}, check(ctx, "new")
		},
		ForkContext: func(ctx context.Context, _ string, _ *extension.ForkOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{}, check(ctx, "fork")
		},
		NavigateTreeContext: func(ctx context.Context, _ string, _ *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{}, check(ctx, "tree")
		},
		SwitchSessionContext: func(ctx context.Context, _ string, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
			return extension.CancelledResult{}, check(ctx, "switch")
		},
		ReloadContext: func(ctx context.Context) error { return check(ctx, "reload") },
	})
	command := runner.CreateCommandContext()
	extension.WithCommandContext(request, command)
	calls := []func() error{
		command.WaitForIdle,
		func() error { _, err := command.NewSession(nil); return err },
		func() error { _, err := command.Fork("entry", nil); return err },
		func() error { _, err := command.NavigateTree("entry", nil); return err },
		func() error { _, err := command.SwitchSession("session.jsonl", nil); return err },
		command.Reload,
	}
	for _, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("command error=%v", err)
		}
	}
	if len(seen) != len(calls) {
		t.Fatalf("forwarded %v", seen)
	}
}

func TestCommandActionRebindingReplacesBothInvocationForms(t *testing.T) {
	runner := NewRunner(nil, t.TempDir())
	want := errors.New("replacement")
	runner.BindCommandActions(extension.CommandActions{ReloadContext: func(context.Context) error { return errors.New("old") }})
	runner.BindCommandActions(extension.CommandActions{Reload: func() error { return want }})
	if err := runner.CreateCommandContext().Reload(); !errors.Is(err, want) {
		t.Fatalf("old contextual action survived rebind: %v", err)
	}
}
