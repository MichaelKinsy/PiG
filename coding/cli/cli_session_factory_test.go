package cli

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func runnerWithHeldCommand(t *testing.T) (runner *inproc.Runner, started, release chan struct{}) {
	t.Helper()
	started, release = make(chan struct{}), make(chan struct{})
	runner = inproc.NewRunner([]extension.Extension{{
		Name: "hold", Path: "/ext/hold", CommandOrder: []string{"hold"},
		Commands: map[string]extension.RegisteredCommand{"hold": {Name: "hold", Handler: func(context.Context, string) error {
			close(started)
			<-release
			return nil
		}}},
	}}, t.TempDir())
	go runner.ExecuteCommand(context.Background(), "hold", "")
	<-started
	return runner, started, release
}

// A command handler that awaits a Session replacement is still running when the factory retires the replaced Session's extension host. The host must outlive that handler (D70 records that the replaced Session's process stops after its command returns).
func TestRetirementWaitsForTheRunningCommandHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		retirement := newCLIRetirement(context.Background())
		runner, _, release := runnerWithHeldCommand(t)
		released := make(chan struct{})
		retirement.retire(runner, func() { close(released) })
		synctest.Wait()
		select {
		case <-released:
			t.Fatal("the host was retired while its command handler still ran")
		default:
		}
		close(release)
		synctest.Wait()
		select {
		case <-released:
		default:
			t.Fatal("the host stayed alive after its command handler returned")
		}
		retirement.close()
	})
}

// A host with no running command retires at once, and closing the factory does not wait for a handler that never returns.
func TestRetirementIsImmediateWhenIdleAndBoundedAtClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		retirement := newCLIRetirement(context.Background())
		idle := inproc.NewRunner(nil, t.TempDir())
		retiredIdle := false
		retirement.retire(idle, func() { retiredIdle = true })
		if !retiredIdle {
			t.Fatal("an idle host was not retired synchronously")
		}
		runner, _, release := runnerWithHeldCommand(t)
		retiredBusy := false
		retirement.retire(runner, func() { retiredBusy = true })
		retirement.close()
		if !retiredBusy {
			t.Fatal("close left a host whose handler never returned")
		}
		close(release)
	})
}
