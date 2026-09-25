package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// This drives the real Pi RpcPeer through a spawned Node process and the production child-pipe connection, rather than reimplementing its frame rules in the test.
func TestRpcUpstreamChildPeer(t *testing.T) {
	root, err := filepath.Abs("../../../../.upstream/current")
	if err != nil {
		t.Fatal(err)
	}
	processContext, stopProcess := context.WithCancel(context.Background())
	t.Cleanup(stopProcess)
	command := exec.CommandContext(processContext, "node", "testdata/rpc-oracle.mjs", root)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	connection, err := ChildConnection(stdin, stdout, nil)
	if err != nil {
		t.Fatal(err)
	}
	zero := float64(0)
	peer := CreatePeer(connection, PeerOptions{DeadMs: &zero})
	t.Cleanup(func() {
		_ = peer.Close()
		peer.Wait()
		connection.Wait()
		if err := command.Wait(); err != nil {
			t.Errorf("Node peer: %v: %s", err, stderr.String())
		}
	})
	token := DefineService[struct{}, struct{}]("client")
	if err := peer.Provide(token, Service{"echo": func(_ context.Context, args []json.RawMessage) (any, error) { return args[0], nil }}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			got, err := peer.Call(t.Context(), "test.nested", i)
			var value int
			if err == nil {
				err = json.Unmarshal(got, &value)
			}
			if err != nil || value != i {
				t.Errorf("nested %d: %s %v", i, got, err)
			}
		})
	}
	wg.Wait()
	if got := peer.Announced(); !reflect.DeepEqual(got, []string{"test"}) {
		t.Fatal(got)
	}
	for _, tt := range []struct{ method, want string }{{"test.fail", "broken"}, {"test.absent", "Unknown method: test.absent"}} {
		if _, err := peer.Call(t.Context(), tt.method); err == nil || err.Error() != tt.want {
			t.Fatalf("%s: %v", tt.method, err)
		}
	}
	if got, err := peer.Call(t.Context(), "test.void"); err != nil || string(got) != "null" {
		t.Fatalf("undefined result: %s %v", got, err)
	}
	if got, err := peer.Call(t.Context(), "away.method", true); err != nil || string(got) != `{"method":"away.method","args":[true]}` {
		t.Fatalf("forward: %s %v", got, err)
	}
	type observed struct {
		payload string
		to      *string
	}
	events := make(chan observed, 4)
	peer.OnEvent(func(service string, payload json.RawMessage, to *string) {
		if service != "test" {
			t.Errorf("event service %q", service)
		}
		events <- observed{string(payload), to}
	})
	if _, err := peer.Call(t.Context(), "test.emit", ""); err != nil {
		t.Fatal(err)
	}
	first, second := receive(t, events), receive(t, events)
	if first.payload != `{"type":"broadcast"}` || first.to != nil || second.payload != `{"type":"addressed"}` || second.to == nil || *second.to != "" {
		t.Fatalf("events %+v %+v", first, second)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := peer.Call(ctx, "test.wait", "turn"); result <- err }()
	if got := receive(t, events); got.payload != `{"type":"started","label":"turn"}` {
		t.Fatal(got)
	}
	cancel()
	if err := receive(t, result); err == nil || err.Error() != "Call cancelled" {
		t.Fatal(err)
	}
	if got := receive(t, events); got.payload != `{"type":"cancelled","reason":"Cancelled by caller"}` {
		t.Fatal(got)
	}
	// A result after abandonment is ignored, and the next call still correlates correctly.
	if got, err := peer.Call(t.Context(), "test.echo", "still alive"); err != nil || string(got) != `"still alive"` {
		t.Fatalf("late result: %s %v", got, err)
	}
}
