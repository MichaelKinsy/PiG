package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Pi's AgentSessionRuntime.dispose awaits session_shutdown, then invalidates every extension context before returning to process.exit. Teardown is not a sequence of live provider removals: no ending extension needs another catalog snapshot.
func TestHostShutdownDetachesCatalogBeforeProviderRemoval(t *testing.T) {
	for _, count := range []int{0, 1, 20} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, bridge, peers := shutdownCatalogHost(t.Context(), count)
				defer peers.Wait()
				builds := 0
				bridge.SetHostAction("getModels", func() []json.RawMessage {
					builds++
					// Make a redundant read observably slow without a wall-clock timeout.
					time.Sleep(time.Second)
					return modelJSONs(map[string]any{"provider": "fixture", "id": "model"})
				})
				builds = 0 // SetHostAction publishes the live setup snapshot.
				start := time.Now()
				h.Shutdown("quit")
				if builds != 0 || time.Since(start) != 0 {
					t.Errorf("shutdown rebuilt %d catalogs and waited %s for retired consumers", builds, time.Since(start))
				}
				if len(h.exts) != 0 || len(bridge.extConns) != 0 || len(bridge.registeredProviderOrder) != 0 || len(bridge.registeredProviderConfigs) != 0 {
					t.Fatal("shutdown retained extensions, connections, or provider registrations")
				}
			})
		})
	}
}

// A transport shutdown is one-way, unlike the awaited session_shutdown event. An extension that reads the barrier but does not finish its own cleanup cannot hold the Host open; the Host cancels it and joins its connection workers.
func TestHostShutdownDoesNotAwaitTransportCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHost("")
		host, peer := net.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		conn := NewConn("slow-cleanup", host)
		conn.Start(ctx)
		readBarrier := make(chan struct{})
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			defer func() { _ = peer.Close() }()
			for {
				env, err := readEnvelope(peer)
				if err != nil {
					return
				}
				if env.Type == MsgShutdown {
					close(readBarrier)
					<-ctx.Done()
					return
				}
			}
		}()
		h.exts["slow-cleanup"] = withConn(&managedExt{config: ExtConfig{Name: "slow-cleanup"}, cancel: cancel, exitedCh: exited}, conn)
		start := time.Now()
		h.Shutdown("quit")
		if time.Since(start) != 0 {
			t.Fatalf("Host awaited transport cleanup for %s", time.Since(start))
		}
		select {
		case <-readBarrier:
		default:
			t.Fatal("extension never received the shutdown barrier")
		}
		select {
		case <-exited:
		default:
			t.Fatal("Host returned before joining the extension")
		}
	})
}

func shutdownCatalogHost(ctx context.Context, count int) (*Host, *UIBridge, *sync.WaitGroup) {
	h := NewHost("")
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	peers := new(sync.WaitGroup)
	for i := range count {
		name := fmt.Sprintf("provider-%d", i)
		host, peer := net.Pipe()
		conn := NewConn(name, host)
		conn.Start(ctx)
		peers.Go(func() {
			defer func() { _ = peer.Close() }()
			_, _ = io.Copy(io.Discard, peer)
		})
		bridge.RegisterExtConn(name, conn, true)
		bridge.RecordProviderRegistration(name, []byte(`{"baseUrl":"http://fixture.invalid"}`))
		h.exts[name] = withConn(&managedExt{config: ExtConfig{Name: name}, providerNames: []string{name}}, conn)
	}
	return h, bridge, peers
}

// awaitGoroutinesParked returns once no goroutine but the caller can run: every other one waits for a channel, a lock, a timer or its I/O. It is
// to a benchmark, which has no bubble, what synctest.Wait is to the shutdown tests. stacks is the caller's buffer for the goroutines' stacks, so
// the wait allocates nothing.
func awaitGoroutinesParked(tb testing.TB, stacks []byte) {
	tb.Helper()
	for range 10000 {
		runtime.Gosched()
		dump := stacks[:runtime.Stack(stacks, true)]
		if len(dump) < len(stacks) && !bytes.Contains(dump, []byte(" [runnable")) && bytes.Count(dump, []byte(" [running")) == 1 {
			return
		}
	}
	tb.Fatalf("goroutines kept running:\n%s", stacks[:runtime.Stack(stacks, true)])
}

// The measured interval is Shutdown and the end of every goroutine of the connections it stops. It starts once those goroutines wait for their
// first frame, as the connections of a host that shuts down do, and ends once they and their peers have ended: what a connection's goroutines
// allocate to start, or to end after Shutdown returned, fell inside the interval on one run and outside it on the next (1066 to 1239 allocations
// for twenty connections in five runs).
func BenchmarkHostShutdownProviderCatalog(b *testing.B) {
	for _, count := range []int{0, 1, 20} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			models := make([]json.RawMessage, 2000)
			for i := range models {
				models[i] = modelJSON(map[string]any{"id": fmt.Sprint(i), "provider": "fixture", "contextWindow": 128000, "maxTokens": 8192})
			}
			stacks := make([]byte, 1<<20)
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				h, bridge, peers := shutdownCatalogHost(b.Context(), count)
				bridge.SetHostAction("getModels", func() []json.RawMessage { return models })
				awaitGoroutinesParked(b, stacks)
				b.StartTimer()
				h.Shutdown("quit")
				peers.Wait()
				awaitGoroutinesParked(b, stacks)
			}
		})
	}
}
