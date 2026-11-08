package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Not upstream's (docs/plan/chord-plugins-node-evaluator.md, 10.4 #4): Pi's `HostPackages` are module instances of the app's own process; here the
// app offers Go functions and JSON values (GoPackage), which the plugins `require` as they require a host package. The
// plugin of these tests is a handwritten script (`handmade`) that serves `test.probe`, whose `call` runs the op the test names,
// so every case runs in the real Node subprocess and reads its result through the Go host's hold.

// cbProbePlugin is a plugin of the worker kind that serves `test.probe`: `call(request)` runs `ops[op](args, ctx)` with
// `next` (the host's chord), `go()` (the `@acme/go` host package) and `file()` (the `@acme/file` one) in scope, and answers its
// JSON.
func cbProbePlugin(t *testing.T, ops string) PluginScript {
	t.Helper()
	return handmade(t, "@test/cb", "worker", `(function (require, module, exports) {
const next = require("@earendil-works/chord");
const { defineFacet, remote, method, str, hostKind } = next;
const go = () => require("@acme/go");
const file = () => require("@acme/file");
const Worker = hostKind("test.worker");
const Probe = remote("test.probe", {
	version: 1,
	params: {},
	hostAddress: () => Worker(),
	variants: { main: { state: {}, methods: { call: method(str(), str()) } } },
});
const ops = { `+ops+` };
module.exports = { default: defineFacet((host) =>
	host.serve(Probe, () => ({
		variant: "main",
		members: {
			call: async (request, ctx) => {
				const { op, args } = JSON.parse(request);
				return JSON.stringify((await ops[op](args, ctx)) ?? null);
			},
		},
	})),
) };
})`)
}

// cbStart loads the probe plugin with ops on a Node host (more of its load from change) routed from a new Go host, and holds
// the probe.
func cbStart(t *testing.T, ops string, change func(*RuntimeLoad)) (*NodeHost, *probe) {
	t.Helper()
	address := workerKind.Address(nil)
	load := RuntimeLoad{
		Kind: "worker", Scripts: []PluginScript{cbProbePlugin(t, ops)},
		Address: &address, Principal: workerPrincipal, Peer: serverPrincipal,
	}
	if change != nil {
		change(&load)
	}
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(load, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %v", report.Failed)
	}
	return node, probeOn(t, serverFor(t, workerRoute(node)))
}

// A plugin `require`s a GoPackage: it calls a Go function with JSON arguments and reads the result, and reads a value (a
// number, and an object whose keys keep their order) and a function of a package it shares with a HostPackages file.
func TestGoPackageAPluginCallsAGoFunctionAndReadsAValue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file.mjs")
	if err := os.WriteFile(file, []byte(`export const fromFile = "a file's value";`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, p := cbStart(t, `
greet: async ({ name }, ctx) => go().greet({ name }, ctx),
answer: async () => go().answer,
config: async () => go().config,
fromFile: async () => file().fromFile,
nothing: async () => go().nothing,
`, func(load *RuntimeLoad) {
		load.Modules = HostPackages{"@acme/file": file}
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"greet": GoFunc(func(_ context.Context, args delta.JsonValue) (delta.JsonValue, error) {
				name, _ := args.(*delta.JsonObject).Value("name").(string)
				return "hello " + name, nil
			}),
			"answer":  GoValue(float64(42)),
			"config":  GoValue(delta.JsonObjectOf("z", 1.0, "a", []any{"x", true, nil})),
			"nothing": GoValue(nil),
		}}}
	})
	if got := p.must("greet", map[string]any{"name": "Pi"}); got != "hello Pi" {
		t.Fatalf("greet = %v", got)
	}
	if got := p.must("answer", nil); got != 42.0 {
		t.Fatalf("answer = %v", got)
	}
	if got := p.must("config", nil); !reflect.DeepEqual(got, map[string]any{"z": 1.0, "a": []any{"x", true, nil}}) {
		t.Fatalf("config = %v", got)
	}
	if got := p.must("fromFile", nil); got != "a file's value" {
		t.Fatalf("fromFile = %v (a HostPackages file must keep working next to a GoPackage)", got)
	}
	if got := p.must("nothing", nil); got != nil {
		t.Fatalf("nothing = %v", got)
	}
}

// An app's own module imports the GoPackage by its specifier and gets the same functions and values the plugins `require`: its
// top-level code calls a Go function and checks a value, so the load fails unless both worked.
func TestGoPackageAnAppModuleImportsItBySpecifier(t *testing.T) {
	module := filepath.Join(t.TempDir(), "core.mjs")
	if err := os.WriteFile(module, []byte(`import { answer, twice } from "@acme/go";
import * as next from "@earendil-works/chord";
if (answer !== 7) throw new Error("answer is " + answer);
if ((await twice(21)) !== 42) throw new Error("twice(21) is not 42");
export default next.defineFacet(() => {});
`), 0o644); err != nil {
		t.Fatal(err)
	}
	called := make(chan float64, 4)
	address := workerKind.Address(nil)
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "worker", Core: []CoreFacet{{Name: "@test/core", Module: module}}, Address: &address,
		Principal: workerPrincipal, Peer: serverPrincipal,
		GoPackages: []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"answer": GoValue(float64(7)),
			"twice": GoFunc(func(_ context.Context, args delta.JsonValue) (delta.JsonValue, error) {
				called <- args.(float64)
				return args.(float64) * 2, nil
			}),
		}}},
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if got := <-called; got != 21 {
		t.Fatalf("the Go function got %v, want 21", got)
	}
}

// A Go error crosses as a thrown Error (its message), a *data.ChordError as a ChordError with its code and reason, and a result
// that is not JSON as an error too; an unknown export is the plugin's own undefined.
func TestGoPackageAGoErrorIsAThrownError(t *testing.T) {
	_, p := cbStart(t, `
fail: async (_a, ctx) => {
	try { await go().fail(null, ctx); } catch (error) {
		return { name: error.name, message: error.message, chord: next.isChordError(error), code: error.code, reason: error.reason };
	}
},
chord: async (_a, ctx) => {
	try { await go().chord(null, ctx); } catch (error) {
		return { name: error.name, message: error.message, chord: next.isChordError(error), code: error.code, reason: error.reason };
	}
},
notJSON: async (_a, ctx) => {
	try { await go().notJSON(null, ctx); } catch (error) { return error.message; }
},
missing: async () => typeof go().nope,
`, func(load *RuntimeLoad) {
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"fail": GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) {
				return nil, errors.New("the Go side failed")
			}),
			"chord": GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) {
				return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: "refused by Go"}, nil)
			}),
			"notJSON": GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) { return make(chan int), nil }),
		}}}
	})
	want := map[string]any{"name": "Error", "message": "the Go side failed", "chord": false}
	if got := p.must("fail", nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("fail = %v, want %v", got, want)
	}
	want = map[string]any{"name": "ChordError", "message": "refused by Go", "chord": true, "code": "forbidden", "reason": "revoked"}
	if got := p.must("chord", nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("chord = %v, want %v", got, want)
	}
	if got, _ := p.must("notJSON", nil).(string); !strings.Contains(got, "the result is not JSON") {
		t.Fatalf("notJSON = %v", got)
	}
	if got := p.must("missing", nil); got != "undefined" {
		t.Fatalf("missing = %v", got)
	}
}

// cbBlocker is a Go function that waits for its context and reports why it ended.
type cbBlocker struct {
	started chan struct{}
	ended   chan error
}

func newCbBlocker() *cbBlocker {
	return &cbBlocker{started: make(chan struct{}, 4), ended: make(chan error, 4)}
}

func (b *cbBlocker) function() GoExport {
	return GoFunc(func(ctx context.Context, _ delta.JsonValue) (delta.JsonValue, error) {
		b.started <- struct{}{}
		<-ctx.Done()
		b.ended <- ctx.Err()
		return nil, ctx.Err()
	})
}

func (b *cbBlocker) awaitEnd(t *testing.T, what string) error {
	t.Helper()
	select {
	case err := <-b.ended:
		return err
	case <-time.After(15 * time.Second):
		t.Fatalf("the Go function's context did not end: %s", what)
		return nil
	}
}

// A call the plugin aborts (an AbortSignal, the call's context argument) rejects at once with the abort's reason and cancels the
// Go function's context.
func TestGoPackageACancelledCallRejectsAndCancelsTheGoContext(t *testing.T) {
	blocker := newCbBlocker()
	_, p := cbStart(t, `
abort: async () => {
	const controller = new AbortController();
	const pending = go().block(null, controller.signal).then(() => "resolved", (error) => error.message);
	await new Promise((resolve) => setTimeout(resolve, 100));
	controller.abort(new Error("the plugin gave up"));
	return pending;
},
alreadyAborted: async () => {
	const signal = AbortSignal.abort(new Error("aborted before the call"));
	return go().block(null, signal).then(() => "resolved", (error) => error.message);
},
`, func(load *RuntimeLoad) {
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{"block": blocker.function()}}}
	})
	if got := p.must("abort", nil); got != "the plugin gave up" {
		t.Fatalf("abort = %v", got)
	}
	if err := blocker.awaitEnd(t, "the plugin aborted its call"); !errors.Is(err, context.Canceled) {
		t.Fatalf("the Go function's context ended with %v, want context.Canceled", err)
	}
	<-blocker.started
	// A call aborted before it starts never reaches Go.
	if got := p.must("alreadyAborted", nil); got != "aborted before the call" {
		t.Fatalf("alreadyAborted = %v", got)
	}
	if len(blocker.started) != 0 {
		t.Fatal("a call aborted before it began ran the Go function")
	}
}

// The abort of the request a plugin's call belongs to (the Go caller's context of the probe's `call`, which the host hands the
// handler as its `ctx.abortSignal`) cancels the Go function's context.
func TestGoPackageTheAbortOfThePluginsRequestCancelsTheGoContext(t *testing.T) {
	blocker := newCbBlocker()
	_, p := cbStart(t, `
block: async (_a, ctx) => go().block(null, ctx).then(() => "resolved", (error) => error.name),
`, func(load *RuntimeLoad) {
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{"block": blocker.function()}}}
	})
	request, err := json.Marshal(map[string]any{"op": "block", "args": nil})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(background)
	done := make(chan error, 1)
	go func() {
		_, err := p.ref.Call("call", string(request), ctx)
		done <- err
	}()
	select {
	case <-blocker.started:
	case <-time.After(15 * time.Second):
		t.Fatal("the Go function never started")
	}
	cancel()
	if err := blocker.awaitEnd(t, "the request was aborted"); !errors.Is(err, context.Canceled) {
		t.Fatalf("the Go function's context ended with %v, want context.Canceled", err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the cancelled call never returned")
	}
}

// The calls of one plugin run concurrently and are answered in any order: `slow` waits in Go until the plugin asks for
// `release`, which it does only after `fast` was answered, so the answers come in the opposite order to the calls.
func TestGoPackageCallsRunConcurrentlyAndAnswerInAnyOrder(t *testing.T) {
	release := make(chan struct{})
	_, p := cbStart(t, `
both: async () => {
	const order = [];
	const slow = go().slow(null).then(() => order.push("slow"));
	await go().fast(null);
	order.push("fast");
	await go().release(null);
	await slow;
	return order;
},
`, func(load *RuntimeLoad) {
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{
			"slow": GoFunc(func(ctx context.Context, _ delta.JsonValue) (delta.JsonValue, error) {
				select {
				case <-release:
					return nil, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}),
			"fast":    GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) { return nil, nil }),
			"release": GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) { close(release); return nil, nil }),
		}}}
	})
	if got := p.must("both", nil); !reflect.DeepEqual(got, []any{"fast", "slow"}) {
		t.Fatalf("order = %v, want [fast slow]", got)
	}
}

// A Go function still running when the process ends has its context cancelled.
func TestGoPackageTheProcessEndingCancelsARunningGoFunction(t *testing.T) {
	blocker := newCbBlocker()
	node, p := cbStart(t, `block: async () => go().block(null).then(() => "resolved", () => "rejected"),`, func(load *RuntimeLoad) {
		load.GoPackages = []GoPackage{{Specifier: "@acme/go", Exports: map[string]GoExport{"block": blocker.function()}}}
	})
	go func() { _, _ = p.call("block", nil) }()
	select {
	case <-blocker.started:
	case <-time.After(15 * time.Second):
		t.Fatal("the Go function never started")
	}
	node.kill()
	<-node.Exited()
	if err := blocker.awaitEnd(t, "the process ended"); !errors.Is(err, context.Canceled) {
		t.Fatalf("the Go function's context ended with %v, want context.Canceled", err)
	}
}

// A GoPackage that cannot be offered fails the load before a process starts.
func TestGoPackageThatCannotBeOfferedFailsTheLoad(t *testing.T) {
	function := GoFunc(func(context.Context, delta.JsonValue) (delta.JsonValue, error) { return nil, nil })
	for name, test := range map[string]struct {
		packages []GoPackage
		modules  HostPackages
		want     string
	}{
		"no specifier":  {packages: []GoPackage{{}}, want: "has no Specifier"},
		"twice":         {packages: []GoPackage{{Specifier: "@a/b"}, {Specifier: "@a/b"}}, want: `"@a/b" is offered twice`},
		"host's own":    {packages: []GoPackage{{Specifier: "@earendil-works/chord"}}, want: "the host's own package"},
		"a file's":      {packages: []GoPackage{{Specifier: "@a/b"}}, modules: HostPackages{"@a/b": "/x.mjs"}, want: "both a GoPackage and a HostPackages file"},
		"no export":     {packages: []GoPackage{{Specifier: "@a/b", Exports: map[string]GoExport{"x": {}}}}, want: "neither GoFunc nor GoValue"},
		"unnamed":       {packages: []GoPackage{{Specifier: "@a/b", Exports: map[string]GoExport{"": function}}}, want: "export with no name"},
		"value no JSON": {packages: []GoPackage{{Specifier: "@a/b", Exports: map[string]GoExport{"x": GoValue(make(chan int))}}}, want: `export "x" is not JSON`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
				Kind: "worker", Principal: workerPrincipal, Peer: serverPrincipal, GoPackages: test.packages, Modules: test.modules,
			}, background)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}
