package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/next-node/fixtures/workers/{control,worker}.ts: the fixture contracts (the session kind the
// workers serve, the fixture worker's `Control`, the plugin's `Notes`), the worker's start data (how it behaves), and the
// fixture worker itself: `WorkerMain` with a host at its address, the shipped plugins, and a core facet serving `Control`
// (crash, block, busy, local calls) and `Busy` (the app's keep-alive counts). Its start data picks a behaviour for the tests:
// exit or fail before connecting, never connect, a slow or endless load, slow closers that log their order to a file.
//
// Forced Go differences. A worker is the test binary run again (TestMain, fixtureEnv "worker"). `process.exit` is
// exitProcess; an error `fn` returns is Pi's throw. A blocked event loop is the worker process stopped for the time Pi's
// busy loop takes (freeze), so the worker answers nothing, a ping and a shutdown included. `Control.fatal` is not ported: a throw from a timer is a panic in a goroutine of the
// app, which ends a Go process with its own exit status (workers_main.go), and no test calls it. Every message the worker
// sends (frame or control) is counted by wrapping its port.

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
			contracts.Meth("block", contracts.NewMethod(contracts.Int())),
			contracts.Meth("blockThenCrash", contracts.NewMethod(contracts.Int())),
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

	fixtureBusy = mustFixture(contracts.Local("test.busy", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("counts", contracts.Map(contracts.Int())), contracts.Mem("set", contracts.NewMethod(contracts.Int()))}}))
	fixtureEcho = mustFixture(contracts.Local("test.echo", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("echo", contracts.NewMethod(contracts.Int(), contracts.Int()))}}))

	fixtureCounts = mustFixture(documents.DefineState("counts", documents.StateOptions{
		Schema:  contracts.Map(contracts.Int()),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("tasks", float64(0)) },
	}))
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

// fixtureEvaluator is the plugin runtime's evaluation seam of the fixture worker: the `@test/notes` plugin's worker facet
// (test/next-node/fixtures/workers/plugins/notes/worker.ts), serving `Notes` at the worker's address with the label its
// source carries. A source that begins `throw` is a script whose evaluation fails.
type fixtureEvaluator struct{}

var (
	throwing   = regexp.MustCompile(`^throw `)
	labelConst = regexp.MustCompile(`export const label = ("(?:[^"\\]|\\.)*");`)
)

// labelOfSource is the label the fixture plugin's `label.ts` declares.
func labelOfSource(source string) (string, error) {
	match := labelConst.FindStringSubmatch(source)
	if match == nil {
		return "", errors.New("evaluation failed: no label")
	}
	var label string
	if err := json.Unmarshal([]byte(match[1]), &label); err != nil {
		return "", err
	}
	return label, nil
}

func (fixtureEvaluator) EvaluateFacet(built plugins.BuiltFacet, options plugins.EvaluateOptions) (host.Facet, error) {
	if throwing.MatchString(built.Source) {
		return host.Facet{}, errors.New("evaluation failed")
	}
	label, err := labelOfSource(built.Source)
	if err != nil {
		return host.Facet{}, err
	}
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(fixtureNotes, func(params *delta.JsonObject, _ data.Principal, _ host.Self) (contracts.Implementation, error) {
			id, _ := params.Value("id").(string)
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"label": host.Expose(state.NewConstant(id + " " + label)),
			}}}, nil
		}, host.ServeOptions{})
	}), nil
}

func runWorkerFixture() {
	port := &countingPort{processPort: openProcessChannel()}
	fixturePort = port
	var worker host.Host
	serve(port, func(start WorkerStart, ctx context.Context) (*WorkerResult, error) {
		behaviour := behaviourOf(start.Start)
		if behaviour.Exit != nil {
			exitProcess(*behaviour.Exit)
		}
		if behaviour.Fatal != "" {
			return nil, errors.New(behaviour.Fatal)
		}
		address := start.Address
		created, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "worker"), OnError: func(host.HostError) {}})
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
		if _, err := start.Plugins.Load(worker, WorkerLoad{Kind: "worker", Core: []host.HostFacet{{Name: "@test/core", Facet: fixtureCore(worker)}}}, ctx); err != nil {
			return nil, err
		}
		if behaviour.DisconnectAndBlock {
			time.AfterFunc(300*time.Millisecond, func() {
				// Blocks before `serve`'s own disconnect listener runs: the child lives on without its channel.
				port.processPort.(*channel).prependDisconnect(func() { freeze(20 * time.Second) })
				port.Disconnect()
			})
		}
		if calls := behaviour.LocalCalls; calls != 0 {
			time.AfterFunc(300*time.Millisecond, func() {
				echo, err := worker.Get(fixtureEcho, delta.JsonObjectOf(), chordctx.Background(), host.GetOptions{})
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return
				}
				before := port.sent.Load()
				for index := range calls {
					if _, err := echo.Call("echo", float64(index), chordctx.Background()); err != nil {
						fmt.Fprintln(os.Stderr, err)
						return
					}
				}
				_, _ = fmt.Fprintf(os.Stdout, "local calls sent %d\n", port.sent.Load()-before)
				echo.Release()
			})
		}
		busy, err := worker.Get(fixtureBusy, delta.JsonObjectOf(), ctx, host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &WorkerResult{Busy: busy.Member("counts")}, nil
	}, WorkerMainOptions{
		Evaluator: fixtureEvaluator{},
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

// fixtureCore is worker.ts's core facet: `Busy`, `Echo` and `Control`.
func fixtureCore(worker host.Host) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(fixtureBusy, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			counts := self.State(fixtureCounts).(*documents.Singleton)
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"counts": self.Derive([]any{counts}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						_, err := counts.Change(func(draft *delta.Object) (any, error) { return nil, draft.Set("tasks", args) }, ctx)
						return nil, err
					},
				},
			}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(fixtureEcho, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args, nil },
			}}}, nil
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(fixtureControl, func(_ *delta.JsonObject, _ data.Principal, self host.Self) (contracts.Implementation, error) {
			busy := self.Get(fixtureBusy, delta.JsonObjectOf())
			echo := self.Get(fixtureEcho, delta.JsonObjectOf())
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"crash": func(code delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					time.AfterFunc(10*time.Millisecond, func() { os.Exit(int(code.(float64))) })
					return nil, nil
				},
				"block": func(ms delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					blockLater(ms, false)
					return nil, nil
				},
				"blockThenCrash": func(ms delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					blockLater(ms, true)
					return nil, nil
				},
				"busy": func(ms delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					if _, err := busy.Call("set", float64(1), ctx); err != nil {
						return nil, err
					}
					// After the call settled (its ctx sealed, maybe this object ended): through the host.
					time.AfterFunc(time.Duration(ms.(float64))*time.Millisecond, func() {
						counts, err := worker.Get(fixtureBusy, delta.JsonObjectOf(), chordctx.Background(), host.GetOptions{})
						if err != nil {
							return
						}
						_, _ = counts.Call("set", float64(0), chordctx.Background())
						counts.Release()
					})
					return nil, nil
				},
				"localCalls": func(n delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					before := fixturePort.sent.Load()
					for index := range int(n.(float64)) {
						if _, err := echo.Call("echo", float64(index), ctx); err != nil {
							return nil, err
						}
					}
					return float64(fixturePort.sent.Load() - before), nil
				},
			}}}, nil
		}, host.ServeOptions{})
	})
}

// fixturePort is the fixture worker's counting port, for `Control.localCalls`.
var fixturePort *countingPort

// blockLater is `setTimeout(() => { busy loop for ms; (then process.exit(9)) }, 10)`: the whole worker process frozen
// (freeze), as a blocked JavaScript thread leaves nothing of Node running: no heartbeat, no message, no signal handler, no
// timer.
func blockLater(ms delta.JsonValue, crash bool) {
	time.AfterFunc(10*time.Millisecond, func() {
		freeze(time.Duration(ms.(float64)) * time.Millisecond)
		if crash {
			os.Exit(9)
		}
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
