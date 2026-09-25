package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithToolCallHandler wraps a typed tool_call handler into the
// type-erased HandlerFn signature and returns a fake extension that
// registers it.
func extWithToolCallHandler(path string, fn func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["tool_call"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			event, _ := args[0].(extension.ToolCallEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(event, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

// extWithToolCallErr produces an extension whose tool_call handler returns
// the given error (no result).
func extWithToolCallErr(path string, err error) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["tool_call"] = []extension.HandlerFn{
		func(args ...any) (any, error) { return nil, err },
	}
	return ext
}

// extWithToolResultHandler wraps a typed tool_result handler.
func extWithToolResultHandler(path string, fn func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["tool_result"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			event, _ := args[0].(extension.ToolResultEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(event, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

// ─── EmitToolCall ─────────────────────────────────────────────────────────

func TestEmitToolCall_NoHandlersReturnsNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil (no handlers)", got)
	}
}

func TestEmitToolCall_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("test invalidation")
	_, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

// TestEmitToolCall_HandlerReceivesTypedEvent: the typed Bash event
// arrives at the handler with concrete type, exercising 's sealed
// sum-type work end-to-end.
func TestEmitToolCall_HandlerReceivesTypedEvent(t *testing.T) {
	var gotBash atomic.Bool
	exts := []extension.Extension{
		extWithToolCallHandler("/ext/a", func(e extension.ToolCallEvent, _ context.Context) *extension.ToolCallEventResult {
			if _, ok := e.(extension.BashToolCallEvent); ok {
				gotBash.Store(true)
			}
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")

	if _, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !gotBash.Load() {
		t.Errorf("handler did not receive a BashToolCallEvent (typed dispatch failure)")
	}
}

// TestEmitToolCall_BlockShortCircuitsRemainingHandlers: when a handler
// returns Block=true, no later handler runs.
//
// upstream: runner.ts:769-773 (`if (result.block) return result;`)
func TestEmitToolCall_BlockShortCircuitsRemainingHandlers(t *testing.T) {
	var first, second, third atomic.Int32
	exts := []extension.Extension{
		extWithToolCallHandler("/ext/a", func(e extension.ToolCallEvent, _ context.Context) *extension.ToolCallEventResult {
			first.Add(1)
			return nil
		}),
		extWithToolCallHandler("/ext/b", func(e extension.ToolCallEvent, _ context.Context) *extension.ToolCallEventResult {
			second.Add(1)
			return &extension.ToolCallEventResult{Block: true, Reason: "denied"}
		}),
		extWithToolCallHandler("/ext/c", func(e extension.ToolCallEvent, _ context.Context) *extension.ToolCallEventResult {
			third.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")

	got, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || !got.Block {
		t.Fatalf("got = %+v, want Block=true", got)
	}
	if got.Reason != "denied" {
		t.Errorf("Reason = %q, want denied", got.Reason)
	}
	if first.Load() != 1 {
		t.Errorf("first handler fired %d times, want 1", first.Load())
	}
	if second.Load() != 1 {
		t.Errorf("second handler fired %d times, want 1", second.Load())
	}
	if third.Load() != 0 {
		t.Errorf("third handler fired %d times, want 0 (block short-circuits)", third.Load())
	}
}

// TestEmitToolCall_NonBlockResultIsReturnedAfterAllHandlersRun:
// non-block results don't short-circuit; the LAST non-nil result wins.
//
// upstream: runner.ts:766-773 (`result = handlerResult` always; only block returns early)
func TestEmitToolCall_NonBlockResultIsReturnedAfterAllHandlersRun(t *testing.T) {
	exts := []extension.Extension{
		extWithToolCallHandler("/ext/a", func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult {
			return &extension.ToolCallEventResult{Reason: "from-a"}
		}),
		extWithToolCallHandler("/ext/b", func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult {
			return &extension.ToolCallEventResult{Reason: "from-b"}
		}),
	}
	r := inproc.NewRunner(exts, ".")

	got, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatal("got = nil, want last non-nil result")
		return
	}
	if got.Reason != "from-b" {
		t.Errorf("Reason = %q, want from-b (last result wins when no block)", got.Reason)
	}
}

func TestEmitToolCall_HandlerErrorStopsDispatch(t *testing.T) {
	var subsequentRan atomic.Int32
	wantErr := errors.New("intentional")
	exts := []extension.Extension{
		extWithToolCallErr("/ext/bad", wantErr),
		extWithToolCallHandler("/ext/good", func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult {
			subsequentRan.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")

	var captured atomic.Int32
	r.AddErrorListener(func(*extension.ExtensionError) {
		captured.Add(1)
	})

	result, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if result != nil {
		t.Errorf("result = %#v, want nil", result)
	}
	if captured.Load() != 0 {
		t.Errorf("error listeners fired %d times, want 0", captured.Load())
	}
	if subsequentRan.Load() != 0 {
		t.Errorf("subsequent handler fired %d times, want 0", subsequentRan.Load())
	}
}

// TestEmitToolCall_NilEventIsNoOp: defensive nil handling.
func TestEmitToolCall_NilEventIsNoOp(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitToolCall(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

// ─── EmitToolResult ───────────────────────────────────────────────────────

func TestEmitToolResult_NoHandlersReturnsNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{})
	if err != nil || got != nil {
		t.Errorf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

func TestEmitToolResult_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{})
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

// TestEmitToolResult_HandlerThatReturnsNilDoesNotMarkModified: a
// handler returning nil leaves the chain untouched; the runner returns
// nil overall (no modification).
//
// upstream: runner.ts:712-716 (`if (!handlerResult) continue`)
func TestEmitToolResult_HandlerThatReturnsNilDoesNotMarkModified(t *testing.T) {
	exts := []extension.Extension{
		extWithToolResultHandler("/ext/a", func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult {
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil (no modification)", got)
	}
}

// TestEmitToolResult_MutationChainCompoundsAcrossHandlers: handler A
// modifies content, handler B sees A's content and modifies details.
// Final result combines both. This exercises the most subtle part of
// upstream's emitToolResult.
//
// upstream: runner.ts:716-731 (currentEvent.X = handlerResult.X chain)
func TestEmitToolResult_MutationChainCompoundsAcrossHandlers(t *testing.T) {
	exts := []extension.Extension{
		extWithToolResultHandler("/ext/a", func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult {
			return &extension.ToolResultEventResult{
				Content: []any{"redacted-by-a"},
			}
		}),
		extWithToolResultHandler("/ext/b", func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult {
			return &extension.ToolResultEventResult{
				Details: map[string]string{"annotated": "by-b"},
			}
		}),
	}
	r := inproc.NewRunner(exts, ".")

	got, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{
			Type:    "tool_result",
			Content: []any{"original"},
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatal("got = nil, want combined modification")
		return
	}
	if len(got.Content) != 1 || got.Content[0] != "redacted-by-a" {
		t.Errorf("Content = %v, want [redacted-by-a]", got.Content)
	}
	gotDetails, _ := got.Details.(map[string]string)
	if gotDetails["annotated"] != "by-b" {
		t.Errorf("Details = %v, want {annotated:by-b}", got.Details)
	}
}

// TestEmitToolResult_IsErrorPropagates: handler can flip IsError;
// modified flag set; result reflects the flip.
func TestEmitToolResult_IsErrorPropagates(t *testing.T) {
	exts := []extension.Extension{
		extWithToolResultHandler("/ext/a", func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult {
			return &extension.ToolResultEventResult{IsError: new(true)}
		}),
	}
	r := inproc.NewRunner(exts, ".")

	got, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{IsError: false},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.IsError == nil || !*got.IsError {
		t.Errorf("got = %+v, want IsError=true", got)
	}
}

// TestEmitToolResult_HandlerErrorRoutesViaEmitErrorAndContinues:
// upstream EXPLICITLY catches throws here (runner.ts:733-740) and
// continues. pig mirrors via emitError.
func TestEmitToolResult_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subsequentRan atomic.Int32
	exts := []extension.Extension{
		{
			Path: "/ext/bad",
			Handlers: map[string][]extension.HandlerFn{
				"tool_result": {
					func(args ...any) (any, error) { return nil, errors.New("blew up") },
				},
			},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithToolResultHandler("/ext/good", func(extension.ToolResultEvent, context.Context) *extension.ToolResultEventResult {
			subsequentRan.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")

	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "tool_result" && e.ExtensionPath == "/ext/bad" {
			captured.Add(1)
		}
	})

	if _, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if captured.Load() != 1 {
		t.Errorf("error listener captured %d, want 1", captured.Load())
	}
	if subsequentRan.Load() != 1 {
		t.Errorf("subsequent handler fired %d times, want 1 (chain must continue)", subsequentRan.Load())
	}
}

// TestEmitToolCall_DispatchOrderMatchesLoadOrder: extensions registered
// in order [a, b, c] dispatch in order [a, b, c]. Locks the contract
// that load order = dispatch order (no priority sorting).
//
// upstream: runner.ts:761 (`for (const ext of this.extensions)` \u2014 plain
// iteration order)
func TestEmitToolCall_DispatchOrderMatchesLoadOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex
	mk := func(tag string) extension.Extension {
		return extWithToolCallHandler("/ext/"+tag, func(extension.ToolCallEvent, context.Context) *extension.ToolCallEventResult {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return nil
		})
	}
	r := inproc.NewRunner([]extension.Extension{mk("a"), mk("b"), mk("c")}, ".")

	if _, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{}); err != nil {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("dispatch order = %v, want [a b c]", order)
	}
}

// TestEmitToolCall_HandlerCanReadExtensionContextFromGoContext: the
// dispatch attaches an extension.Context to the context.Context passed
// to handlers, retrievable via FromContext.
func TestEmitToolCall_HandlerCanReadExtensionContextFromGoContext(t *testing.T) {
	var gotExtCtx atomic.Bool
	exts := []extension.Extension{
		extWithToolCallHandler("/ext/a", func(_ extension.ToolCallEvent, ctx context.Context) *extension.ToolCallEventResult {
			if extension.FromContext(ctx) != nil {
				gotExtCtx.Store(true)
			}
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if !gotExtCtx.Load() {
		t.Errorf("handler did not see extension.Context attached to context.Context")
	}
}

// TestEmitToolCall_HandlerReturningWrongTypeRoutesViaEmitError:
// defensive \u2014 if a handler returns a Go value that's not
// *ToolCallEventResult, the runner surfaces this via emitError instead
// of crashing.
func TestEmitToolCall_HandlerReturningWrongTypeRoutesViaEmitError(t *testing.T) {
	exts := []extension.Extension{
		{
			Path: "/ext/buggy",
			Handlers: map[string][]extension.HandlerFn{
				"tool_call": {
					func(args ...any) (any, error) { return "not a result", nil },
				},
			},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(*extension.ExtensionError) { captured.Add(1) })

	got, err := r.EmitToolCall(context.Background(), extension.BashToolCallEvent{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil (bogus value should not become result)", got)
	}
	if captured.Load() != 1 {
		t.Errorf("emitError fired %d times, want 1", captured.Load())
	}
}

// TestEmitToolResult_ContentOnlyKeepsIsError: upstream applies isError only
// when a handler returns it (`handlerResult.isError !== undefined`), so a
// redaction handler that rewrites only content keeps a failed call failed.
func TestEmitToolResult_ContentOnlyKeepsIsError(t *testing.T) {
	ext := newFakeExtension("/redact")
	ext.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"[redacted]"}]}`), nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	got, err := r.EmitToolResult(context.Background(), extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", IsError: true},
		ToolName:            "bash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.IsError == nil || !*got.IsError {
		t.Fatalf("got = %+v, want the failed call to stay IsError=true", got)
	}
}

// TestEmitToolResult_UsageChains: upstream chains `usage` like the other
// fields (runner.ts emitToolResult `handlerResult.usage !== undefined`).
func TestEmitToolResult_UsageChains(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"usage":{"input":1,"output":2}}`), nil
	}}
	second := newFakeExtension("/second")
	second.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"x"}]}`), nil
	}}
	r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	got, err := r.EmitToolResult(context.Background(), extension.CustomToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result"}, ToolName: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := got.Usage.(map[string]any)
	if got == nil || usage["output"] != float64(2) {
		t.Fatalf("got = %+v, want the first handler's usage carried through the chain", got)
	}
}

// TestEmitToolResult_NextHandlerSeesPredecessorValues: upstream writes each
// handler's content/details/isError/usage into currentEvent, so a later
// handler observes and can build on them (runner.ts emitToolResult).
func TestEmitToolResult_NextHandlerSeesPredecessorValues(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"step1"}],"details":{"n":1},"isError":true,"usage":{"output":7}}`), nil
	}}
	var seen extension.CustomToolResultEvent
	second := newFakeExtension("/second")
	second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
		seen = args[0].(extension.CustomToolResultEvent)
		text := seen.Content[0].(map[string]any)["text"].(string)
		return &extension.ToolResultEventResult{Content: []any{map[string]any{"type": "text", "text": text + "+step2"}}}, nil
	}}
	r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	got, err := r.EmitToolResult(context.Background(), extension.CustomToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", Content: []any{map[string]any{"type": "text", "text": "orig"}}, Usage: map[string]any{"output": float64(1)}},
		ToolName:            "probe",
	})
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := seen.Usage.(map[string]any)
	details, _ := seen.Details.(map[string]any)
	if !seen.IsError || usage["output"] != float64(7) || details["n"] != float64(1) {
		t.Fatalf("second handler saw isError=%v usage=%v details=%v; want the first handler's values", seen.IsError, seen.Usage, seen.Details)
	}
	if text := got.Content[0].(map[string]any)["text"]; text != "step1+step2" {
		t.Fatalf("final content = %v; want the second handler to build on the first", text)
	}
}

// A typed built-in event keeps its variant; replaced details are converted.
func TestEmitToolResult_TypedDetailsChain(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"details":{"fullOutputPath":"/tmp/out"}}`), nil
	}}
	var seen *extension.BashToolDetails
	second := newFakeExtension("/second")
	second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
		seen = args[0].(extension.BashToolResultEvent).Details
		return nil, nil
	}}
	r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	if _, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result"}, ToolName: "bash"}); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.FullOutputPath == "" {
		t.Fatalf("second handler saw bash details %+v; want the first handler's", seen)
	}
}

// A replacement a built-in variant's typed details cannot hold exactly (extra
// fields, another shape) reaches the next handler as returned, with the same
// toolName; upstream assigns handlerResult.details to currentEvent as is.
func TestEmitToolResult_TypedVariantPreservesReplacementDetails(t *testing.T) {
	for name, replacement := range map[string]any{
		"extra fields":      map[string]any{"auditTag": "redacted", "fullOutputPath": "/tmp/out"},
		"incompatible type": map[string]any{"truncation": "not-an-object"},
		"not an object":     []any{"a", float64(1)},
	} {
		t.Run(name, func(t *testing.T) {
			first := newFakeExtension("/first")
			first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
				return &extension.ToolResultEventResult{Details: replacement}, nil
			}}
			var seen map[string]any
			var seenEvent extension.ToolResultEvent
			second := newFakeExtension("/second")
			second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
				seenEvent = args[0].(extension.ToolResultEvent)
				raw, _ := json.Marshal(args[0])
				_ = json.Unmarshal(raw, &seen)
				return nil, nil
			}}
			r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
			got, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result"}, ToolName: "bash"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(seen["details"], replacement) || seen["toolName"] != "bash" {
				t.Fatalf("later handler saw details=%#v toolName=%v (%T); want the exact replacement", seen["details"], seen["toolName"], seenEvent)
			}
			if !reflect.DeepEqual(got.Details, replacement) {
				t.Fatalf("combined details = %#v", got.Details)
			}
		})
	}
}

// A replacement that fits the typed details exactly keeps the typed variant.
func TestEmitToolResult_TypedVariantKeptWhenLossless(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return &extension.ToolResultEventResult{Details: map[string]any{"fullOutputPath": "/tmp/out"}}, nil
	}}
	var seen extension.ToolResultEvent
	second := newFakeExtension("/second")
	second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
		seen = args[0].(extension.ToolResultEvent)
		return nil, nil
	}}
	r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	if _, err := r.EmitToolResult(context.Background(), extension.BashToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result"}, ToolName: "bash"}); err != nil {
		t.Fatal(err)
	}
	bash, ok := seen.(extension.BashToolResultEvent)
	if !ok || bash.Details == nil || bash.Details.FullOutputPath != "/tmp/out" {
		t.Fatalf("later handler saw %#v; want a typed bash event with the details", seen)
	}
}

// A PowerShell result chains like its bash counterpart: handlers see the
// original content, and replacement details that fit PowerShellToolDetails
// (upstream's alias of BashToolDetails) keep the typed variant.
func TestEmitToolResult_PowerShellVariantChainsContentAndDetails(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["tool_result"] = []extension.HandlerFn{func(...any) (any, error) {
		return &extension.ToolResultEventResult{Details: map[string]any{"fullOutputPath": `C:\Temp\ps.log`}}, nil
	}}
	var seen extension.ToolResultEvent
	second := newFakeExtension("/second")
	second.Handlers["tool_result"] = []extension.HandlerFn{func(args ...any) (any, error) {
		seen = args[0].(extension.ToolResultEvent)
		return nil, nil
	}}
	r := inproc.NewRunner([]extension.Extension{first, second}, t.TempDir())
	event := extension.PowerShellToolResultEvent{
		ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", Content: []any{map[string]any{"type": "text", "text": "Get-Date output"}}},
		ToolName:            "powershell",
	}
	got, err := r.EmitToolResult(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	ps, ok := seen.(extension.PowerShellToolResultEvent)
	if !ok || ps.Details == nil || ps.Details.FullOutputPath != `C:\Temp\ps.log` || len(ps.Content) != 1 {
		t.Fatalf("later handler saw %#v; want a typed powershell event with the original content and replaced details", seen)
	}
	if got == nil || len(got.Content) != 1 {
		t.Fatalf("combined result = %#v; want the original content carried through", got)
	}
}
