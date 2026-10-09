//go:build unix

package routing_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
)

// Pi packages/server/src/server.ts:525 notifyConnectionCountChanged (callback option :74): onConnectionCountChanged receives the live connection count after each connect and disconnect, in order; a throwing callback is reported through onError and does not stop the server.
// mutation-checked: negating the condition `err != nil` at unix.go:334 fails it.
func TestServerReportsConnectionCountChangesInOrder(t *testing.T) {
	path := shortDirectory(t, "pcc-") + "/server.sock"
	listener, err := routing.CreateUnixListener(routing.UnixListenerOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var counts []int
	reported := make(chan error, 8)
	server, err := routing.NewServer(newTestServerHost(), routing.ServerOptions{
		Listeners: []routing.ServerListener{listener},
		ServerId:  testServerID,
		OnConnectionCountChanged: func(count int) {
			mu.Lock()
			counts = append(counts, count)
			mu.Unlock()
			if count == 2 {
				panic("count callback failed")
			}
		},
		OnError: func(err error) { reported <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	snapshot := func() []int {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(counts)
	}
	first := connectUnixTestClient(t, path)
	first.hello(t)
	pollUntil(t, "first connection counted", func() bool { return slices.Equal(snapshot(), []int{1}) })
	second := connectUnixTestClient(t, path)
	second.hello(t)
	pollUntil(t, "second connection counted", func() bool { return slices.Equal(snapshot(), []int{1, 2}) })
	if err := <-reported; err == nil || err.Error() != "count callback failed" {
		t.Fatalf("reported %v", err)
	}
	_ = second.Close()
	_ = first.Close()
	pollUntil(t, "both disconnects counted", func() bool { return slices.Equal(snapshot(), []int{1, 2, 1, 0}) })
}
