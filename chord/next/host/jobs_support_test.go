package host

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Fixtures of the jobs tests: the declarations the three upstream files share (Pi's `test/next/jobs/*.test.ts` each
// declare them again) and the hosts that serve them.
//
// Go forms. Types are JSON (`any`); an args or result value is a *delta.JsonObject. A promise a test awaits is a
// blocking call; a `Promise.withResolvers` gate is a channel (`gate`), closed once. `vi.useFakeTimers` is a virtual
// loop (`virtualLoop`): the host runs on its own Loop with the fake clock of host_support_test.go for `Now` and
// `AfterFunc`, and the test advances it. A condition the host reaches in a goroutine of its own is awaited by polling
// (`eventually`), never by sleeping for an order.

func mustDefineState(definition *documents.StateDefinition, err error) *documents.StateDefinition {
	if err != nil {
		panic(err)
	}
	return definition
}

func mustDefineJob(definition *JobDefinition, err error) *JobDefinition {
	if err != nil {
		panic(err)
	}
	return definition
}

var jobLogState = mustDefineState(documents.DefineState("app.log", documents.StateOptions{
	Schema:  contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Str()))),
	Version: 1,
	Initial: func() *delta.JsonObject { return jsonObject("list", []any{}) },
}))

var jobProgress = mustDefineState(documents.DefineState("app.progress", documents.StateOptions{
	Schema:  contracts.Obj(contracts.Prop("output", contracts.Str())),
	Version: 1,
	Initial: func() *delta.JsonObject { return jsonObject("output", "") },
}))

// jobEcho is a job without a state document.
var jobEcho = mustDefineJob(DefineJob("app.echo", JobOptions{
	Version: 1, Args: contracts.Obj(contracts.Prop("text", contracts.Str())), Result: contracts.Str(),
}))

// jobShell is a job with a state document and one run at a time.
var jobShell = mustDefineJob(DefineJob("app.shell", JobOptions{
	Version: 1, Args: contracts.Obj(contracts.Prop("command", contracts.Str())),
	Result: contracts.Obj(contracts.Prop("exit", contracts.Int())), State: jobProgress, Concurrency: new(1),
}))

// jobStoreType is the host-lifetime object of most tests: its log document, and the jobs the test declares.
var jobStoreType = mustContract(contracts.Local("app.store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("log", contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Str()))))},
}))

// jobItemType is the same with params: it lives while referenced or running jobs.
var jobItemType = mustContract(contracts.Local("app.item", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("log", contracts.Obj(contracts.Prop("list", contracts.Arr(contracts.Str()))))},
}))

// textArgs is `{ text }`.
func textArgs(text string) *delta.JsonObject { return jsonObject("text", text) }

// commandArgs is `{ command }`.
func commandArgs(command string) *delta.JsonObject { return jsonObject("command", command) }

// textOf is `args.text`.
func textOf(args any) string { return args.(*delta.JsonObject).Value("text").(string) }

// commandOf is `args.command`.
func commandOf(args any) string { return args.(*delta.JsonObject).Value("command").(string) }

// outputOf is `state.output` of an outcome's or a state's value.
func outputOf(state any) string { return state.(*delta.JsonObject).Value("output").(string) }

// gate is `Promise.withResolvers<void>()`: Open settles it once, Wait returns when it is settled.
type gate struct {
	once sync.Once
	done chan struct{}
}

func newGate() *gate { return &gate{done: make(chan struct{})} }

// Open is `resolve()`.
func (g *gate) Open() { g.once.Do(func() { close(g.done) }) }

// Wait is `await gate.promise`.
func (g *gate) Wait() { <-g.done }

// Done is the channel of the promise.
func (g *gate) Done() <-chan struct{} { return g.done }

// gated is a `run` that waits for g, then returns `text`.
func gated(g *gate) func(args any, ctx JobCtx) (any, error) {
	return func(args any, _ JobCtx) (any, error) {
		g.Wait()
		return textOf(args), nil
	}
}

// eventually polls cond until it holds: the host reaches it in goroutines of its own (a run, a finish, a timer job), so
// no channel of the test says when. It fails the test after a generous bound that only a hung host reaches.
func eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

// virtualLoop is the host's loop under fake timers (`vi.useFakeTimers`): sections, microtasks and tasks of the real
// Loop, `Now` and `AfterFunc` of the fake clock, whose timer jobs run as sections.
type virtualLoop struct {
	*Loop
	fake *fakeLoop
}

func newVirtualLoop() *virtualLoop { return &virtualLoop{Loop: NewLoop(), fake: newFakeLoop()} }

// Now is the fake Date.now().
func (l *virtualLoop) Now() int64 { return l.fake.Now() }

// AfterFunc is the fake setTimeout; its job runs as a section.
func (l *virtualLoop) AfterFunc(d time.Duration, job func()) Timer {
	return l.fake.AfterFunc(d, func() { l.Loop.Run(job) })
}

// jobHostOptions is the options of declaredJob: Pi's `loaded` options and fake timers.
type jobHostOptions struct {
	loadedOptions
	Loop *virtualLoop
}

// jobsLoaded is `loaded` over a host that may run on a virtual loop.
func jobsLoaded(t testing.TB, setup func(host FacetHost) error, options jobHostOptions) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	hostOptions := HostOptions{
		Telemetry: options.Telemetry, CloseTimeout: options.CloseTimeout, ReadyTimeout: options.ReadyTimeout,
		Store: options.Store, OnError: errs.push,
	}
	if options.Loop != nil {
		hostOptions.loop = options.Loop
	}
	host := newJobHost(t, hostOptions)
	ctx := options.Context
	if ctx == nil {
		ctx = background
	}
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}, ctx))
	return host, errs
}

// jobFixture is what Pi's `declared()` returns: the object's `self`, its log, the job and the host.
type jobFixture struct {
	t      testing.TB
	Self   Self
	Log    *singletonDoc
	Job    JobHandle
	Host   Host
	Errors *hostErrors
}

// declaredJob loads a host serving jobStoreType with the job definition; implement gets the object's self and log.
func declaredJob(
	t testing.TB, definition *JobDefinition, implement func(self Self, log *singletonDoc) JobImplementation, options jobHostOptions,
) *jobFixture {
	t.Helper()
	found := &jobFixture{t: t}
	host, errs := jobsLoaded(t, func(h FacetHost) error {
		return h.Serve(jobStoreType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			log := stateOf(self, jobLogState)
			handle := self.Job(definition, implement(self, log))
			found.Self, found.Log, found.Job = self, log, handle
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"log": log.Member()},
			}}, nil
		}, ServeOptions{})
	}, options)
	found.Host, found.Errors = host, errs
	return found
}

// appendLog is `(await tx.draft(log)).list.push(entry)`.
func appendLog(tx documents.Tx, log *singletonDoc, entry string) error {
	draft, err := tx.Draft(log.s)
	if err != nil {
		return err
	}
	_, err = draft.Array("list").Push(entry)
	return err
}

// List is `log.value.list`, read in a section.
func (f *jobFixture) List() []any {
	var list []any
	f.Host.(*chordHost).enter(func() { list = f.Log.Value().Value("list").([]any) })
	return list
}

// jobStart is `await job.start(args, ctx, options)`.
func jobStart(t testing.TB, job JobHandle, args any, ctx context.Context, key ...string) Run {
	t.Helper()
	options := StartOptions{}
	if len(key) > 0 {
		options.Key = &key[0]
	}
	run, err := job.Start(args, ctx, options)
	must(t, err)
	return run
}

// runOutcome is `await run.outcome(context)`.
func runOutcome(t testing.TB, run Run) Outcome {
	t.Helper()
	outcome, err := run.Outcome(background)
	must(t, err)
	return outcome.(Outcome)
}

// runOutcomeErr is `await rejection(run.outcome(ctx))`: the error.
func runOutcomeErr(t testing.TB, run Run, ctx context.Context) error {
	t.Helper()
	outcome, err := run.Outcome(ctx)
	if err == nil {
		t.Fatalf("outcome = %+v, want a rejection", outcome)
	}
	return err
}

// jobRunning is `job.running.value`: the entries, as *delta.JsonObject.
func jobRunning(t testing.TB, job JobHandle) []any {
	t.Helper()
	var value any
	var err error
	recordsOf(job).loop.Run(func() { value, err = job.Running().Value() })
	must(t, err)
	return value.([]any)
}

// phases is the `phase` of each entry of `running`.
func phases(entries []any) []any {
	out := make([]any, len(entries))
	for i, entry := range entries {
		out[i] = entry.(*delta.JsonObject).Value("phase")
	}
	return out
}

// expectChordError fails unless err is a ChordError of code (and reason, when not empty).
func expectChordError(t testing.TB, err error, code data.ChordErrorCode, reason data.ChordErrorReason) {
	t.Helper()
	chordErr, ok := data.IsChordError(err)
	if !ok || chordErr.Code != code || (reason != "" && chordErr.Reason != reason) {
		t.Fatalf("error = %v, want %s/%s", err, code, reason)
	}
}

// jobReports is the reported job errors, as `kind: message` (a drop as `job_dropped job`).
func jobReports(errs []HostError) []string {
	var out []string
	for _, err := range errs {
		switch err.Kind {
		case ErrJobFailed, ErrJobFinishFailed:
			out = append(out, fmt.Sprintf("%s: %s", err.Kind, err.Error.Error()))
		case ErrJobDropped:
			out = append(out, fmt.Sprintf("%s %s", err.Kind, err.Job))
		}
	}
	return out
}

// kindsOf is the kind of each reported error.
func kindsOf(errs []HostError) []string {
	out := make([]string, len(errs))
	for i, err := range errs {
		out[i] = string(err.Kind)
	}
	return out
}

// activityCount is `host.activity.value.<name>`.
func activityCount(t testing.TB, host Host, name string) int {
	t.Helper()
	var value any
	var err error
	host.(*chordHost).enter(func() { value, err = host.Activity().Value() })
	must(t, err)
	return int(value.(*delta.JsonObject).Value(name).(float64))
}

// referenceCount is `host.references(typ).value.length`.
func referenceCount(t testing.TB, host Host, typ *contracts.Contract) int {
	t.Helper()
	var value any
	var err error
	host.(*chordHost).enter(func() { value, err = host.References(typ).Value() })
	must(t, err)
	return len(value.([]any))
}

// meteredHost is `metered()`: the telemetry a host counts through and the counts so far.
func meteredHost(t testing.TB) (*telemetry.InMemoryTelemetryContext, context.Context, func() map[string]float64) {
	t.Helper()
	meter := nexttest.Metered(t)
	return meter.Telemetry, meter.Context, meter.Counts
}

// recordsOf is the records behind a job handle.
func recordsOf(handle JobHandle) *jobRecords { return handle.(*job).records }

// timerWait is `await new Promise((resolve) => setTimeout(resolve, d))`: a wait on the loop's timers that ignores any
// abort.
func timerWait(loop eventLoop, d time.Duration) {
	done := make(chan struct{})
	loop.AfterFunc(d, func() { close(done) })
	<-done
}

// newJobHost is CreateHost, failing the test when it refuses the options.
func newJobHost(t testing.TB, options HostOptions) Host {
	t.Helper()
	host, err := CreateHost(options)
	must(t, err)
	return host
}
