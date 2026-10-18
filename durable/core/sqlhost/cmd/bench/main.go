// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command bench is the native candidate of the Durable conformance gate (durable/contract/impls/native.mjs): the bench
// scenario (durable/contract/scenarios/bench.mjs) run through the native session on the core, with its statements written
// as a trace of statements only (contracttest.Writer), which the Node tools replay onto the starting store.
//
//	bench --db <store> --trace <out> [--seed N] [--turns N] [--tools N] [--variant full|incr] [--stream CHARS]
//	      [--partial-ms MS] [--fault retryable-error] [--payload-kb K] [--parallel] [--recover] [--label L]
//	bench --db <store> --trace <out> --record <recording> ...   also records what the core was given (durable/core/contracttest/counts)
//	bench --db <fixture copy> --timing <results.jsonl> --size N --sample I --version C [--turns 10] [--tools 8] [--cpuprofile FILE]
//	bench seed DIR SIZE [TARGET]
//	bench sample FIXTURE SIZE [INDEX] [TARGET] [VERSION]
//
// seed and sample are durable/interop/bench.mjs's commands for the native core (k8s.go), so a benchmark profile measures
// both the same way (bench/k8s, profile durable-core).
//
// --timing measures instead of tracing (the benchmark report's native build, bench/durable/report/RUNNER-SPEC.md): it runs
// on the real clock with no statement trace, checks the fixture's seed fingerprint and appends one durable-bench
// bench/run.ts line per run (timingLine). --cpuprofile writes the pprof CPU profile of that measured run.
//
// CONTRACT_CRASH_AFTER=<seq> kills the process right after the commit that consumed that seq (contracttest.Writer).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime/pprof"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/counts"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest/guard"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// lookupSchema is bench.mjs's lookup tool, Type.Object({ n: Type.Number() }), in the key order TypeBox gives it and Pi
// stores (type, required, properties): decoding the JSON keeps that order, so the stored declaration does not change.
var lookupSchema = func() ai.ToolSchema {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"lookup","description":"Look up record number n","parameters":{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}}`), &schema); err != nil {
		panic(err)
	}
	return schema
}()

type options struct {
	db, trace, variant, fault, label string
	record                           string
	seed, turns, tools, stream       int
	payloadKB                        int
	partialMs                        float64
	parallel, recoverRun, subagent   bool
	timing, target, version          string
	// workload is workloadStandard (compaction off), workloadSummary or workloadSummaryBlocking (k8s.go, BENCH_WORKLOAD).
	workload     string
	cpuProfile   string
	size, sample int
	// observe sees every effect the core asks for (tests).
	observe func(abi.Effect)
	// seedFrom is the first history turn --seed writes (k8s seed batches continue a store); finish, when set, ends a run
	// that is neither traced nor timed (k8s.go) and receives what it measured.
	seedFrom int
	finish   func(context.Context, harness.Harness, *script, measured) error
}

// measured is what a run measured: the open (store to open root conversation) and each measured turn, in wall time and
// in process CPU time (user + system, every thread), all in milliseconds.
type measured struct {
	open, openCPU   float64
	turns, turnsCPU []float64
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "seed" || os.Args[1] == "sample") {
		if err := k8sMain(context.Background(), os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			os.Exit(1)
		}
		return
	}
	var o options
	flag.StringVar(&o.db, "db", "", "store file, opened in place")
	flag.StringVar(&o.trace, "trace", "", "statement trace to write")
	flag.StringVar(&o.variant, "variant", "full", "scripted model: full or incr")
	flag.StringVar(&o.fault, "fault", "", "retryable-error: the second model call fails once")
	flag.StringVar(&o.label, "label", "m", "measured turn label")
	flag.IntVar(&o.seed, "seed", 0, "turns of history written before the measured turns")
	flag.IntVar(&o.turns, "turns", 0, "measured turns")
	flag.IntVar(&o.tools, "tools", measuredTools, "lookups per measured turn")
	flag.IntVar(&o.stream, "stream", 0, "answer length in characters (W3)")
	flag.IntVar(&o.payloadKB, "payload-kb", 0, "lookup result size in KiB (W4c)")
	flag.Float64Var(&o.partialMs, "partial-ms", 0, "progress.partialIntervalMs")
	flag.BoolVar(&o.parallel, "parallel", false, "announce a turn's lookups in one round (W4d)")
	flag.BoolVar(&o.subagent, "subagent", false, "each measured turn delegates to a foreground sub-agent (W4e)")
	flag.BoolVar(&o.recoverRun, "recover", false, "let the interrupted turn finish and stop")
	flag.StringVar(&o.record, "record", "", "also record the core's events, reads and uuids to this file (the count gate's replay input)")
	flag.StringVar(&o.timing, "timing", "", "measure: append one durable-bench result line to this file (no trace, real clock)")
	flag.StringVar(&o.target, "target", "pig-native", "timing: the result line's target")
	flag.StringVar(&o.version, "version", "", "timing: the result line's version (the core commit)")
	flag.IntVar(&o.size, "size", 0, "timing: the fixture's history size in turns")
	flag.IntVar(&o.sample, "sample", 0, "timing: the sample number")
	flag.StringVar(&o.cpuProfile, "cpuprofile", "", "timing: write a CPU profile of the measured run to this file")
	flag.Parse()
	// A run that outgrows the heap bound, or whose parent is gone, ends itself (the row's timeout kills only the parent).
	guardOptions := guard.FromEnv("bench")
	guardOptions.Step = 0
	guard.Start(guardOptions)
	if o.timing != "" {
		// A measured run is the production host on the real clock: no synctest bubble, no settling, no trace.
		if o.cpuProfile != "" {
			f, err := os.Create(o.cpuProfile)
			if err == nil {
				err = pprof.StartCPUProfile(f)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "bench:", err)
				os.Exit(1)
			}
		}
		err := run(context.Background(), o)
		pprof.StopCPUProfile()
		if err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
			os.Exit(1)
		}
		return
	}
	// The reference runs under detenv: its clock moves only when everything else is blocked on a timer. A synctest bubble gives
	// the Go run the same time: the time package is fake inside it and advances when every goroutine is durably blocked.
	// testing.Main supplies the *testing.T synctest.Test needs; a failure is reported as bench's own error and exit status.
	testing.Main(func(string, string) (bool, error) { return true, nil }, []testing.InternalTest{{Name: "Bench", F: func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			if err := run(t.Context(), o); err != nil {
				fmt.Fprintln(os.Stderr, "bench:", err)
				os.Exit(1)
			}
		})
	}}}, nil, nil)
}

// observing shows each effect of a step to a callback before the host runs it.
type observing struct {
	sqlhost.Stepper
	seen func(abi.Effect)
}

func (o observing) Step(ev abi.Event) *abi.Step {
	step := o.Stepper.Step(ev)
	if step != nil {
		for _, e := range step.Effects {
			o.seen(e)
		}
	}
	return step
}

func params(values []abi.Value) []contracttest.Param {
	out := make([]contracttest.Param, len(values))
	for i, v := range values {
		switch v.Kind {
		case abi.ValueNull:
			out[i] = nil
		case abi.ValueInt:
			out[i] = v.Int
		case abi.ValueFloat:
			out[i] = v.Float
		case abi.ValueText:
			out[i] = string(v.Bytes)
		default:
			out[i] = append([]byte(nil), v.Bytes...)
		}
	}
	return out
}

func run(ctx context.Context, o options) (err error) {
	timed := o.timing != "" || o.finish != nil
	switch {
	case o.finish != nil && (o.trace != "" || o.timing != "" || o.recoverRun):
		return errors.New("a k8s run takes no --trace, --timing or --recover")
	case o.db == "":
		return errors.New("--db is required")
	case !timed && o.trace == "":
		return errors.New("--trace or --timing is required")
	case timed && o.record != "":
		return errors.New("--record needs the deterministic run: it takes --trace, not --timing")
	case o.finish == nil && timed && (o.trace != "" || o.version == "" || o.size <= 0 || o.turns <= 0 || o.seed != 0 || o.recoverRun):
		return errors.New("--timing takes --version, --size and --turns, and no --trace, --seed or --recover")
	}
	var w *contracttest.Writer
	if !timed {
		var err error
		if w, err = contracttest.NewWriter(o.trace); err != nil {
			return err
		}
		if err := w.Meta("bench", "native"); err != nil {
			return err
		}
		if err := w.Store("s0", o.db); err != nil {
			return err
		}
	}
	opened, openedCPU := time.Now(), processCPU()
	db, err := sqlhost.OpenDB(ctx, o.db)
	if err != nil {
		return err
	}

	epoch, rng := detenvClock()
	drawRNG = rng
	if o.stream > 0 {
		fauxChunkChars = 8 * 4
	}
	window := 1_000_000_000
	if summarizes(o.workload) {
		window = summaryWindow
	}
	faux := ai.FauxConfig{API: ai.API(fauxAPI(epoch, rng.next())), Models: []ai.FauxModelDefinition{{ID: "scripted-1", ContextWindow: window}}, TokenSize: &ai.FauxTokenSize{Min: new(1_000_000_000), Max: new(1_000_000_000)}}
	if o.stream > 0 {
		faux.TokenSize, faux.TokensPerSecond = &ai.FauxTokenSize{Min: new(8), Max: new(8)}, 400
	}
	provider := ai.NewFauxProvider(faux)
	sc := &script{variant: o.variant, fault: o.fault}
	if summarizes(o.workload) {
		sc.summary = summaryText
	}
	if w != nil {
		sc.record = w.Model
	}
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(sc.respond)})
	// A factory step answers one call; the script re-arms itself at the start of each (workload.mjs respond).
	sc.rearm = func() { provider.AppendResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(sc.respond)}) }
	models := ai.CreateModels()
	models.SetProvider(provider.Provider())

	registry := harness.CreateRegistry()
	lookup := new(durable.ToolRegistration)
	*lookup = durable.ToolRegistration{
		ToolSchema: lookupSchema,
		Execute: func(_ context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			n := 0
			if m, ok := args.(map[string]any); ok {
				if f, ok := m["n"].(float64); ok {
					n = int(f)
				}
			}
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: payload(n, o.payloadKB)}}}, nil
		},
	}
	untagged := false
	if err := registry.Install(&durable.Extension{
		Name:  "bench",
		Tools: []*durable.ToolRegistration{lookup},
		Sections: []*durable.PromptSection{harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
			s := system
			return &s, nil
		}, harness.SectionOptions{Tag: &untagged})},
	}); err != nil {
		return err
	}

	if o.subagent {
		if err := registry.Install(subagentExtension()); err != nil {
			return err
		}
	}

	settings := harness.HarnessSettings{Compaction: &harness.CompactionPolicyPatch{Enabled: new(summarizes(o.workload))}}
	if o.workload == workloadSummaryBlocking {
		settings.Compaction.BackgroundTokens = new(0)
	}
	if o.partialMs > 0 {
		settings.Progress = &harness.ProgressPolicyPatch{PartialIntervalMs: &o.partialMs}
	}
	var rec *counts.Recorder
	if o.record != "" {
		if rec, err = counts.NewRecorder(o.record); err != nil {
			return err
		}
	}
	// total_changes() is the rows the connection has changed: the recording keeps what each commit changed, which the replay
	// cannot see because it applies no statement.
	var changes int64
	rowsChanged := func() {
		rows, _, err := db.All(ctx, "SELECT total_changes()", nil)
		if err != nil || len(rows) != 1 {
			fmt.Fprintln(os.Stderr, "bench: total_changes:", err)
			os.Exit(1)
		}
		now := rows[0][0].Int
		rec.RowsChanged(uint64(now - changes))
		changes = now
	}
	settle, onCommitted := synctest.Wait, func(c abi.Commit, sqlText []string) {
		if rec != nil {
			rowsChanged()
		}
		stmts := make([]contracttest.Stmt, len(c.Stmts))
		for i, s := range c.Stmts {
			stmts[i] = contracttest.Stmt{SQL: sqlText[s.SQL], Params: params(s.Params)}
		}
		if err := w.Commit("s0", c.Seq, stmts); err != nil {
			fmt.Fprintln(os.Stderr, "bench: trace:", err)
			os.Exit(1)
		}
	}
	if timed {
		settle, onCommitted = nil, nil
	}
	ids := &uuidv7Gen{rng: rng, nowMillis: func() int64 { return int64(math.Floor(virtualNow())) }}
	uuid := ids.next
	if rec != nil {
		uuid = rec.UUID(uuid)
	}
	var stepper sqlhost.Stepper = core.NewSession(uuid)
	if rec != nil {
		stepper = rec.Stepper(stepper.(*core.Session))
	}
	if o.observe != nil {
		stepper = observing{stepper, o.observe}
	}
	session, err := driver.Open(ctx, driver.SessionConfig{
		Core: stepper, SQL: core.SQLTable(), DB: db,
		Harness: harness.HarnessOptions{
			Models: models, Registry: registry,
			Now:      func() float64 { return math.Floor(virtualNow()) },
			Settings: func() *harness.HarnessSettings { return &settings },
			OnReport: func(err error, _ durable.ReportInfo) { fmt.Fprintln(os.Stderr, "report:", err) },
		},
		Monotonic:   virtualNow,
		TimerLead:   time.Nanosecond,
		Settle:      settle,
		OnCommitted: onCommitted,
	})
	if err != nil {
		return err
	}
	// Every path closes the session, so its host goroutine ends with the run. An error return (a refused fixture, a failed turn)
	// used to leave it running, reading the clock the next run resets. Close is idempotent, so the paths that close it first are unaffected.
	defer func() { err = errors.Join(err, session.Close(context.WithoutCancel(ctx))) }()
	model := provider.GetModel()
	root, err := session.Root(ctx, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: model.Provider.ID(), ModelId: model.ID})}})
	if err != nil {
		return err
	}
	open, openCPU := time.Since(opened), processCPU()-openedCPU
	runTurn := func(t turnSpec) error {
		sub, err := root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(t.text), RequestId: &t.id})
		if err != nil {
			return err
		}
		settled, err := sub.Wait(ctx)
		if err != nil {
			return err
		}
		if settled.Status != durable.SubmissionDone {
			return fmt.Errorf("submission %s settled as %s", t.id, settled.Status)
		}
		if err := root.WaitForIdle(ctx); err != nil {
			return err
		}
		return sc.recordErr
	}
	if o.recoverRun {
		if err := root.WaitForIdle(ctx); err != nil {
			return err
		}
	}
	for _, t := range historyTurns(o.seedFrom, o.seed) {
		if err := runTurn(t); err != nil {
			return err
		}
	}
	mem, err := contracttest.OpenMemoryLog(os.Getenv("CONTRACT_MEM"))
	if err != nil {
		return err
	}
	defer mem.Close()
	sc.markCall = sc.calls + 1
	turns, turnsCPU := make([]float64, 0, o.turns), make([]float64, 0, o.turns)
	for i := range o.turns {
		suffix := ""
		if o.stream > 0 {
			suffix += fmt.Sprintf(" stream=%d", o.stream)
		}
		if o.parallel {
			suffix += " parallel=1"
		}
		if o.subagent {
			suffix += " subagent=1"
		}
		id := fmt.Sprintf("%s%d", o.label, i)
		if rec != nil {
			rec.Mark(fmt.Sprintf("turn %d", i))
		}
		started, startedCPU := time.Now(), processCPU()
		if err := runTurn(turnSpec{id, fmt.Sprintf("turn %s tools=%d%s", id, o.tools, suffix)}); err != nil {
			return err
		}
		turns, turnsCPU = append(turns, ms(time.Since(started))), append(turnsCPU, processCPU()-startedCPU)
		mem.Sample(i)
	}
	if o.finish != nil {
		return o.finish(ctx, session, sc, measured{open: ms(open), openCPU: openCPU, turns: turns, turnsCPU: turnsCPU})
	}
	if timed {
		return finishTiming(ctx, o, session, sc, open, turns)
	}
	if rec != nil {
		rec.Mark("end")
	}
	if err := session.Close(ctx); err != nil {
		return err
	}
	if rec != nil {
		if err := rec.Close(); err != nil {
			return err
		}
	}
	//portlint:allow all the bench's result line has no Pi counterpart; the runner parses it
	out, _ := json.Marshal(map[string]any{"seeded": o.seed, "turns": o.turns, "calls": sc.calls, "bench": sc.benchFingerprint()})
	if err := w.Out(string(out)); err != nil {
		return err
	}
	return w.Close(0)
}
