package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithContextHandler wraps a typed context handler.
func extWithContextHandler(path string, fn func(extension.ContextEvent, context.Context) *extension.ContextEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["context"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.ContextEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(ev, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

func extWithBPRHandler(path string, fn func(extension.BeforeProviderRequestEvent, context.Context) any) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["before_provider_request"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.BeforeProviderRequestEvent)
			ctx, _ := args[1].(context.Context)
			return fn(ev, ctx), nil
		},
	}
	return ext
}

func extWithUserBashHandler(path string, fn func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["user_bash"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.UserBashEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(ev, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

// ─── EmitContext ──────────────────────────────────────────────────────────

func TestEmitContext_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitContext(context.Background(), nil)
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

// TestEmitContext_NoHandlersReturnsCloneOfInput: empty runner returns a
// (shallow) clone of the input messages, not the input slice itself.
func TestEmitContext_NoHandlersReturnsCloneOfInput(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	in := []extension.AgentMessage{"a", "b", "c"}
	got, err := r.EmitContext(context.Background(), in)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("got = %v, want [a b c]", got)
	}
	// Verify the runner returned a clone, not the input slice. Mutate
	// got[0] and confirm in[0] is unchanged.
	got[0] = "MUTATED"
	if in[0] != "a" {
		t.Errorf("input was mutated through returned slice; want defensive clone")
	}
}

func TestEmitContextDeepClonesNestedMessageData(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	nested := map[string]any{"content": []any{map[string]any{"text": "original"}}}
	got, err := r.EmitContext(context.Background(), []extension.AgentMessage{nested})
	if err != nil {
		t.Fatal(err)
	}
	gotContent := got[0].(map[string]any)["content"].([]any)
	gotContent[0].(map[string]any)["text"] = "mutated"
	originalContent := nested["content"].([]any)
	if text := originalContent[0].(map[string]any)["text"]; text != "original" {
		t.Fatalf("nested input was mutated through context clone: %v", text)
	}
}

// TestEmitContext_HandlerCanRewriteMessages: a handler returning a
// new Messages slice replaces the chained value.
func TestEmitContext_HandlerCanRewriteMessages(t *testing.T) {
	exts := []extension.Extension{
		extWithContextHandler("/ext/a", func(extension.ContextEvent, context.Context) *extension.ContextEventResult {
			return &extension.ContextEventResult{Messages: []extension.AgentMessage{"replaced"}}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitContext(context.Background(), []extension.AgentMessage{"original"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 1 || got[0] != "replaced" {
		t.Errorf("got = %v, want [replaced]", got)
	}
}

// TestEmitContext_ChainCompounds: handler A appends "+a", handler B
// sees A's output and appends "+b". Final result reflects both.
func TestEmitContext_ChainCompounds(t *testing.T) {
	exts := []extension.Extension{
		extWithContextHandler("/ext/a", func(ev extension.ContextEvent, _ context.Context) *extension.ContextEventResult {
			next := append([]extension.AgentMessage(nil), ev.Messages...)
			next = append(next, "+a")
			return &extension.ContextEventResult{Messages: next}
		}),
		extWithContextHandler("/ext/b", func(ev extension.ContextEvent, _ context.Context) *extension.ContextEventResult {
			next := append([]extension.AgentMessage(nil), ev.Messages...)
			next = append(next, "+b")
			return &extension.ContextEventResult{Messages: next}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitContext(context.Background(), []extension.AgentMessage{"start"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 3 || got[0] != "start" || got[1] != "+a" || got[2] != "+b" {
		t.Errorf("got = %v, want [start +a +b] (chain must compound)", got)
	}
}

// TestEmitContext_NilMessagesInResultIsNoOp: a handler returning a
// result with nil Messages does not modify the chain.
func TestEmitContext_NilMessagesInResultIsNoOp(t *testing.T) {
	exts := []extension.Extension{
		extWithContextHandler("/ext/a", func(extension.ContextEvent, context.Context) *extension.ContextEventResult {
			return &extension.ContextEventResult{Messages: nil}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, _ := r.EmitContext(context.Background(), []extension.AgentMessage{"keep"})
	if len(got) != 1 || got[0] != "keep" {
		t.Errorf("got = %v, want [keep] (nil Messages is no-op)", got)
	}
}

func TestEmitContext_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subseqRan atomic.Int32
	exts := []extension.Extension{
		{
			Path:      "/ext/bad",
			Handlers:  map[string][]extension.HandlerFn{"context": {func(args ...any) (any, error) { return nil, errors.New("kaboom") }}},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithContextHandler("/ext/good", func(extension.ContextEvent, context.Context) *extension.ContextEventResult {
			subseqRan.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "context" {
			captured.Add(1)
		}
	})
	if _, err := r.EmitContext(context.Background(), nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured.Load() != 1 {
		t.Errorf("captured %d, want 1", captured.Load())
	}
	if subseqRan.Load() != 1 {
		t.Errorf("subsequent fired %d, want 1", subseqRan.Load())
	}
}

// ─── EmitBeforeProviderRequest ────────────────────────────────────────────

func TestEmitBPR_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitBeforeProviderRequest(context.Background(), nil)
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

func TestEmitBPR_NoHandlersReturnsInputUnchanged(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	in := map[string]any{"k": "v"}
	got, err := r.EmitBeforeProviderRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	gotMap, ok := got.(map[string]any)
	if !ok || gotMap["k"] != "v" {
		t.Errorf("got = %v, want input unchanged", got)
	}
}

// TestEmitBPR_ChainCompounds: handler A wraps with "{a:...}", handler B
// wraps with "{b:...}". Final payload reflects both wraps.
func TestEmitBPR_ChainCompounds(t *testing.T) {
	exts := []extension.Extension{
		extWithBPRHandler("/ext/a", func(ev extension.BeforeProviderRequestEvent, _ context.Context) any {
			return map[string]any{"a": ev.Payload}
		}),
		extWithBPRHandler("/ext/b", func(ev extension.BeforeProviderRequestEvent, _ context.Context) any {
			return map[string]any{"b": ev.Payload}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitBeforeProviderRequest(context.Background(), "init")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	outer, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("outer not map: %T", got)
	}
	inner, ok := outer["b"].(map[string]any)
	if !ok {
		t.Fatalf("inner not map: %T", outer["b"])
	}
	if inner["a"] != "init" {
		t.Errorf("inner[a] = %v, want init (chain must compound)", inner["a"])
	}
}

// TestEmitBPR_NilHandlerResultDoesNotChangeChain: per upstream
// `if (handlerResult !== undefined)`, a nil result leaves payload as-is.
func TestEmitBPR_NilHandlerResultDoesNotChangeChain(t *testing.T) {
	exts := []extension.Extension{
		extWithBPRHandler("/ext/a", func(extension.BeforeProviderRequestEvent, context.Context) any { return nil }),
	}
	r := inproc.NewRunner(exts, ".")
	got, _ := r.EmitBeforeProviderRequest(context.Background(), "keep")
	if got != "keep" {
		t.Errorf("got = %v, want keep (nil result is no-op)", got)
	}
}

// ─── EmitUserBash ─────────────────────────────────────────────────────────

func TestEmitUserBash_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{})
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

func TestEmitUserBash_NoHandlersReturnsNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash"})
	if err != nil || got != nil {
		t.Errorf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

func validUserBashResult() *extension.UserBashEventResult {
	return &extension.UserBashEventResult{Result: map[string]any{"output": "handled", "exitCode": 0.0, "cancelled": false, "truncated": false}}
}

// TestEmitUserBash_FirstValidResultWinsAndShortCircuits: opposite direction
// from EmitToolCall (last-wins-unless-block). Locks the contract.
//
// upstream: runner.ts emitUserBash (`if (handlerResult === undefined) continue; ... return handlerResult`)
func TestEmitUserBash_FirstValidResultWinsAndShortCircuits(t *testing.T) {
	var thirdFired atomic.Int32
	exts := []extension.Extension{
		extWithUserBashHandler("/ext/a", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult { return nil }),
		extWithUserBashHandler("/ext/b", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
			return validUserBashResult()
		}),
		extWithUserBashHandler("/ext/c", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
			thirdFired.Add(1)
			return validUserBashResult()
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.Result == nil {
		t.Errorf("got = %+v, want the first valid result (from /ext/b)", got)
	}
	if thirdFired.Load() != 0 {
		t.Errorf("third fired %d, want 0 (first-wins must short-circuit)", thirdFired.Load())
	}
}

// Ports upstream extensions-runner.test.ts: a throwing user_bash handler is
// reported through onError and the rejection propagates, so callers fail
// closed instead of running the command locally; later handlers do not run.
func TestEmitUserBash_HandlerErrorIsReportedAndFailsClosed(t *testing.T) {
	var subseq atomic.Int32
	exts := []extension.Extension{
		{
			Path:      "/ext/bad",
			Handlers:  map[string][]extension.HandlerFn{"user_bash": {func(args ...any) (any, error) { return nil, errors.New("Routing failed") }}},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithUserBashHandler("/ext/good", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
			subseq.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured []extension.ExtensionError
	r.AddErrorListener(func(e *extension.ExtensionError) { captured = append(captured, *e) })
	if _, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{}); err == nil || err.Error() != "Routing failed" {
		t.Fatalf("err = %v, want the handler's error", err)
	}
	if len(captured) != 1 || captured[0].Event != "user_bash" || captured[0].Error != "Routing failed" || captured[0].ExtensionPath != "/ext/bad" {
		t.Errorf("captured = %+v", captured)
	}
	if subseq.Load() != 0 {
		t.Errorf("subsequent fired %d, want 0", subseq.Load())
	}
}

// Ports upstream "fails closed when a user_bash handler returns %s" (#9068).
// Handler results cross the subprocess wire as JSON, so each case is the JSON
// an extension would send; operations can never carry an exec function there.
func TestEmitUserBash_InvalidResultFailsClosed(t *testing.T) {
	for name, raw := range map[string]string{
		"an empty object":          `{}`,
		"null operations":          `{"operations":null}`,
		"operations without exec":  `{"operations":{}}`,
		"a null result":            `{"result":null}`,
		"an incomplete result":     `{"result":{"output":"handled"}}`,
		"operations and a result":  `{"operations":{"exec":"fn"},"result":{"output":"handled","exitCode":0,"cancelled":false,"truncated":false}}`,
		"a result with bad fields": `{"result":{"output":"handled","exitCode":"0","cancelled":false,"truncated":false}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ext := newFakeExtension("/ext/invalid")
			ext.Handlers["user_bash"] = []extension.HandlerFn{func(...any) (any, error) { return json.RawMessage(raw), nil }}
			r := inproc.NewRunner([]extension.Extension{ext}, ".")
			var captured []extension.ExtensionError
			r.AddErrorListener(func(e *extension.ExtensionError) { captured = append(captured, *e) })
			_, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: "pwd"})
			if err == nil || !strings.Contains(err.Error(), "Invalid user_bash handler result") {
				t.Fatalf("err = %v, want the invalid-result error", err)
			}
			if len(captured) != 1 || captured[0].Event != "user_bash" || !strings.Contains(captured[0].Error, "Invalid user_bash handler result") {
				t.Fatalf("captured = %+v", captured)
			}
		})
	}
}

// Ports upstream "accepts valid user_bash operations and result overrides"
// for the result form, which is the one a subprocess extension can send.
func TestEmitUserBash_AcceptsValidResultOverride(t *testing.T) {
	ext := newFakeExtension("/ext/valid")
	ext.Handlers["user_bash"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"result":{"output":"handled","exitCode":0,"cancelled":false,"truncated":false}}`), nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	got, err := r.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: "result"})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := got.Result.(map[string]any)
	if result["output"] != "handled" || result["exitCode"] != 0.0 || result["cancelled"] != false || result["truncated"] != false {
		t.Fatalf("result = %#v", got)
	}
}

// A Go handler may return a typed result; it is judged and returned in its
// JSON form.
func TestEmitUserBash_AcceptsTypedGoResult(t *testing.T) {
	type bashResult struct {
		Output    string `json:"output"`
		ExitCode  int    `json:"exitCode"`
		Cancelled bool   `json:"cancelled"`
		Truncated bool   `json:"truncated"`
	}
	exts := []extension.Extension{extWithUserBashHandler("/ext/typed", func(extension.UserBashEvent, context.Context) *extension.UserBashEventResult {
		return &extension.UserBashEventResult{Result: bashResult{Output: "typed", ExitCode: 3}}
	})}
	got, err := inproc.NewRunner(exts, ".").EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash"})
	if err != nil {
		t.Fatal(err)
	}
	if result, _ := got.Result.(map[string]any); result["output"] != "typed" || result["exitCode"] != 3.0 {
		t.Fatalf("result = %#v", got.Result)
	}
}
