package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:57-70. Selection validation runs before any directory discovery, authentication resolution or connection attempt.
func TestOpenClientRuntimeValidationPrecedesDiscovery(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		command ClientCommand
		want    string
	}{
		{"provider requires model", ClientCommand{Provider: new("")}, "Server model provider requires a model"},
		{"explicit connection model", ClientCommand{Connect: &TransportAddress{Transport: "unix", Path: "not-read"}, Model: new("")}, "Model selection is only valid when automatically activating a new server"},
	} {
		t.Run(row.name, func(t *testing.T) {
			directory := t.TempDir() + "/not-created"
			runtime, err := OpenClientRuntime(t.Context(), row.command, OpenClientRuntimeOptions{Directory: &directory})
			if runtime != nil || err == nil || err.Error() != row.want {
				t.Fatalf("open=%v, %v; want %q", runtime, err, row.want)
			}
			if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validation touched discovery directory: %v", err)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/client.ts:9-15. The named Go variants serialize the same closed result shapes, without unrelated zero-value fields.
func TestClientResultJSONShapes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		result     ClientResult
		kind, want string
	}{
		{ClientListResult{Sessions: []services.SessionAddress{}}, "list", `{"kind":"list","sessions":[]}`},
		{ClientAttachedResult{ServerId: "server", SessionId: ""}, "attached", `{"kind":"attached","serverId":"server","sessionId":""}`},
		{ClientPromptedResult{ServerId: "server", SessionId: "session", Text: "answer"}, "prompted", `{"kind":"prompted","serverId":"server","sessionId":"session","text":"answer"}`},
	} {
		data, err := json.Marshal(row.result)
		if err != nil || string(data) != row.want || row.result.Kind() != row.kind {
			t.Fatalf("result=%s, %v; kind=%s; want %s", data, err, row.result.Kind(), row.want)
		}
	}
}

// upstream: packages/coding-agent/src/experimental/client.ts:80-132. Terminal event arrival and callback-tail completion are independent; user-string messages are not decoded as assistant content.
func TestClientEventDeliveryWaitsForBoundaryAndOrderedCallbacks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan struct{}), make(chan struct{})
		releaseOnce := sync.OnceFunc(func() { close(release) })
		var observed []string
		delivery := newClientEventDelivery(func(ctx context.Context, event json.RawMessage) error {
			if ctx != context.Background() {
				t.Error("callback Context changed")
			}
			if len(observed) == 0 {
				close(started)
				<-release
			}
			observed = append(observed, string(event))
			return nil
		})
		t.Cleanup(func() {
			releaseOnce()
			if err := delivery.close(); err != nil {
				t.Error(err)
			}
		})
		events := []string{
			`{"type":"message_end","runId":"run","message":{"role":"user","content":"text"}}`,
			`{"type":"message_end","runId":"run","message":{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"thinking","thinking":"private"},{"type":"text","text":" second"}]}}`,
			`{"type":"run_end","runId":"run"}`,
		}
		for _, event := range events {
			delivery.enqueue(json.RawMessage(event))
		}
		<-started
		if err := delivery.waitBoundary("run"); err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- delivery.close() }()
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("delivery returned before callback completion: %v", err)
		default:
		}
		releaseOnce()
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(observed, events) {
			t.Fatalf("events=%q, want %q", observed, events)
		}
		if got := delivery.completedText["run"]; got != "first second" {
			t.Fatalf("completed text=%q", got)
		}
	})
}

// upstream: packages/coding-agent/src/experimental/client.ts:90-123. A rejected deliveryTail skips later callbacks but does not remove the accepted operation's terminal-boundary wait.
func TestClientEventFailureDoesNotInventTerminalBoundary(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("event callback failed")
		calls := 0
		delivery := newClientEventDelivery(func(context.Context, json.RawMessage) error { calls++; return failure })
		t.Cleanup(func() {
			delivery.enqueue(json.RawMessage(`{"type":"run_suspend","runId":"run"}`))
			if err := delivery.close(); !errors.Is(err, failure) {
				t.Errorf("close=%v", err)
			}
		})
		delivery.enqueue(json.RawMessage(`{"type":"run_start","runId":"run"}`))
		synctest.Wait()
		boundary := make(chan error, 1)
		go func() { boundary <- delivery.waitBoundary("run") }()
		synctest.Wait()
		select {
		case err := <-boundary:
			t.Fatalf("callback rejection bypassed terminal wait: %v", err)
		default:
		}
		delivery.enqueue(json.RawMessage(`{"type":"run_suspend","runId":"run"}`))
		if err := <-boundary; err != nil {
			t.Fatal(err)
		}
		if err := delivery.close(); !errors.Is(err, failure) {
			t.Fatalf("delivery failure=%v", err)
		}
		if calls != 1 {
			t.Fatalf("callbacks after rejected tail=%d, want one", calls)
		}
	})
}

// upstream: packages/coding-agent/src/experimental/client-runtime.ts:106-127. The first disposal settles same-phase resources concurrently in source order; the disposed flag makes subsequent or reentrant calls return immediately.
func TestClientRuntimeDisposalJoinsSourcesAndRetainsErrors(t *testing.T) {
	t.Parallel()
	started := make(chan int, 2)
	release := make(chan struct{})
	failures := []error{errors.New("first source"), errors.New("second source")}
	runtime := &ClientRuntime{}
	for index, failure := range failures {
		runtime.sources = append(runtime.sources, func(ctx context.Context) error {
			if ctx != context.Background() {
				t.Error("cleanup Context changed")
			}
			started <- index
			<-release
			return failure
		})
	}
	closed := make(chan error, 1)
	finished := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	go func() { defer close(finished); closed <- runtime.Dispose() }()
	t.Cleanup(func() { releaseOnce(); <-finished })
	<-started
	<-started
	if err := runtime.Dispose(); err != nil {
		t.Fatalf("repeated disposal=%v, want nil", err)
	}
	releaseOnce()
	first := <-closed
	aggregate, ok := errors.AsType[*services.AggregateError](first)
	if !ok || aggregate.Message != "Failed to dispose experimental client runtime" || !reflect.DeepEqual(aggregate.Errors, []any{failures[0], failures[1]}) {
		t.Fatalf("cleanup=%#v", first)
	}
}
