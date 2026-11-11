package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
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

// Ports packages/chord/test/node/fixtures/workers/{control,worker}.ts: the fixture contracts (the session kind the
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
// exitProcess in Go code and the NodeHost's own in `Control.crash` (the worker exits with its code). `Control.blockThenCrash`
// is not ported: a Go worker has no single thread to block, and the tests that need an unresponsive worker stop its process from
// outside (freezeWorker, SIGSTOP). `Control.block` takes the path of a file instead of a duration and blocks the NodeHost's event
// loop until the file exists (for 2 s at most), so a test decides when the NodeHost answers again. `Control.fatal` is not ported:
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
			contracts.Meth("block", contracts.NewMethod(contracts.Str())),
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
	// LateCloseMs is how long a closer takes that the app registers after the plugins load (`late` in the log).
	LateCloseMs int
	// HoldNodeStart: the NodeHost's Node subprocess is not started until this file exists (or 20 s passed), so the worker is
	// in the middle of starting its host, its socket directory made and nothing connected, for as long as the test wants.
	HoldNodeStart string
	// SpawnProbe: at its start, a program the worker spawns prints the owner variable it sees (`owner seen by a spawned program: <value>` on stdout).
	SpawnProbe bool
	// BlockNode: 300 ms after its load, blocks the NodeHost's event loop until this file exists (`node blocked` on stdout).
	BlockNode string
	// ShutdownDuringReload: shuts down the moment a reload of the server's is being applied (taken off the queue, in flight).
	ShutdownDuringReload bool
	// DisconnectAndBlock: after its load, closes its IPC channel and blocks, alive (the server must reap it).
	DisconnectAndBlock bool
	// LocalCalls: 300 ms after its load, this many sequential local calls, then `local calls sent <n>` on stdout.
	LocalCalls int
	// CloserPanics: a closer fails, and the app's OnError panics on that failure once (Pi's `onError` that throws).
	CloserPanics bool
	// GoReload: no plugin runtime is loaded; the Go core facet is loaded on the host and the worker's Reload hook answers each
	// reload (appending `reload <script keys>` to Log).
	GoReload bool
	// ProgramOnClose is a file the last closer writes after looking for the program the worker runs from (`present`, or the error):
	// Pi's `lazyOnClose` (a closer's first import of a lazy chunk of the worker base at close).
	ProgramOnClose string
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
	if b.LateCloseMs != 0 {
		object.Set("lateCloseMs", float64(b.LateCloseMs))
	}
	if b.BlockNode != "" {
		object.Set("blockNode", b.BlockNode)
	}
	if b.SpawnProbe {
		object.Set("spawnProbe", true)
	}
	if b.HoldNodeStart != "" {
		object.Set("holdNodeStart", b.HoldNodeStart)
	}
	if b.ShutdownDuringReload {
		object.Set("shutdownDuringReload", true)
	}
	if b.DisconnectAndBlock {
		object.Set("disconnectAndBlock", true)
	}
	if b.LocalCalls != 0 {
		object.Set("localCalls", float64(b.LocalCalls))
	}
	if b.CloserPanics {
		object.Set("closerPanics", true)
	}
	if b.ProgramOnClose != "" {
		object.Set("programOnClose", b.ProgramOnClose)
	}
	if b.GoReload {
		object.Set("goReload", true)
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
	b.CloserPanics, _ = object.Value("closerPanics").(bool)
	b.GoReload, _ = object.Value("goReload").(bool)
	b.ProgramOnClose, _ = object.Value("programOnClose").(string)
	if ms, ok := object.Value("loadMs").(float64); ok {
		b.LoadMs = int(ms)
	}
	b.Hang, _ = object.Value("hang").(bool)
	b.Log, _ = object.Value("log").(string)
	if ms, ok := object.Value("closeMs").(float64); ok {
		b.CloseMs = int(ms)
	}
	if ms, ok := object.Value("lateCloseMs").(float64); ok {
		b.LateCloseMs = int(ms)
	}
	b.BlockNode, _ = object.Value("blockNode").(string)
	b.SpawnProbe, _ = object.Value("spawnProbe").(bool)
	b.HoldNodeStart, _ = object.Value("holdNodeStart").(string)
	b.ShutdownDuringReload, _ = object.Value("shutdownDuringReload").(bool)
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
				_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) { return nil, draft.Set("tasks", n) })
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

// holdNodeGate is the file HoldNodeStart names, once the worker knows its behaviour.
var holdNodeGate atomic.Pointer[string]

// holdNodeStart is the plugins' Command in the worker: the Node subprocess of the NodeHost (the one that runs host.mjs, not the
// version check) waits for holdNodeGate to exist first.
func holdNodeStart(ctx context.Context, name string, args ...string) *exec.Cmd {
	gate := holdNodeGate.Load()
	if gate == nil || !slices.ContainsFunc(args, func(arg string) bool { return strings.HasSuffix(arg, "host.mjs") }) {
		return exec.CommandContext(ctx, name, args...)
	}
	const wait = `gate=$1; shift; i=0; while [ ! -e "$gate" ] && [ $i -lt 400 ]; do i=$((i+1)); sleep 0.05; done; exec "$@"`
	return exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", wait, "sh", *gate, name}, args...)...)
}

// goReloadLog is the file the GoReload behaviour's Reload hook appends to.
var goReloadLog atomic.Pointer[string]

// onErrorThrew is whether the fixture's OnError threw already.
var onErrorThrew atomic.Bool

func runWorkerFixture() {
	if os.Getenv(fixtureReportExecutable) != "" {
		executable, _ := os.Executable()
		_, _ = fmt.Fprintf(os.Stdout, "executable %s\n", executable)
	}
	if os.Getenv(fixtureReportArgs) != "" {
		_, _ = fmt.Fprintf(os.Stdout, "args %q\n", os.Args[1:])
	}
	if os.Getenv(fixtureReportCompileCache) != "" {
		_, _ = fmt.Fprintf(os.Stdout, "compilecache %q\n", os.Getenv("NODE_COMPILE_CACHE"))
	}
	port := &countingPort{processPort: openProcessChannel()}
	var worker host.Host
	serve(port, func(start WorkerStart, ctx context.Context) (*WorkerResult, error) {
		behaviour := behaviourOf(start.Start)
		if behaviour.HoldNodeStart != "" {
			holdNodeGate.Store(&behaviour.HoldNodeStart)
		}
		if behaviour.SpawnProbe {
			seen, err := exec.Command("/bin/sh", "-c", `printf %s "${`+plugins.SocketOwnerEnv+`-unset}"`).Output()
			if err != nil {
				return nil, err
			}
			_, _ = fmt.Fprintf(os.Stdout, "owner seen by a spawned program: %s\n", seen)
		}
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
		// Registered first, so it runs last: after the other closers, the program the worker runs from is still there (Pi's closer that
		// imports a lazy chunk of the base at close, `lazyOnClose`: the base's files stay until every worker exited).
		start.OnClose(func(context.Context) error {
			if behaviour.ProgramOnClose == "" {
				return nil
			}
			report := "present"
			if _, err := os.Stat(os.Args[0]); err != nil {
				report = err.Error()
			}
			return os.WriteFile(behaviour.ProgramOnClose, []byte(report), 0o600)
		})
		start.OnClose(func(context.Context) error {
			if behaviour.Log != "" {
				appendLog(behaviour.Log, "host\n")
			}
			return worker.Close(chordctx.Background())
		})
		start.OnClose(func(context.Context) error {
			if behaviour.CloserPanics {
				return errors.New("closer-boom")
			}
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
		if behaviour.GoReload {
			goReloadLog.Store(&behaviour.Log)
			if _, err := worker.Load([]host.HostFacet{{Name: fixtureGoCoreName, Facet: fixtureGoCore()}}, ctx); err != nil {
				return nil, err
			}
			busy, err := worker.Get(ctx, fixtureBusy, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			return &WorkerResult{Busy: host.Expose(busy.Member("counts"))}, nil
		}
		if _, err := start.Plugins.Load(ctx, worker, WorkerLoad{
			Kind: "worker", NodeCore: []plugins.CoreFacet{fixtureCoreModule()}, HostKind: fixtureSession,
			Core:      []host.HostFacet{{Name: fixtureGoCoreName, Facet: fixtureGoCore()}},
			Bridged:   []plugins.BridgedFacet{{Name: fixtureGoCoreName, Contracts: []*contracts.Contract{fixtureBusy, fixtureEcho}}},
			Principal: delta.JsonObjectOf("kind", "worker"), Peer: delta.JsonObjectOf("kind", "server"),
		}); err != nil {
			return nil, err
		}
		if behaviour.LateCloseMs != 0 {
			start.OnClose(func(context.Context) error {
				time.Sleep(time.Duration(behaviour.LateCloseMs) * time.Millisecond)
				if behaviour.Log != "" {
					appendLog(behaviour.Log, "late\n")
				}
				return nil
			})
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
		if release := behaviour.BlockNode; release != "" {
			time.AfterFunc(300*time.Millisecond, func() {
				control, err := worker.Get(chordctx.Background(), fixtureControl, delta.JsonObjectOf("id", id), host.GetOptions{})
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return
				}
				// Held until the process exits: a release is a message to the NodeHost, which is blocked once the call returns.
				if _, err := control.Call("block", release, chordctx.Background()); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			})
		}
		if behaviour.ShutdownDuringReload {
			w := start.Plugins.worker
			applying := make(chan struct{})
			var once sync.Once
			w.mu.Lock()
			w.reloadApplying = func() { once.Do(func() { close(applying) }) }
			w.mu.Unlock()
			//portlint:allow golifetime the wait ends with the worker's context, which the shutdown it makes aborts
			go func() {
				select {
				case <-applying:
					w.shutdown()
				case <-ctx.Done():
				}
			}()
		}
		// The app's keep-alive counts are a local type of this Go host: read directly, no hold of the NodeHost's objects.
		busy, err := worker.Get(ctx, fixtureBusy, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &WorkerResult{Busy: host.Expose(busy.Member("counts"))}, nil
	}, WorkerMainOptions{
		Node: plugins.NodeOptions{OnOutput: fixtureOutput(port), Command: holdNodeStart},
		Reload: func(scripts []plugins.PluginScript, _ context.Context) (host.ReloadReport, error) {
			keys := make([]string, len(scripts))
			for index, script := range scripts {
				keys[index] = scriptKey(script)
			}
			if log := goReloadLog.Load(); log != nil {
				appendLog(*log, "reload "+strings.Join(keys, ",")+"\n")
			}
			return host.ReloadReport{Plugins: host.PluginChanges{Changed: []string{scripts[0].Plugin.Name}}}, nil
		},
		Redact: func(err any) CrossedError {
			message := fmt.Sprint(err)
			if typed, ok := err.(error); ok {
				message = typed.Error()
			}
			return CrossedError{Message: regexp.MustCompile(`secret\S*`).ReplaceAllString(message, "[redacted]")}
		},
		// Its errors on stderr, as an app logs them: the server keeps the tail private.
		OnError: func(err any) {
			fmt.Fprintln(os.Stderr, err)
			// The first report of a failed closer throws; `fatal`'s own report of it then goes through.
			if strings.Contains(fmt.Sprint(err), "closer-boom") && !onErrorThrew.Swap(true) {
				panic("onError throws")
			}
		},
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
