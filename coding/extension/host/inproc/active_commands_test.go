package inproc

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A command handler that requests a Session replacement is still running while the replacement retires its Runner. The Runner reports the running handler so the retirement waits for it.
func TestRunnerWaitForCommandsWaitsForRunningHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		runner := NewRunner([]extension.Extension{{
			Name: "wait", Path: "/ext/wait", CommandOrder: []string{"hold"},
			Commands: map[string]extension.RegisteredCommand{"hold": {Name: "hold", Handler: func(context.Context, string) error {
				close(started)
				<-release
				return nil
			}}},
		}}, t.TempDir())
		go runner.ExecuteCommand(context.Background(), "hold", "")
		<-started
		if got := runner.ActiveCommands(); got != 1 {
			t.Fatalf("ActiveCommands during handler = %d, want 1", got)
		}
		waited := make(chan error, 1)
		go func() { waited <- runner.WaitForCommands(context.Background()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("WaitForCommands returned %v while the handler still ran", err)
		default:
		}
		close(release)
		if err := <-waited; err != nil {
			t.Fatalf("WaitForCommands after the handler returned = %v", err)
		}
		if got := runner.ActiveCommands(); got != 0 {
			t.Fatalf("ActiveCommands after handler = %d, want 0", got)
		}
	})
}

func TestRunnerWaitForCommandsStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		runner := NewRunner([]extension.Extension{{
			Name: "stuck", Path: "/ext/stuck", CommandOrder: []string{"hold"},
			Commands: map[string]extension.RegisteredCommand{"hold": {Name: "hold", Handler: func(context.Context, string) error {
				close(started)
				<-release
				return nil
			}}},
		}}, t.TempDir())
		go runner.ExecuteCommand(context.Background(), "hold", "")
		<-started
		ctx, cancel := context.WithCancel(context.Background())
		waited := make(chan error, 1)
		go func() { waited <- runner.WaitForCommands(ctx) }()
		synctest.Wait()
		cancel()
		if err := <-waited; !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForCommands after cancel = %v, want context.Canceled", err)
		}
		close(release)
	})
}
