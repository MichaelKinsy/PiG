package experimental

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// coordinator.ts uses a live Set keyed by callback reference, not by subscription.
func TestCoordinatorConnectionListenerIdentity(t *testing.T) {
	for _, cleanup := range []int{0, 1} {
		t.Run(fmt.Sprint(cleanup), func(t *testing.T) {
			server := NewCoordinatorConnection(CoordinatorConnectionOptions{})
			calls := 0
			listener := NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { calls++ })
			remove := []func(){server.OnEvent(listener), server.OnEvent(listener)}
			emit := func(want int) {
				t.Helper()
				calls = 0
				if err := server.handleMessage(json.RawMessage(`{"type":"peer_connected","peerId":"p"}`)); err != nil {
					t.Fatal(err)
				}
				if calls != want {
					t.Errorf("listener called %d times, want %d", calls, want)
				}
			}
			emit(1)
			remove[cleanup]()
			emit(0)
			server.OnEvent(listener)
			emit(1)
			// A cleanup deletes by reference, including a later registration of that reference.
			remove[cleanup]()
			emit(0)
			server.OnEvent(listener)
			remove[1-cleanup]()
			emit(0)
		})
	}
}

func TestCoordinatorConnectionListenerLiveSet(t *testing.T) {
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{})
	var calls []string
	makeListener := func(name string) *CoordinatorConnectionListener {
		return NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { calls = append(calls, name) })
	}
	b, c, d := makeListener("b"), makeListener("c"), makeListener("d")
	var a *CoordinatorConnectionListener
	var removeA, removeB func()
	first := true
	a = NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) {
		calls = append(calls, "a")
		if first {
			first = false
			removeB()
			server.OnEvent(b)
			server.OnEvent(d)
			removeA()
			server.OnEvent(a)
		}
	})
	removeA = server.OnEvent(a)
	removeB = server.OnEvent(b)
	server.OnEvent(c)
	// Adding an existing member must not move it to the end of the Set.
	server.OnEvent(a)
	server.OnEvent(b)
	for _, want := range [][]string{{"a", "c", "b", "d", "a"}, {"c", "b", "d", "a"}} {
		calls = nil
		if err := server.handleMessage(json.RawMessage(`{"type":"message","from":"p","payload":null}`)); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(calls, want) {
			t.Errorf("live listener order %v, want %v", calls, want)
		}
	}
}

// The pinned connection and the native receive path both deliver once, then never after either cleanup.
func TestCoordinatorConnectionListenerIdentitySocket(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, cleanup := range []int{0, 1} {
		t.Run(fmt.Sprintf("upstream/%d", cleanup), func(t *testing.T) {
			if runtime.GOOS == "windows" {
				// The upstream probe listens on a socket file path; Node on
				// Windows listens only on named pipes (EACCES).
				t.Skip("upstream's experimental coordinator cannot listen on Windows: Node listens only on named pipes, not socket file paths")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "--import", upstreamLoaderImport(root), filepath.Join(root, "internal/experimental/testdata/coordinator-listener-oracle.mjs"), t.TempDir(), fmt.Sprint(cleanup))
			cmd.Env = append(os.Environ(), "PIG_TEST_ROOT="+root)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("upstream listener probe: %v\n%s", err, output)
			}
			if string(output) != "[1,0]\n" {
				t.Fatalf("upstream listener counts: %s", output)
			}
		})
		t.Run(fmt.Sprintf("native/%d", cleanup), func(t *testing.T) {
			public, control, _ := startTestCoordinator(t, false)
			peer := controlDial(t, control)
			peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "worker"})
			peer.want(t, `{"type":"peer_registered","peerId":"worker"}`)
			server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend", ServerConnectionID: new("s")})
			t.Cleanup(server.Close)
			calls := 0
			listener := NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) { calls++ })
			remove := []func(){server.OnEvent(listener), server.OnEvent(listener)}
			observed := make(chan int, 1)
			server.OnEvent(NewCoordinatorConnectionListener(func(CoordinatorConnectionEvent) {
				count := calls
				calls = 0
				remove[cleanup]()
				observed <- count
			}))
			if err := server.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			peer.want(t, `{"type":"server_connected","serverConnectionId":"s"}`)
			for _, want := range []int{1, 0} {
				peer.send(t, map[string]any{"type": "send", "to": "server", "payload": nil})
				select {
				case got := <-observed:
					if got != want {
						t.Errorf("socket listener called %d times, want %d", got, want)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("missing listener observation")
				}
			}
		})
	}
}
