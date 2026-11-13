package inproc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type orderedLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *orderedLog) add(entry string) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

func (l *orderedLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.entries...)
}

// runner.ts emit family (emit 1103, emitToolCall 1256, emitToolResult 1197, emitMessageEnd 1158, emitContext 1312, emitBeforeProviderRequest 1375,
// emitBeforeProviderHeaders 1406, emitUserBash 1276, emitInput 1532): every handler is awaited in turn, so a handler starts only after the
// previous one has finished. The first extension's handler blocks; the second extension's handler must not start until it is released.
func TestRunnerEmitsAwaitEachHandlerBeforeStartingTheNext(t *testing.T) {
	ctx := context.Background()
	emits := map[string]func(r *Runner) error{
		"tool_call": func(r *Runner) error {
			_, err := r.EmitToolCall(ctx, extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "bash"})
			return err
		},
		"tool_result": func(r *Runner) error {
			_, err := r.EmitToolResult(ctx, extension.CustomToolResultEvent{ToolName: "t", Type: "tool_result", ToolCallID: "c1", Input: map[string]any{}})
			return err
		},
		"message_end": func(r *Runner) error {
			_, err := r.EmitMessageEnd(ctx, extension.MessageEndEvent{Type: "message_end", Message: assistantText("m")})
			return err
		},
		"context": func(r *Runner) error {
			_, err := r.EmitContext(ctx, []extension.AgentMessage{customMessage("m")})
			return err
		},
		"before_provider_request": func(r *Runner) error {
			_, err := r.EmitBeforeProviderRequest(ctx, "payload")
			return err
		},
		"before_provider_headers": func(r *Runner) error {
			_, err := r.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{})
			return err
		},
		"user_bash": func(r *Runner) error {
			_, err := r.EmitUserBash(ctx, extension.UserBashEvent{Type: "user_bash", Command: "ls", Cwd: "/w"})
			return err
		},
		"input": func(r *Runner) error {
			_, err := r.EmitInput(ctx, "hello", nil, extension.InputSource("interactive"), "")
			return err
		},
		"session_start": func(r *Runner) error {
			_, err := r.Emit(ctx, extension.SessionStartEvent{Type: "session_start", Reason: "startup"})
			return err
		},
	}
	for event, emit := range emits {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log := &orderedLog{}
				release := make(chan struct{})
				letGo := sync.OnceFunc(func() { close(release) })
				t.Cleanup(letGo)
				first := extension.Extension{Path: "/ext/first.ts", Handlers: map[string][]extension.HandlerFn{event: {func(...any) (any, error) {
					log.add("first start")
					<-release
					log.add("first end")
					return nil, nil
				}}}}
				second := extension.Extension{Path: "/ext/second.ts", Handlers: map[string][]extension.HandlerFn{event: {func(...any) (any, error) {
					log.add("second")
					return nil, nil
				}}}}
				r := NewRunner([]extension.Extension{first, second}, t.TempDir())
				r.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("error listener: %+v", err) })
				done := make(chan error, 1)
				go func() { done <- emit(r) }()
				synctest.Wait()
				if got := log.snapshot(); !reflect.DeepEqual(got, []string{"first start"}) {
					t.Fatalf("while the first handler runs: %q, want only its start", got)
				}
				letGo()
				synctest.Wait()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if got := log.snapshot(); !reflect.DeepEqual(got, []string{"first start", "first end", "second"}) {
					t.Fatalf("order = %q", got)
				}
			})
		})
	}
}

// runner.ts:1158-1195 emitMessageEnd: each handler sees the message its predecessors produced, a handler that throws is reported and
// the chain goes on with the last good message.
func TestRunnerMessageEndChainsReplacementsPastAFailingHandler(t *testing.T) {
	var seen []extension.AgentMessage
	replace := func(text string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			seen = append(seen, args[0].(extension.MessageEndEvent).Message)
			message := assistantText(text)
			return &extension.MessageEndEventResult{Message: &message}, nil
		}
	}
	failing := func(args ...any) (any, error) {
		seen = append(seen, args[0].(extension.MessageEndEvent).Message)
		return nil, errors.New("handler failed")
	}
	exts := []extension.Extension{
		{Path: "/ext/a.ts", Handlers: map[string][]extension.HandlerFn{"message_end": {replace("from a")}}},
		{Path: "/ext/b.ts", Handlers: map[string][]extension.HandlerFn{"message_end": {failing}}},
		{Path: "/ext/c.ts", Handlers: map[string][]extension.HandlerFn{"message_end": {replace("from c")}}},
	}
	r := NewRunner(exts, t.TempDir())
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.ExtensionPath+": "+err.Error) })
	result, err := r.EmitMessageEnd(context.Background(), extension.MessageEndEvent{Type: "message_end", Message: assistantText("original")})
	if err != nil || result == nil || !reflect.DeepEqual(*result, assistantText("from c")) {
		t.Fatalf("result = %v, %v", result, err)
	}
	want := []extension.AgentMessage{assistantText("original"), assistantText("from a"), assistantText("from a")}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("handlers saw %v, want %v", seen, want)
	}
	if !reflect.DeepEqual(reported, []string{"/ext/b.ts: handler failed"}) {
		t.Fatalf("reported = %q", reported)
	}
}

// runner.ts:1406-1432 emitBeforeProviderHeaders: handlers mutate one header map in turn, so a later handler sees an earlier one's
// header, and a handler that throws is reported without ending the chain.
func TestRunnerBeforeProviderHeadersChainsMutationsPastAFailingHandler(t *testing.T) {
	value := func(s string) *string { return &s }
	var sawByThird []string
	exts := []extension.Extension{
		{Path: "/ext/a.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
			args[0].(extension.BeforeProviderHeadersEvent).Headers["x-a"] = value("1")
			return nil, nil
		}}}},
		{Path: "/ext/b.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(...any) (any, error) {
			return nil, errors.New("handler failed")
		}}}},
		{Path: "/ext/c.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
			headers := args[0].(extension.BeforeProviderHeadersEvent).Headers
			for key, v := range headers {
				sawByThird = append(sawByThird, fmt.Sprintf("%s=%s", key, *v))
			}
			headers["x-c"] = value("2")
			return nil, nil
		}}}},
	}
	r := NewRunner(exts, t.TempDir())
	var reported []string
	r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	headers, err := r.EmitBeforeProviderHeaders(context.Background(), extension.ProviderHeaders{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sawByThird, []string{"x-a=1"}) || len(headers) != 2 || *headers["x-c"] != "2" {
		t.Fatalf("third handler saw %q, final headers %v", sawByThird, headers)
	}
	if !reflect.DeepEqual(reported, []string{"handler failed"}) {
		t.Fatalf("reported = %q", reported)
	}
}
