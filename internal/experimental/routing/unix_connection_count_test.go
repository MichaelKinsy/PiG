//go:build unix

package routing_test

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// upstream: packages/server/src/transports/unix/preset.ts:23 forwards onConnectionCountChanged, and server.ts:523-527 calls it with the live connection count after each change; a throwing observer is reported through onError, never into server state.
// mutation-checked: negating the condition `c.closed || c.closing` at unix.go:76 fails it.
func TestUnixServerReportsConnectionCountChanges(t *testing.T) {
	path := socketPath(t, false)
	var mu sync.Mutex
	var counts []int
	var failures []error
	changed := make(chan struct{}, 8)
	server, err := routing.CreateUnixServer(newTestServerHost(), routing.UnixServerOptions{
		Path: path, ServerId: testServerID,
		OnConnectionCountChanged: func(count int) {
			mu.Lock()
			counts = append(counts, count)
			mu.Unlock()
			changed <- struct{}{}
			if count == 2 {
				panic("observer failure")
			}
		},
		OnError: func(err error) {
			mu.Lock()
			failures = append(failures, err)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := startError(server); err != nil {
		t.Fatal(err)
	}
	wait := func(want int) {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("no count change; counts so far %v, want %d more", counts, want)
		}
	}
	first := connectUnixTestClient(t, path)
	expectHello(t, first, testServerID)
	wait(1)
	second := connectUnixTestClient(t, path)
	expectHello(t, second, testServerID)
	wait(2)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	wait(1)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	wait(0)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(counts, []int{1, 2, 1, 0}) {
		t.Errorf("counts = %v, want [1 2 1 0]", counts)
	}
	if len(failures) != 1 || failures[0] == nil || failures[0].Error() != "observer failure" {
		t.Errorf("observer failure reports = %v, want one \"observer failure\"", failures)
	}
}
