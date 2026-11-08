package hosts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

// A retry that began a resolve whose start never follows (the last holder left, or the start was refused) leaves the
// relocation waiting for a worker that never shows: the wait is the operation's, so cancelling it ends the wait.
func TestAStartThatNeverShowsEndsWithTheCallersContext(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{}})
	workers := fixture.Server.Workers()
	ctx, cancel := context.WithCancel(p2test.Background)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- workers.StartedForTest(ctx, "no-worker-ever-starts", true) }()
	select {
	case err := <-result:
		t.Fatalf("started returned before its context ended: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("started = %v, want the context's cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("started still waits after its context ended")
	}
}
