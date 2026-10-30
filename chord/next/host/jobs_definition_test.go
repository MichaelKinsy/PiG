package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports packages/chord/test/next/jobs/definition.test.ts: what DefineJob checks at declaration (names, versions, closed
// JSON args, a singleton state, concurrency, the shape of accepts, persist) and what it gives.
//
// Type-level checks (`expectTypeOf(...).toEqualTypeOf<JobDefinition<...>>()`) are designed out: Go's JobDefinition is
// not generic (jobs_definition.go), its args, result and state are JSON values, and the compiler checks the one type.
// Checks of what a JavaScript caller may pass that Go's types cannot express (`defineJob(42 as never, ...)`, an unknown
// option key, an accepts adapter with an extra key, a non-integer concurrency, a name with a lone surrogate) are
// designed out by the same mechanism: a Go signature takes a string, a struct with the declared fields, an int and a
// struct with Args and Result, and a Go string holds no lone surrogate.

func jobsProgress(t *testing.T) *documents.StateDefinition {
	t.Helper()
	definition, err := documents.DefineState("app.progress", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("output", contracts.Str())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("output", "") },
	})
	must(t, err)
	return definition
}

func jobsKeyedState(t *testing.T) *documents.StateDefinition {
	t.Helper()
	definition, err := documents.DefineState("app.keyed", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("output", contracts.Str())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("output", "") },
		Keyed:   true,
	})
	must(t, err)
	return definition
}

func jobArgs() contracts.TSchema { return contracts.Obj(contracts.Prop("command", contracts.Str())) }

func expectDefineError(t *testing.T, name string, options JobOptions, want string) {
	t.Helper()
	_, err := DefineJob(name, options)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("DefineJob(%q) = %v, want an error containing %q", name, err, want)
	}
}

func mustJob(t *testing.T, name string, options JobOptions) *JobDefinition {
	t.Helper()
	definition, err := DefineJob(name, options)
	must(t, err)
	return definition
}

// definition.test.ts: defineJob keeps its name, version, Chord's copies of its schemas, its state and concurrency
func TestDefineJobKeepsItsNameVersionChordsCopiesOfItsSchemasItsStateAndConcurrency(t *testing.T) {
	progress := jobsProgress(t)
	args := jobArgs()
	shell := mustJob(t, "app.shell", JobOptions{Version: 2, Args: args, Result: contracts.Int(), State: progress, Concurrency: new(1)})
	if shell.Name != "app.shell" || shell.Version != 2 || shell.State != progress || shell.Concurrency == nil || *shell.Concurrency != 1 || len(shell.Accepts) != 0 {
		t.Fatalf("shell = %+v", shell)
	}
	if shell.Args.Schema.Node == args.Node {
		t.Fatal("Args.Schema is the caller's schema, not Chord's copy")
	}
	plain := mustJob(t, "app.plain", JobOptions{Version: 1, Args: args, Result: contracts.Str()})
	if plain.State != nil || plain.Concurrency != nil {
		t.Fatalf("plain = %+v", plain)
	}
}

// definition.test.ts: defineJob takes a restricted name only: lowercase [a-z0-9@/._~-]
func TestDefineJobTakesARestrictedNameOnly(t *testing.T) {
	for _, name := range []string{"@app/jobs.run_1~x-y", "a"} {
		if got := mustJob(t, name, JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int()}).Name; got != name {
			t.Fatalf("name = %q, want %q", got, name)
		}
	}
	for _, name := range []string{"", "App.run", "app:run", "app run", "a\x00b", "é"} {
		quoted, err := json.Marshal(name)
		must(t, err)
		expectDefineError(t, name, JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int()},
			"A job name "+string(quoted)+": expected lowercase [a-z0-9@/._~-]")
	}
}

// definition.test.ts: defineJob refuses a bad name, version, unknown option or concurrency
func TestDefineJobRefusesABadNameVersionOrConcurrency(t *testing.T) {
	expectDefineError(t, "", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int()}, "A job name")
	expectDefineError(t, "j", JobOptions{Version: 0, Args: jobArgs(), Result: contracts.Int()}, "j.version: expected a positive integer")
	for _, concurrency := range []int{0, -1} {
		expectDefineError(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int(), Concurrency: new(concurrency)},
			"j.concurrency: expected a positive integer")
	}
}

// definition.test.ts: defineJob requires closed JSON args, as a method's: open objects and bytes are refused; records are fine
func TestDefineJobRequiresClosedJSONArgs(t *testing.T) {
	open := contracts.TSchema{Node: delta.JsonObjectOf("type", "object", "properties", delta.JsonObjectOf("a", contracts.Str().Node))}
	expectDefineError(t, "j", JobOptions{Version: 1, Args: open, Result: contracts.Int()}, "argument objects must be closed")
	expectDefineError(t, "j", JobOptions{Version: 1, Args: contracts.Obj(contracts.Prop("data", contracts.Bytes())), Result: contracts.Int()},
		"j.args: job arguments are JSON")
	mustJob(t, "j", JobOptions{Version: 1, Args: contracts.Obj(contracts.Prop("tags", contracts.Map(contracts.Str()))), Result: contracts.Unknown()})
}

// definition.test.ts: defineJob takes a singleton document definition as its state
func TestDefineJobTakesASingletonDocumentDefinitionAsItsState(t *testing.T) {
	expectDefineError(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int(), State: jobsKeyedState(t)},
		"j.state: expected a singleton document definition")
}

// definition.test.ts: defineJob checks accepts at declaration: older versions only, each with an args and a result function
func TestDefineJobChecksAcceptsAtDeclaration(t *testing.T) {
	adapter := JobAdapter{
		Args:   func(old any) (any, error) { return jsonObject("command", old.(*delta.JsonObject).Value("cmd")), nil },
		Result: func(old any) (any, error) { return old, nil },
	}
	mustJob(t, "j", JobOptions{Version: 3, Args: jobArgs(), Result: contracts.Int(), Accepts: JobAccepts{1: adapter, 2: adapter}})
	expectDefineError(t, "j", JobOptions{Version: 2, Args: jobArgs(), Result: contracts.Int(), Accepts: JobAccepts{2: adapter}},
		"j.accepts[2]: not an older version")
	expectDefineError(t, "j", JobOptions{Version: 2, Args: jobArgs(), Result: contracts.Int(), Accepts: JobAccepts{0: adapter}},
		"j.accepts[0]: not an older version")
	expectDefineError(t, "j", JobOptions{Version: 2, Args: jobArgs(), Result: contracts.Int(), Accepts: JobAccepts{1: {Args: adapter.Args}}},
		"j.accepts[1].result: expected a function")
}

// definition.test.ts: defineJob takes persist with a resume rule, a persisted state and a JSON-only result (slice 6b §4)
func TestDefineJobTakesPersistWithAResumeRuleAPersistedStateAndAJSONOnlyResult(t *testing.T) {
	persistedState, err := documents.DefineState("p.state", documents.StateOptions{
		Schema:         contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version:        1,
		Initial:        func() *delta.JsonObject { return jsonObject("n", float64(0)) },
		Persist:        true,
		CheckpointWhen: func(*documents.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
	})
	must(t, err)
	inMemory, err := documents.DefineState("m.state", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	must(t, err)
	definition := mustJob(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int(), State: persistedState, Persist: &JobPersistence{Resume: ResumeRerun}})
	if definition.Persist == nil || *definition.Persist != (JobPersistence{Resume: ResumeRerun}) {
		t.Fatalf("persist = %+v", definition.Persist)
	}
	expectDefineError(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int(), State: inMemory, Persist: &JobPersistence{Resume: ResumeAbandon}},
		"a persisted job's state is a persisted document")
	expectDefineError(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Bytes(), Persist: &JobPersistence{Resume: ResumeRerun}},
		"a persisted job's result is JSON")
	// An in-memory job may return bytes.
	mustJob(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Bytes()})
	expectDefineError(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int(), Persist: &JobPersistence{}},
		`expected "rerun" or "abandon"`)
}

// definition.test.ts: defineJob fingerprints args, result and state: a schema change without a version bump changes it
func TestDefineJobFingerprintsArgsResultAndState(t *testing.T) {
	one := mustJob(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int()})
	if same := mustJob(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Int()}); same.Fingerprint != one.Fingerprint {
		t.Fatalf("same declaration, fingerprints %s and %s", same.Fingerprint, one.Fingerprint)
	}
	if changed := mustJob(t, "j", JobOptions{Version: 1, Args: jobArgs(), Result: contracts.Str()}); changed.Fingerprint == one.Fingerprint {
		t.Fatal("a changed result schema kept the fingerprint")
	}
}

// Names the mechanism that carries the type-level checks of the jobs tests (definition.test.ts `expectTypeOf(Shell)`,
// job.test.ts `expectTypeOf(ctx)` and `Outcome<...>["args"]`): Pi's JobDefinition<A, R, S>, JobHandle<A, R, S>,
// JobCtx<P, S> and Outcome<A, R, S> are generic over JSON shapes TypeScript checks at compile time; the Go types are one
// concrete shape each (args, results and states are JSON values) and the compiler checks that the concrete types satisfy
// the interfaces the layers share.
func TestJobTypesAreOneConcreteShapeTheCompilerChecks(t *testing.T) {
	var (
		_ documents.JobHandle = (*job)(nil)
		_ JobHandle           = (*job)(nil)
		_ documents.Run       = (*runHandle)(nil)
		_ JobCtx              = (*jobContext)(nil)
		_ RunState            = (*runState)(nil)
	)
	// The JSON members are `any`: an Outcome holds whatever the run returned, its args and its state as JSON values.
	outcome := Outcome{Args: textArgs("a"), State: jsonObject("output", ""), Result: "r"}
	if textOf(outcome.Args) != "a" || outputOf(outcome.State) != "" || outcome.Result != "r" {
		t.Fatalf("outcome = %+v", outcome)
	}
	// A job without a state has no `ctx.State()`: Pi's `JobCtx<P, undefined>` has no `state` member.
	if (&jobContext{}).State() != nil {
		t.Fatal("a job context without a state has a State")
	}
}
