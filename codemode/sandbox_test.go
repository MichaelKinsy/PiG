package codemode_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/codemode"
)

// Ports packages/codemode/test/sandbox.test.ts (v1.0.1) with its original scripts and expectations. The upstream
// tests use vitest's toMatchObject; a helper here compares the same fields. Go mechanics: the abort signal is a
// context.Context, JSON text stands for `unknown`, and a never-settling tool promise becomes a tool that waits for
// its context, because every goroutine of an execution joins before Execute returns.
//
// Not ported: "reports a missing worker file as a sandbox error" (there is no worker file: the VM runs in-process on
// wazero, docs/specs/builtin-codemode-tool-search.md). TestCorruptWasmModuleIsASandboxError takes its place. Also not
// ported: 0.99.2 "accepts a worker path string" (#10204): the string form of `workerUrl` names the worker entry of a
// Bun compiled executable, and this engine has no worker.

func js(s string) json.RawMessage { return json.RawMessage(s) }

// Base64 of the leading bytes of each format. image() only inspects the signature.
const (
	pngBase64  = "iVBORw0KGgo="
	jpegBase64 = "/9j/4A=="
	gifBase64  = "R0lGODlh"
	webpBase64 = "UklGRgAAAABXRUJQ"
)

var echo = codemode.Tool{Name: "echo", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) { return args, nil }}

func newSandbox(t *testing.T, timeoutMs float64, tools ...codemode.Tool) *codemode.Sandbox {
	t.Helper()
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: tools, TimeoutMs: timeoutMs})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox
}

func run(t *testing.T, sandbox *codemode.Sandbox, code string, options ...codemode.ExecuteOptions) codemode.Result {
	t.Helper()
	var opt codemode.ExecuteOptions
	if len(options) > 0 {
		opt = options[0]
	}
	result, err := sandbox.Execute(t.Context(), code, opt)
	if err != nil {
		t.Fatalf("Execute(%q): %v", code, err)
	}
	return result
}

// receive waits for a value a goroutine of the test sends, so a missing send fails the test instead of hanging it.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the execution")
		panic("unreachable")
	}
}

func sameJSON(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	if want == "undefined" {
		if got != nil {
			t.Fatalf("%s = %s, want undefined", what, got)
		}
		return
	}
	var g, w any
	if got == nil {
		t.Fatalf("%s is undefined, want %s", what, want)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s = %q: %v", what, got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s = %s, want %s", what, got, want)
	}
}

func wantOK(t *testing.T, result codemode.Result, value string) {
	t.Helper()
	if !result.OK {
		t.Fatalf("result failed: %+v", result.Error)
	}
	sameJSON(t, "value", result.Value, value)
}

func wantFailure(t *testing.T, result codemode.Result, kind codemode.ErrorKind) *codemode.Error {
	t.Helper()
	if result.OK || result.Error == nil || result.Error.Kind != kind {
		t.Fatalf("result = %+v (error %+v), want failure of kind %q", result, result.Error, kind)
	}
	return result.Error
}

func TestEmbeddedSourcesMatchTheirRecordedHashes(t *testing.T) {
	// upstream "embedded sources parse as JavaScript": the prelude parses (and runs) in every execution below; the
	// hashes pin the exact upstream bytes that the engine and the prelude were verified against.
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	if got := sum([]byte(codemode.PreludeSource)); got != "c8c292ac0bc913654384ae12ecd759d6de89862acab0ce912f2e733878749880" {
		t.Errorf("prelude sha256 = %s", got)
	}
	if got := sum(codemode.QuickJSWasm()); got != "d4c9375f2b1ca4dc95f72c8aa2982a7a9951ac8011490d79c6582df732b4bbd9" {
		t.Errorf("quickjs.wasm sha256 = %s", got)
	}
	wantOK(t, run(t, newSandbox(t, 10_000), "return 1"), "1")
}

func TestReturnsTheScriptsReturnValueAfterAJSONRoundTrip(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	result := run(t, sandbox, "return { a: 1, b: [true, 'x'] }")
	wantOK(t, result, `{"a":1,"b":[true,"x"]}`)
	if len(result.Output) != 0 || len(result.Calls) != 0 {
		t.Fatalf("output %v calls %v, want none", result.Output, result.Calls)
	}
	wantOK(t, run(t, sandbox, "return 'plain'"), `"plain"`)
	wantOK(t, run(t, sandbox, ""), "undefined")
}

func TestSupportsTopLevelAwait(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000), "const x = await Promise.resolve(41); return x + 1"), "42")
}

func TestCollectsTextImageAndConsoleOutputInOrder(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `
			console.log("hello", 1, { a: 1 });
			text({ json: true });
			text(undefined);
			text(7);
			image("data:image/png;base64,`+pngBase64+`");
			image({ image_url: "data:image/jpeg;base64,`+jpegBase64+`" });
			image({ type: "image", data: "`+gifBase64+`", mimeType: "image/gif" });
			image("data:image/png;base64,`+webpBase64+`");
			image({ type: "image", data: "`+pngBase64+`" });
			console.error(new Error("bad"));
			return null;
		`)
	if !result.OK {
		t.Fatalf("failed: %+v", result.Error)
	}
	want := []codemode.OutputItem{
		{Type: "text", Text: `hello 1 {"a":1}`},
		{Type: "text", Text: `{"json":true}`},
		{Type: "text", Text: "undefined"},
		{Type: "text", Text: "7"},
		{Type: "image", Data: pngBase64, MimeType: "image/png"},
		{Type: "image", Data: jpegBase64, MimeType: "image/jpeg"},
		{Type: "image", Data: gifBase64, MimeType: "image/gif"},
		// The MIME type comes from the data, not from the declared type.
		{Type: "image", Data: webpBase64, MimeType: "image/webp"},
		{Type: "image", Data: pngBase64, MimeType: "image/png"},
	}
	if len(result.Output) != len(want)+1 || !slices.Equal(result.Output[:len(want)], want) {
		t.Fatalf("output = %+v", result.Output)
	}
	last := result.Output[len(want)]
	if last.Type != "text" || !regexp.MustCompile(`^Error: bad`).MatchString(last.Text) {
		t.Fatalf("last output = %+v", last)
	}
}

func TestRejectsInvalidTextAndImageArguments(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `
			const errors = [];
			const circular = {};
			circular.self = circular;
			for (const run of [
				() => text(circular),
				() => image(""),
				() => image("https://example.com/a.png"),
				() => image("data:image/png,raw"),
				() => image({ type: "text", text: "x" }),
				() => image({ type: "image", data: "" }),
				() => image(42),
				() => image("data:image/png;base64,AAAA!"),
				() => image("data:image/png;base64,AAAAA"),
				() => image("data:image/png;base64,AA=A"),
				() => image("data:image/png;base64,"),
				() => image("data:image/png;base64,AAAA\n[Output truncated]"),
				() => image({ type: "image", data: "AAAA!", mimeType: "image/png" }),
				() => image("data:image/png;base64,AAAA"),
				() => image("data:image/png;base64,QUJD"),
				() => image("data:image/jpeg;base64,/9j/9w=="),
			]) {
				try { run(); errors.push("no error"); } catch (error) { errors.push(error.name + ": " + error.message); }
			}
			return errors;
		`)
	if !result.OK || len(result.Output) != 0 {
		t.Fatalf("result = %+v", result)
	}
	var errs []string
	if err := json.Unmarshal(result.Value, &errs); err != nil || len(errs) != 16 {
		t.Fatalf("value = %s (%v)", result.Value, err)
	}
	if !regexp.MustCompile(`^TypeError: .*circular`).MatchString(errs[0]) {
		t.Errorf("errors[0] = %q", errs[0])
	}
	want := []string{
		"TypeError: image expects a non-empty image URL string, an object with image_url, or a raw MCP image block",
		"TypeError: remote image URLs are not supported in tool outputs. Pass a base64 data URI instead",
		"TypeError: invalid image output. Pass a base64 data URI instead",
		`TypeError: image only accepts MCP image blocks, got "text"`,
		"TypeError: image expected MCP image data",
		"TypeError: image expects a non-empty image URL string, an object with image_url, or a raw MCP image block",
	}
	for range 6 {
		want = append(want, "TypeError: invalid image output. The image data is not valid base64 (truncated or corrupted?)")
	}
	for range 3 {
		want = append(want, "TypeError: invalid image output. The image data is not a PNG, JPEG, GIF, or WebP image")
	}
	if !slices.Equal(errs[1:], want) {
		t.Errorf("errors[1:] = %q", errs[1:])
	}
}

// https://github.com/earendil-works/pi/issues/10215
func TestAcceptsWrappedAndLargeBase64ImageData(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	large := "iVBORw0KGgoA" + strings.Repeat("QUJD", 256*1024)
	result := run(t, sandbox, `
			image("data:image/png;base64,iVBORw0K\r\nGgo=\n");
			image("data:image/png;base64,`+large+`");
		`)
	if !result.OK {
		t.Fatalf("failed: %+v", result.Error)
	}
	want := []codemode.OutputItem{
		{Type: "image", Data: pngBase64, MimeType: "image/png"},
		{Type: "image", Data: large, MimeType: "image/png"},
	}
	if !slices.Equal(result.Output, want) {
		t.Fatalf("output has %d items, first %q, want the wrapped data unwrapped and the large data kept", len(result.Output), result.Output[0].Data)
	}
}

func TestEndsTheScriptSuccessfullyOnExitKeepingOutputAndStoreWrites(t *testing.T) {
	result := run(t, newSandbox(t, 10_000, echo), `
			text("before");
			store("k", 1);
			await tools.echo(1);
			try { exit(); } catch {}
			text("after");
			return "unreachable";
		`)
	wantOK(t, result, "undefined")
	if !slices.Equal(result.Output, []codemode.OutputItem{{Type: "text", Text: "before"}}) {
		t.Errorf("output = %+v", result.Output)
	}
	sameJSON(t, "storeWrites.set.k", result.StoreWrites.Set["k"], "1")
	if len(result.StoreWrites.Set) != 1 || len(result.StoreWrites.Delete) != 0 {
		t.Errorf("storeWrites = %+v", result.StoreWrites)
	}
}

func TestKeepsOutputProducedBeforeAFailure(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), "text(\"partial\");\nthrow new Error(\"boom\")")
	if result.OK || !slices.Equal(result.Output, []codemode.OutputItem{{Type: "text", Text: "partial"}}) {
		t.Fatalf("result = %+v", result)
	}
}

func TestReportsSyntaxErrorsWithTheScriptsLineNumber(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 10_000), "const a = 1;\nconst b = ;\nreturn a"), codemode.ErrorScript)
	if e.Name != "SyntaxError" || !regexp.MustCompile(`codemode\.js:2`).MatchString(e.Stack) {
		t.Fatalf("error = %+v", e)
	}
}

func TestReportsThrownErrorsWithTheScriptsLineNumber(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 10_000), "const a = 1;\nthrow new TypeError('boom ' + a)"), codemode.ErrorScript)
	if e.Name != "TypeError" || e.Message != "boom 1" || !regexp.MustCompile(`codemode\.js:2`).MatchString(e.Stack) {
		t.Fatalf("error = %+v", e)
	}
}

func TestFormatsStacksLikeV8WithoutPreludeFrames(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), "console.log(new Error('inner'));\nthrow new RangeError('outer')")
	e := wantFailure(t, result, codemode.ErrorScript)
	if !regexp.MustCompile(`^RangeError: outer\n {4}at .*codemode\.js:2`).MatchString(e.Stack) || strings.Contains(e.Stack, "codemode-prelude.js") {
		t.Fatalf("stack = %q", e.Stack)
	}
	if len(result.Output) == 0 || !regexp.MustCompile(`^Error: inner\n {4}at .*codemode\.js:1`).MatchString(result.Output[0].Text) {
		t.Fatalf("output = %+v", result.Output)
	}
	all, _ := json.Marshal(result.Output)
	if strings.Contains(string(all), "codemode-prelude.js") {
		t.Fatalf("output leaks prelude frames: %s", all)
	}
}

func TestReportsNonErrorThrows(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 10_000), "throw { code: 7 }"), codemode.ErrorScript)
	if e.Message != `{"code":7}` {
		t.Fatalf("error = %+v", e)
	}
}

func TestReportsANonSerializableReturnValueAsAScriptError(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 10_000), "return 10n"), codemode.ErrorScript)
	if e.Name != "TypeError" {
		t.Fatalf("error = %+v", e)
	}
}

func TestExposesToolsAsAsyncFunctionsAndRecordsCalls(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	add := codemode.Tool{Name: "add", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, string(args))
		mu.Unlock()
		var in map[string]float64
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]float64{"sum": in["a"] + in["b"]})
	}}
	result := run(t, newSandbox(t, 10_000, add), `
			const first = await tools.add({ a: 1, b: 2 });
			const second = await tools.add({ a: first.sum, b: 10 });
			return second.sum;
		`)
	wantOK(t, result, "13")
	if !slices.Equal(seen, []string{`{"a":1,"b":2}`, `{"a":3,"b":10}`}) {
		t.Errorf("seen = %v", seen)
	}
	if len(result.Calls) != 2 {
		t.Fatalf("calls = %+v", result.Calls)
	}
	for _, c := range result.Calls {
		if c.Name != "add" || c.Status != codemode.CallOK || c.DurationMs < 0 {
			t.Errorf("call = %+v", c)
		}
	}
}

func TestRunsConcurrentCallsAndListsToolNames(t *testing.T) {
	delay := codemode.Tool{Name: "delay", Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		select {
		case <-time.After(20 * time.Millisecond):
			return args, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	result := run(t, newSandbox(t, 10_000, echo, delay), `
			const [a, b, c] = await Promise.all([tools.delay(1), tools.delay(2), tools.echo(3)]);
			return { values: [a, b, c], names: Object.keys(tools) };
		`)
	wantOK(t, result, `{"values":[1,2,3],"names":["echo","delay"]}`)
}

func TestExposesToolsUnderNormalizedIdentifiersAndListsThemInALL_TOOLS(t *testing.T) {
	constant := func(name, description, value string) codemode.Tool {
		return codemode.Tool{Name: name, Description: description, Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) { return js(value), nil }}
	}
	result := run(t, newSandbox(t, 10_000,
		constant("my-tool", "Dashes", `"dash"`), constant("my_tool", "Shadowed", `"underscore"`), constant("mcp__docs__search", "", `"mcp"`)), `
			try { ALL_TOOLS.push({}); } catch {}
			return {
				all: ALL_TOOLS,
				calls: [await tools.my_tool(), await tools["my-tool"](), await tools.mcp__docs__search()],
			};
		`)
	wantOK(t, result, `{"all":[{"name":"my_tool","description":"Dashes"},{"name":"mcp__docs__search","description":""}],"calls":["dash","dash","mcp"]}`)
}

func TestPassesUndefinedArgumentsAndResultsThrough(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000, codemode.Tool{Name: "noop", Execute: echo.Execute}), "return [await tools.noop(), await tools.noop(null)]"), "[null,null]")
}

func TestTurnsToolErrorsIntoCatchableErrorsInTheScript(t *testing.T) {
	fail := codemode.Tool{Name: "fail", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("tool exploded")
	}}
	result := run(t, newSandbox(t, 10_000, fail), `
			try {
				await tools.fail();
				return "no error";
			} catch (error) {
				return { isError: error instanceof Error, message: error.message };
			}
		`)
	wantOK(t, result, `{"isError":true,"message":"tool exploded"}`)
	if len(result.Calls) != 1 || result.Calls[0].Name != "fail" || result.Calls[0].Status != codemode.CallError {
		t.Fatalf("calls = %+v", result.Calls)
	}
}

func TestRejectsCallsToUnknownTools(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 10_000), "return await tools.missing()"), codemode.ErrorScript)
	if e.Name != "TypeError" {
		t.Fatalf("error = %+v", e)
	}
}

func TestAbortsUnawaitedCallsWhenTheScriptReturns(t *testing.T) {
	var aborted bool
	slow := codemode.Tool{Name: "slow", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		aborted = true
		return nil, errors.New("aborted")
	}}
	result := run(t, newSandbox(t, 10_000, slow), "tools.slow(); return 'early'")
	wantOK(t, result, `"early"`)
	if len(result.Calls) != 1 || result.Calls[0].Name != "slow" || result.Calls[0].Status != codemode.CallCancelled {
		t.Fatalf("calls = %+v", result.Calls)
	}
	// Execute returns only after its tool goroutines joined, so the write is visible here without a lock.
	if !aborted {
		t.Fatal("the unawaited call's context was not cancelled")
	}
}

func TestSupportsRegisterAndUnregisterBetweenExecutions(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	if err := sandbox.RegisterTool(echo); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.RegisterTool(echo); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("second RegisterTool error = %v", err)
	}
	if tools := sandbox.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("Tools() = %+v", tools)
	}
	wantOK(t, run(t, sandbox, "return await tools.echo('a')"), `"a"`)
	if !sandbox.UnregisterTool("echo") {
		t.Fatal("UnregisterTool(echo) = false")
	}
	wantOK(t, run(t, sandbox, "return 'echo' in tools"), "false")
}

// valueOrMessage is upstream's `result.ok ? result.value : result.error.message` as JSON text.
func valueOrMessage(t *testing.T, result codemode.Result) json.RawMessage {
	t.Helper()
	if result.OK {
		return result.Value
	}
	if result.Error == nil {
		t.Fatalf("failed result without an error: %+v", result)
	}
	message, err := json.Marshal(result.Error.Message)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestNamesCloseMatchesWhenAScriptReadsAToolThatDoesNotExist(t *testing.T) {
	webSearch := codemode.Tool{Name: "web-search", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) { return js(`""`), nil }}
	sandbox := newSandbox(t, 10_000, echo, webSearch)
	attempt := func(expression string) json.RawMessage {
		t.Helper()
		return valueOrMessage(t, run(t, sandbox, "return "+expression+";"))
	}
	message := func(expression string) string {
		t.Helper()
		var text string
		if raw := attempt(expression); json.Unmarshal(raw, &text) != nil {
			t.Errorf("%s = %s, want an error message", expression, raw)
		}
		return text
	}
	if got, want := message("tools.Echo"), `tools.Echo does not exist. Did you mean tools.echo? ALL_TOOLS lists every tool; searchTools(query) finds tools by topic. Check for a member with "Echo" in tools.`; got != want {
		t.Errorf("tools.Echo = %q, want %q", got, want)
	}
	if got := message("tools.websearch"); !strings.Contains(got, "Did you mean tools.web_search?") {
		t.Errorf("tools.websearch = %q", got)
	}
	if got := message("tools.nothing"); !strings.Contains(got, "Available: echo, web_search.") {
		t.Errorf("tools.nothing = %q", got)
	}
	sameJSON(t, "in/toString/stringify", attempt("['echo' in tools, 'nothing' in tools, String(tools.toString), JSON.stringify(tools)]"), `[true,false,"undefined","{}"]`)
}

func TestStoreReadsTheSnapshotAndReportsWrites(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `
			const seen = load("counter");
			store("counter", seen + 1);
			store("list", [1, { a: null }]);
			store("old", undefined);
			return [seen, load("counter"), load("missing"), load("old")];
		`, codemode.ExecuteOptions{Store: map[string]json.RawMessage{"counter": js("41"), "old": js(`"x"`)}})
	wantOK(t, result, "[41,42,null,null]")
	sameJSON(t, "set.counter", result.StoreWrites.Set["counter"], "42")
	sameJSON(t, "set.list", result.StoreWrites.Set["list"], `[1,{"a":null}]`)
	if !slices.Equal(result.StoreWrites.Delete, []string{"old"}) {
		t.Errorf("delete = %v", result.StoreWrites.Delete)
	}
}

func TestStoreReturnsCopies(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `const value = load("obj"); value.a = 2; const kept = { b: 1 }; store("kept", kept); kept.b = 2;
			return [load("obj").a, load("kept").b];`, codemode.ExecuteOptions{Store: map[string]json.RawMessage{"obj": js(`{"a":1}`)}})
	wantOK(t, result, "[1,1]")
	sameJSON(t, "set.kept", result.StoreWrites.Set["kept"], `{"b":1}`)
}

func TestRejectsInvalidKeysValuesAndOversizedWritesInsideTheScript(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `
			const attempt = (fn) => { try { fn(); return "ok"; } catch (error) { return error.name; } };
			return [
				attempt(() => store(1, "x")),
				attempt(() => load({})),
				attempt(() => store("fn", () => 1)),
				attempt(() => store("big", "x".repeat(300 * 1024))),
				attempt(() => { for (let i = 0; i < 8; i++) store("k" + i, "x".repeat(200 * 1024)); }),
			];
		`)
	wantOK(t, result, `["TypeError","TypeError","TypeError","RangeError","RangeError"]`)
}

func TestExplainsOversizedWrites(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `store("img", "x".repeat(300 * 1024));`)
	if result.OK || result.Error == nil {
		t.Fatalf("result = %+v, want a failure", result)
	}
	for _, want := range []string{`store("img") value has 307202 characters of JSON`, "Show images with image()"} {
		if !strings.Contains(result.Error.Message, want) {
			t.Errorf("message %q lacks %q", result.Error.Message, want)
		}
	}
}

func TestReservesTheStoreAndLoadNames(t *testing.T) {
	execute := func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	for _, name := range []string{"store", "load"} {
		if _, err := codemode.NewSandbox(codemode.SandboxOptions{Globals: []codemode.Tool{{Name: name, Execute: execute}}}); err == nil || !strings.Contains(err.Error(), "Invalid global") {
			t.Errorf("global %q: error = %v", name, err)
		}
	}
}

func TestExposesGlobalsAsTopLevelFunctionsWithoutRecordingThemAsCalls(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	attach := codemode.Tool{Name: "attach", Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, string(args))
		mu.Unlock()
		return nil, nil
	}}
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Tools: []codemode.Tool{echo}, Globals: []codemode.Tool{attach}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result := run(t, sandbox, `
			await attach({ ref: 1 });
			attach("not awaited");
			return [typeof attach, typeof globalThis.attach, await tools.echo(2)];
		`)
	wantOK(t, result, `["function","function",2]`)
	if len(result.Calls) != 1 || result.Calls[0].Name != "echo" {
		t.Fatalf("calls = %+v", result.Calls)
	}
	// Calls start in message order, so an unawaited global still runs before the script settles.
	if !slices.Equal(seen, []string{`{"ref":1}`, `"not awaited"`}) {
		t.Fatalf("seen = %v", seen)
	}
}

func TestGroupsNamespacedGlobalsAndSpreadsArgumentsOnRequest(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	list := codemode.Tool{Name: "models.list", Spread: true, Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		seen = append(seen, string(args))
		mu.Unlock()
		return nil, nil
	}}
	first := codemode.Tool{Name: "models.first", Execute: echo.Execute}
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Globals: []codemode.Tool{list, first}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result := run(t, sandbox, `
			await models.list("classifier", undefined, 3);
			await models.list();
			try { models.extra = 1; } catch {}
			return [Object.keys(models), await models.first("a", "ignored"), "extra" in models];
		`)
	wantOK(t, result, `[["list","first"],"a",false]`)
	if !slices.Equal(seen, []string{`["classifier",null,3]`, `[]`}) {
		t.Fatalf("seen = %v", seen)
	}
}

func TestNamesTheMembersOfANamespaceWhenAScriptReadsOneThatDoesNotExist(t *testing.T) {
	execute := func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Globals: []codemode.Tool{
		{Name: "models.classify", Execute: execute},
		{Name: "models.generateImages", Execute: execute},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	result := run(t, sandbox, "await models.generateImage();")
	if result.OK || result.Error == nil {
		t.Fatalf("result = %+v, want a failure", result)
	}
	if got, want := result.Error.Message, `models.generateImage does not exist. Did you mean models.generateImages? Check for a member with "generateImage" in models.`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestRejectsInvalidAndReservedGlobalNames(t *testing.T) {
	execute := func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	build := func(names ...string) error {
		var globals []codemode.Tool
		for _, name := range names {
			globals = append(globals, codemode.Tool{Name: name, Execute: execute})
		}
		_, err := codemode.NewSandbox(codemode.SandboxOptions{Globals: globals})
		return err
	}
	for _, name := range []string{"a.b.c", "a.", ".a", "tools.x", "store.x", "a.not-valid", "not-valid", "tools", "console"} {
		if err := build(name); err == nil || !strings.Contains(err.Error(), "Invalid global") {
			t.Errorf("global %q: error = %v", name, err)
		}
	}
	if err := build("models", "models.list"); err == nil || !strings.Contains(err.Error(), "conflicts with the namespace") {
		t.Errorf("models + models.list: error = %v", err)
	}
}

func TestTerminatesASynchronousInfiniteLoopOnTimeout(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	started := time.Now()
	result := run(t, sandbox, "while (true) {}", codemode.ExecuteOptions{TimeoutMs: 200})
	wantFailure(t, result, codemode.ErrorTimeout)
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("took %v", elapsed)
	}
}

func TestRunsWithoutADeadlineWhenTimeoutIsInfinity(t *testing.T) {
	wait := codemode.Tool{Name: "wait", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		select {
		case <-time.After(50 * time.Millisecond):
			return js(`"late"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	wantOK(t, run(t, newSandbox(t, 10_000, wait), "return await tools.wait()", codemode.ExecuteOptions{TimeoutMs: math.Inf(1)}), `"late"`)
}

func TestFailsAScriptThatWaitsOnAPromiseNothingCanSettle(t *testing.T) {
	sandbox := newSandbox(t, 10_000, echo)
	result := run(t, sandbox, "await tools.echo(1); await new Promise(() => {}); return 'never'", codemode.ExecuteOptions{TimeoutMs: math.Inf(1)})
	e := wantFailure(t, result, codemode.ErrorScript)
	if !strings.Contains(e.Message, "can never settle") {
		t.Fatalf("error = %+v", e)
	}
	// Returning while a call is still pending is not a stall.
	wantOK(t, run(t, sandbox, "tools.echo(2); return 'early'", codemode.ExecuteOptions{TimeoutMs: math.Inf(1)}), `"early"`)
}

func TestTerminatesAMicrotaskSpinningLoopOnTimeout(t *testing.T) {
	wantFailure(t, run(t, newSandbox(t, 10_000), "while (true) await null", codemode.ExecuteOptions{TimeoutMs: 200}), codemode.ErrorTimeout)
}

func TestAbortsViaContextAndCancelsInFlightCalls(t *testing.T) {
	var mu sync.Mutex
	var toolCtx context.Context
	called := make(chan struct{}, 8)
	hang := codemode.Tool{Name: "hang", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		toolCtx = ctx
		mu.Unlock()
		called <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	sandbox := newSandbox(t, 10_000, hang)
	closed := make(chan codemode.Result, 1)
	go func() {
		result, _ := sandbox.Execute(context.Background(), "await tools.hang(); return 'never'", codemode.ExecuteOptions{})
		closed <- result
	}()
	receive(t, called)
	first, cancelFirst := context.WithCancelCause(context.Background())
	cancelFirst(errors.New("user cancelled"))
	mu.Lock()
	if toolCtx.Err() != nil {
		t.Fatal("the running execution's tool context was cancelled by an unrelated abort")
	}
	mu.Unlock()
	result, err := sandbox.Execute(first, "await tools.hang()", codemode.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if e := wantFailure(t, result, codemode.ErrorAborted); e.Message != "user cancelled" {
		t.Fatalf("message = %q", e.Message)
	}

	second, cancelSecond := context.WithCancel(context.Background())
	pending := make(chan codemode.Result, 1)
	go func() {
		result, _ := sandbox.Execute(second, "await tools.hang(); return 'never'", codemode.ExecuteOptions{})
		pending <- result
	}()
	receive(t, called)
	cancelSecond()
	aborted := receive(t, pending)
	wantFailure(t, aborted, codemode.ErrorAborted)
	if len(aborted.Calls) != 1 || aborted.Calls[0].Name != "hang" || aborted.Calls[0].Status != codemode.CallCancelled {
		t.Fatalf("calls = %+v", aborted.Calls)
	}
	mu.Lock()
	if toolCtx.Err() == nil {
		t.Fatal("the aborted execution's tool context was not cancelled")
	}
	mu.Unlock()

	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	if e := wantFailure(t, receive(t, closed), codemode.ErrorAborted); e.Message != "Sandbox closed" {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestRejectsExecuteAfterClose(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.Execute(t.Context(), "return 1", codemode.ExecuteOptions{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Execute after Close error = %v", err)
	}
}

func TestRunsExecutionsInParallelWithoutSharingState(t *testing.T) {
	sandbox := newSandbox(t, 10_000)
	codes := []string{
		"globalThis.shared = 'a'; await null; return globalThis.shared",
		"globalThis.shared = 'b'; await null; return globalThis.shared",
		"return typeof globalThis.shared",
	}
	results := make([]codemode.Result, len(codes))
	var wg sync.WaitGroup
	for i, code := range codes {
		wg.Go(func() { results[i], _ = sandbox.Execute(t.Context(), code, codemode.ExecuteOptions{}) })
	}
	wg.Wait()
	for i, want := range []string{`"a"`, `"b"`, `"undefined"`} {
		wantOK(t, results[i], want)
	}
}

func TestTurnsDeepRecursionIntoACatchableRangeError(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000), `
			let depth = 0;
			function dive() { depth++; dive(); }
			try { dive(); } catch (error) { return [error.name, depth > 1000]; }
		`), `["RangeError",true]`)
}

func TestReportsAFailingWasmModuleAsASandboxError(t *testing.T) {
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Wasm: func() ([]byte, error) { return nil, errors.New("no wasm") }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if e := wantFailure(t, run(t, sandbox, "return 1"), codemode.ErrorSandbox); e.Message != "Failed to load QuickJS: no wasm" {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestCorruptWasmModuleIsASandboxError(t *testing.T) {
	sandbox, err := codemode.NewSandbox(codemode.SandboxOptions{Wasm: func() ([]byte, error) { return []byte("not wasm"), nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	e := wantFailure(t, run(t, sandbox, "return 1"), codemode.ErrorSandbox)
	if !strings.HasPrefix(e.Message, "Failed to load QuickJS: ") {
		t.Fatalf("message = %q", e.Message)
	}
}

func TestHasNoHostGlobals(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000), `
			return [
				typeof process, typeof require, typeof module, typeof setTimeout, typeof fetch,
				typeof WebAssembly, typeof std, typeof os, typeof globalThis.constructor,
			]
		`), `["undefined","undefined","undefined","undefined","undefined","undefined","undefined","undefined","function"]`)
}

func TestKeepsEvalAndFunctionInsideTheVM(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000, echo), `
			return [
				eval("typeof process"),
				new Function("return typeof process")(),
				tools.echo.constructor("return typeof require")(),
				(async () => {}).constructor("return typeof setTimeout")() instanceof Promise,
			];
		`), `["undefined","undefined","undefined",true]`)
}

func TestRejectsDynamicImport(t *testing.T) {
	result := run(t, newSandbox(t, 10_000), `
			try { await import("node:fs"); return "imported"; } catch (error) { return error.constructor.name; }
		`)
	if !result.OK || string(result.Value) == `"imported"` {
		t.Fatalf("result = %+v (%s)", result, result.Value)
	}
}

func TestKeepsToolsAndConsoleFrozen(t *testing.T) {
	wantOK(t, run(t, newSandbox(t, 10_000, echo), `
			try { tools.echo = () => 'nope'; } catch {}
			try { tools.extra = () => 'nope'; } catch {}
			try { globalThis.tools = null; } catch {}
			return ["extra" in tools, await tools.echo('still')];
		`), `[false,"still"]`)
}

// .upstream/v1.0.1/packages/codemode/test/sandbox.test.ts:582 (#10283): the host keeps all output, so a script that
// prints in a loop must not grow it without bound.
func TestSandboxFailsAScriptWhoseOutputPassesTheLimitsEvenIfItCatchesTheError(t *testing.T) {
	sandbox := newSandbox(t, 0)
	for _, print := range []string{"text(s)", "console.log(s)", `image("data:image/png;base64," + p)`} {
		result := run(t, sandbox, `
				const s = "x".repeat(1 << 20);
				const p = "iVBORw0KGgoA" + "A".repeat(1 << 20);
				for (;;) { try { `+print+`; } catch {} }
			`)
		if result.OK || result.Error == nil || result.Error.Kind != codemode.ErrorScript || result.Error.Name != "RangeError" || !strings.Contains(result.Error.Message, "script output exceeded") {
			t.Fatalf("%s: result = %+v", print, result.Error)
		}
		chars := 0
		for _, item := range result.Output {
			if item.Type == "text" {
				chars += len(item.Text)
			} else {
				chars += len(item.Data)
			}
		}
		if chars > codemode.MaxOutputChars || chars <= codemode.MaxOutputChars-(2<<20) {
			t.Fatalf("%s: output chars = %d", print, chars)
		}
	}

	empty := run(t, sandbox, `for (;;) text("");`)
	if empty.OK || empty.Error == nil || empty.Error.Name != "RangeError" || len(empty.Output) != codemode.MaxOutputItems {
		t.Fatalf("empty: error = %+v, %d items", empty.Error, len(empty.Output))
	}
}
