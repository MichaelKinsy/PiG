package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/next-node/fixtures/workers/{control,worker}.ts: the fixture contracts (the session kind the
// workers serve, the fixture worker's `Control`, the plugin's `Notes`), the worker's start data (how it behaves), and the
// fixture worker itself: `WorkerMain`, the shipped plugins, a JavaScript core facet serving `Control` (crash, busy, local calls)
// and a Go core facet serving `Busy` (the app's keep-alive counts) and `Echo`. Its start data picks a behaviour for the tests:
// exit or fail before connecting, never connect, a slow or endless load, slow closers that log their order to a file.
//
// The plugins component evaluates in a Node subprocess (docs/plan/chord-plugins-node-evaluator.md), so the host that serves the
// worker's address, the JavaScript core facet (a module: testdata/core.mjs) and the real plugin built from testdata/plugins/notes
// is the worker's NodeHost, the child of the worker's Go host; the Go host is a relay without an address that
// `WorkerPlugins.Load` routes to the NodeHost's link. The Go core facet (`fixtureGoCore`) loads on that Go host, and
// `WorkerLoad.Bridged` gives the NodeHost a proxy of its local types, so `Control` binds to `Busy` and `Echo` in the same closure
// as the plugins, and the worker's Go code reads `Busy` directly, as a local type of its own host.
//
// Forced Go differences. A worker is the test binary run again (TestMain, fixtureEnv "worker"). `process.exit` is
// exitProcess in Go code and the NodeHost's own in `Control.crash` (the worker exits with its code). `Control.block` and
// `blockThenCrash` (a busy loop that blocks the event loop) are not ported: a Go worker has no single thread to block, and the
// tests that need an unresponsive worker stop its process from outside (freezeWorker, SIGSTOP). `Control.fatal` is not ported:
// no test calls it. Every message the worker sends to the server (frame or control) is counted by wrapping its port;
// `Control.localCalls` is called by the worker itself, whose Go code reports how many messages it sent to the server between
// the calls' two lines.

var (
	fixtureSession = mustFixture(contracts.NewHostKind("session", contracts.Prop("id", contracts.Str())))

	fixtureControl = mustFixture(contracts.Remote("test.control", contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			id, _ := params.Value("id").(string)
			return fixtureSession.Address(delta.JsonObjectOf("id", id)), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("crash", contracts.NewMethod(contracts.Int())),
			contracts.Meth("busy", contracts.NewMethod(contracts.Int())),
			contracts.Meth("localCalls", contracts.NewMethod(contracts.Int(), contracts.Int())),
		}})},
	}))

	// fixtureNotes is the fixture plugin's notes at a worker: test/next-node/workers/support.ts `Notes`.
	fixtureNotes = mustFixture(contracts.Remote("test.notes", contracts.RemoteDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Str())},
		HostAddress: func(params *delta.JsonObject) (contracts.Address, error) {
			id, _ := params.Value("id").(string)
			return fixtureSession.Address(delta.JsonObjectOf("id", id)), nil
		},
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("label", contracts.Str())}})},
	}))

	// fixtureBusy is the Go core facet's `Busy`: the app's keep-alive counts, a local type of the worker's Go host that the
	// NodeHost's `Control` uses through a proxy (plugins.BridgedFacet), and `fixtureEcho` its `Echo`.
	fixtureBusy = mustFixture(contracts.Local("test.busy", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("counts", contracts.Map(contracts.Int())), contracts.Mem("set", contracts.NewMethod(contracts.Int())),
			contracts.Mem("pulse", contracts.NewMethod(contracts.Int())),
		},
	}))
	fixtureEcho = mustFixture(contracts.Local("test.echo", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("echo", contracts.NewMethod(contracts.Int(), contracts.Int()))},
	}))

	fixtureCounts = func() *documents.StateDefinition {
		definition, err := documents.DefineState("counts", documents.StateOptions{
			Schema: contracts.Map(contracts.Int()), Version: 1,
			Initial: func() *delta.JsonObject { return delta.JsonObjectOf("tasks", 0.0) },
		})
		if err != nil {
			panic(err)
		}
		return definition
	}()
)

func mustFixture[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// workerBehaviour is the fixture worker's start data: how it behaves.
type workerBehaviour struct {
	// Exit exits with this code before connecting.
	Exit *int
	// Fatal fails with this message before connecting (`fatal`, redacted: `secret…` words removed).
	Fatal string
	// NoConnect: never connect (the start timer).
	NoConnect bool
	// LoadMs waits this long after connecting, before the load.
	LoadMs int
	// Hang never loads.
	Hang bool
	// Log is a file the closers append their names to, in the order they run.
	Log string
	// CloseMs is how long the second closer takes.
	CloseMs int
	// DisconnectAndBlock: after its load, closes its IPC channel and blocks, alive (the server must reap it).
	DisconnectAndBlock bool
	// LocalCalls: 300 ms after its load, this many sequential local calls, then `local calls sent <n>` on stdout.
	LocalCalls int
}

func (b workerBehaviour) json() *delta.JsonObject {
	object := delta.NewJsonObject(0)
	if b.Exit != nil {
		object.Set("exit", float64(*b.Exit))
	}
	if b.Fatal != "" {
		object.Set("fatal", b.Fatal)
	}
	if b.NoConnect {
		object.Set("connect", false)
	}
	if b.LoadMs != 0 {
		object.Set("loadMs", float64(b.LoadMs))
	}
	if b.Hang {
		object.Set("hang", true)
	}
	if b.Log != "" {
		object.Set("log", b.Log)
	}
	if b.CloseMs != 0 {
		object.Set("closeMs", float64(b.CloseMs))
	}
	if b.DisconnectAndBlock {
		object.Set("disconnectAndBlock", true)
	}
	if b.LocalCalls != 0 {
		object.Set("localCalls", float64(b.LocalCalls))
	}
	return object
}

func behaviourOf(value delta.JsonValue) workerBehaviour {
	var b workerBehaviour
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return b
	}
	if code, ok := object.Value("exit").(float64); ok {
		exit := int(code)
		b.Exit = &exit
	}
	b.Fatal, _ = object.Value("fatal").(string)
	if connect, ok := object.Value("connect").(bool); ok && !connect {
		b.NoConnect = true
	}
	if ms, ok := object.Value("loadMs").(float64); ok {
		b.LoadMs = int(ms)
	}
	b.Hang, _ = object.Value("hang").(bool)
	b.Log, _ = object.Value("log").(string)
	if ms, ok := object.Value("closeMs").(float64); ok {
		b.CloseMs = int(ms)
	}
	b.DisconnectAndBlock, _ = object.Value("disconnectAndBlock").(bool)
	if calls, ok := object.Value("localCalls").(float64); ok {
		b.LocalCalls = int(calls)
	}
	return b
}

// countingPort counts every message the worker sends, frames and control messages alike (`process.send` of worker.ts).
type countingPort struct {
	processPort
	sent atomic.Int64
}

func (p *countingPort) Send(message []byte, callback func(err error)) (bool, error) {
	p.sent.Add(1)
	return p.processPort.Send(message, callback)
}

func (p *countingPort) SendControl(payload []byte) bool {
	p.sent.Add(1)
	return p.processPort.SendControl(payload)
}

func (p *countingPort) sendControlThen(payload []byte, then func()) {
	p.sent.Add(1)
	p.processPort.sendControlThen(payload, then)
}

// fixtureCoreModule is the fixture worker's JavaScript core facet (testdata/core.mjs): `Control`, a module the worker's Node host
// imports.
func fixtureCoreModule() plugins.CoreFacet {
	path, err := filepath.Abs(filepath.Join("testdata", "core.mjs"))
	if err != nil {
		panic(err)
	}
	return plugins.CoreFacet{Name: "@test/core", Module: path}
}

// fixtureGoCoreName is the name of the fixture's Go core facet on the worker's Go host and of its proxy on the NodeHost.
const fixtureGoCoreName = "@test/core-go"

// fixtureGoCore is the fixture worker's Go core facet: `Busy` (the app's keep-alive counts: a document, `set` and `pulse`, which
// is busy for a time and clears itself) and `Echo`, the types the worker.ts `core` serves beside `Control`.
func fixtureGoCore() host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(fixtureBusy, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			doc := self.State(fixtureCounts).(*documents.Singleton)
			setTasks := func(n float64, ctx context.Context) error {
				_, err := doc.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("tasks", n) }, ctx)
				return err
			}
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"counts": self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, setTasks(args.(float64), ctx)
					},
					"pulse": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						if err := setTasks(1, ctx); err != nil {
							return nil, err
						}
						// After the call settled (its ctx sealed): the document of this object, which the worker's Go code holds.
						time.AfterFunc(time.Duration(args.(float64))*time.Millisecond, func() { _ = setTasks(0, chordctx.Background()) })
						return nil, nil
					},
				},
			}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(fixtureEcho, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args, nil },
			}}}, nil
		}, host.ServeOptions{})
	})
}

// fixtureOutput forwards the worker's Node host's output as a worker's own, and turns the two lines of `Control.localCalls`
// into the line the test reads: the messages this process sent to the server between them (a local call sends none).
func fixtureOutput(port *countingPort) func(stream plugins.NodeStream, chunk []byte) {
	var begun atomic.Int64
	return func(stream plugins.NodeStream, chunk []byte) {
		out := os.Stdout
		if stream == plugins.NodeStderr {
			out = os.Stderr
		}
		switch strings.TrimSpace(string(chunk)) {
		case "local calls begin":
			begun.Store(port.sent.Load())
		case "local calls end":
			_, _ = fmt.Fprintf(os.Stdout, "local calls sent %d\n", port.sent.Load()-begun.Load())
		default:
			_, _ = out.Write(chunk)
		}
	}
}

func runWorkerFixture() {
	port := &countingPort{processPort: openProcessChannel()}
	var worker host.Host
	serve(port, func(start WorkerStart, ctx context.Context) (*WorkerResult, error) {
		behaviour := behaviourOf(start.Start)
		if behaviour.Exit != nil {
			exitProcess(*behaviour.Exit)
		}
		if behaviour.Fatal != "" {
			return nil, errors.New(behaviour.Fatal)
		}
		// The worker's host is a relay: the NodeHost serves the address (WorkerPlugins.Load routes it), so it has none.
		created, err := host.CreateHost(host.HostOptions{Principal: delta.JsonObjectOf("kind", "worker"), OnError: func(host.HostError) {}})
		if err != nil {
			return nil, err
		}
		worker = created
		start.OnClose(func(context.Context) error {
			if behaviour.Log != "" {
				appendLog(behaviour.Log, "host\n")
			}
			return worker.Close(chordctx.Background())
		})
		start.OnClose(func(context.Context) error {
			if behaviour.CloseMs != 0 {
				time.Sleep(time.Duration(behaviour.CloseMs) * time.Millisecond)
			}
			if behaviour.Log != "" {
				appendLog(behaviour.Log, "store\n")
			}
			return nil
		})
		if behaviour.NoConnect {
			return nil, sleep(ctx, 1_000_000_000*time.Millisecond)
		}
		if err := worker.Connect(start.Parent, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server"), Trusted: true}, ctx); err != nil {
			return nil, err
		}
		if behaviour.LoadMs != 0 {
			if err := sleep(ctx, time.Duration(behaviour.LoadMs)*time.Millisecond); err != nil {
				return nil, err
			}
		}
		if behaviour.Hang {
			return nil, sleep(ctx, 1_000_000_000*time.Millisecond)
		}
		if _, err := start.Plugins.Load(worker, WorkerLoad{
			Kind: "worker", Core: []plugins.CoreFacet{fixtureCoreModule()}, HostKind: fixtureSession,
			GoCore:    []host.HostFacet{{Name: fixtureGoCoreName, Facet: fixtureGoCore()}},
			Bridged:   []plugins.BridgedFacet{{Name: fixtureGoCoreName, Contracts: []*contracts.Contract{fixtureBusy, fixtureEcho}}},
			Principal: delta.JsonObjectOf("kind", "worker"), Peer: delta.JsonObjectOf("kind", "server"),
		}, ctx); err != nil {
			return nil, err
		}
		id, _ := start.Address.Params.Value("id").(string)
		if behaviour.DisconnectAndBlock {
			time.AfterFunc(300*time.Millisecond, func() {
				// Blocks before `serve`'s own disconnect listener runs: the child lives on without its channel.
				port.processPort.(*channel).prependDisconnect(func() { time.Sleep(20 * time.Second) })
				port.Disconnect()
			})
		}
		if calls := behaviour.LocalCalls; calls != 0 {
			time.AfterFunc(300*time.Millisecond, func() {
				control, err := worker.Get(chordctx.Background(), fixtureControl, delta.JsonObjectOf("id", id), host.GetOptions{})
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return
				}
				defer control.Release()
				if _, err := control.Call("localCalls", float64(calls), chordctx.Background()); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			})
		}
		// The app's keep-alive counts are a local type of this Go host: read directly, no hold of the NodeHost's objects.
		busy, err := worker.Get(ctx, fixtureBusy, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &WorkerResult{Busy: busy.Member("counts")}, nil
	}, WorkerMainOptions{
		Node: plugins.NodeOptions{OnOutput: fixtureOutput(port)},
		Redact: func(err any) CrossedError {
			message := fmt.Sprint(err)
			if typed, ok := err.(error); ok {
				message = typed.Error()
			}
			return CrossedError{Message: regexp.MustCompile(`secret\S*`).ReplaceAllString(message, "[redacted]")}
		},
		// Its errors on stderr, as an app logs them: the server keeps the tail private.
		OnError: func(err any) { fmt.Fprintln(os.Stderr, err) },
	})
}

func appendLog(path, text string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(text)
	_ = file.Close()
}

// sleep waits d, or until ctx aborts and returns its cause (`sleep(ms, undefined, { signal })`).
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
