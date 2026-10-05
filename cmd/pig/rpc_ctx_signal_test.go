package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Pi gives an extension `ctx.signal` as runner.getSignalFn() (runner.ts:917-920), bound to `() => this.agent.signal` (agent-session.ts:3368): the signal of the run in progress (agent.ts:336-338), undefined while no run is active.
// Every handler of a run, a command issued during it and a tool's context see that one object, and aborting the run aborts it while a handler that holds it is still in flight. The run's signal is cleared before agent_settled.
// testdata/ctx-signal.mjs records ctx.signal at each entry point; a signal is named by the order it first appears in.
type ctxSignalRow struct {
	Where   string `json:"where"`
	Signal  string `json:"signal"`
	Aborted *bool  `json:"aborted,omitempty"`
	// Outcome and ParamAborted are set by the rows a handler writes after it waited on the signal.
	Outcome      string `json:"outcome,omitempty"`
	ParamAborted *bool  `json:"paramAborted,omitempty"`
	// SameAsCtx is whether the tool's signal parameter is ctx.signal itself.
	SameAsCtx *bool `json:"sameAsCtx,omitempty"`
}

// ctxSignalPollReport is where testdata/ctx-signal-poll.mjs, a second extension that reads ctx.signal from a timer, records the changes it sees.
func ctxSignalPollReport(report string) string { return report + ".poll" }

func startCtxSignalPig(t *testing.T, extraEnv ...string) (*rpcProcess, string) {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "ctx-signal.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	pollFixture, err := filepath.Abs(filepath.Join("testdata", "ctx-signal-poll.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.jsonl")
	env := append([]string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "CTX_SIGNAL_REPORT=" + report, "CTX_SIGNAL_POLL_REPORT=" + ctxSignalPollReport(report)}, extraEnv...)
	return startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1", "-e", fixture, "-e", pollFixture), report
}

// startCtxSignalPi starts the pinned Pi in RPC mode with the same fixture and Pi's test-faux provider.
func startCtxSignalPi(t *testing.T, extraEnv ...string) (*rpcProcess, string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != "1.0.3" {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	home := t.TempDir()
	report := filepath.Join(t.TempDir(), "report.jsonl")
	env := append([]string{"HOME=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "CTX_SIGNAL_REPORT=" + report, "CTX_SIGNAL_POLL_REPORT=" + ctxSignalPollReport(report)}, extraEnv...)
	args := []string{filepath.Join(piRoot, "dist", "cli.js"), "--mode", "rpc", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1",
		"-e", filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts"), "-e", filepath.Join(root, "cmd", "pig", "testdata", "ctx-signal.mjs"), "-e", filepath.Join(root, "cmd", "pig", "testdata", "ctx-signal-poll.mjs")}
	return startJSONLProcessAt(t, t.TempDir(), env, "node", args...), report
}

func readCtxSignalReport(t *testing.T, report string) []ctxSignalRow {
	t.Helper()
	data, err := os.ReadFile(report)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var rows []ctxSignalRow
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row ctxSignalRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

// awaitCtxSignalRow polls the report until a row for where exists.
func awaitCtxSignalRow(t *testing.T, p *rpcProcess, report, where string) {
	t.Helper()
	deadline := time.Now().Add(p.budget)
	for time.Now().Before(deadline) {
		for _, row := range readCtxSignalReport(t, report) {
			if row.Where == where {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s row in the report:%s\n%s", where, formatCtxSignalRows(readCtxSignalReport(t, report)), p.stderr.String())
}

var ctxSignalNone = ctxSignalRow{Signal: "undefined"}

// awaitCtxSignalPollRow waits until the timer extension has recorded the given state.
func awaitCtxSignalPollRow(t *testing.T, p *rpcProcess, report, signal string) {
	t.Helper()
	deadline := time.Now().Add(p.budget)
	for time.Now().Before(deadline) {
		for _, row := range readCtxSignalReport(t, ctxSignalPollReport(report)) {
			if row.Signal == signal {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the timer extension never recorded ctx.signal %s\n%s", signal, p.stderr.String())
}

func ctxSignalPollWant(signal string) ctxSignalRow {
	return ctxSignalRow{Where: "poll", Signal: signal}
}

// awaitCtxSignalPoll waits until the timer extension has recorded every row of want, then requires it recorded nothing else: its states follow the run with no request in between.
func awaitCtxSignalPoll(t *testing.T, p *rpcProcess, report string, want []ctxSignalRow) {
	t.Helper()
	deadline := time.Now().Add(p.budget)
	for len(readCtxSignalReport(t, ctxSignalPollReport(report))) < len(want) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// A late extra transition would show here.
	time.Sleep(100 * time.Millisecond)
	if got := readCtxSignalReport(t, ctxSignalPollReport(report)); !reflect.DeepEqual(got, want) {
		t.Fatalf("ctx.signal read by a timer:\n got %s\nwant %s\n%s", formatCtxSignalRows(got), formatCtxSignalRows(want), p.stderr.String())
	}
}

func ctxSignalWant(where string, rest ctxSignalRow) ctxSignalRow {
	rest.Where = where
	return rest
}

func TestExtensionContextSignalComparedWithPi(t *testing.T) {
	live := ctxSignalRow{Signal: "s1", Aborted: new(false)}
	aborted := ctxSignalRow{Signal: "s1", Aborted: new(true)}
	scenarios := []struct {
		name string
		env  []string
		// drive runs the scenario after the extension loaded.
		drive func(t *testing.T, p *rpcProcess, report string)
		want  []ctxSignalRow
		// poll is what the timer of ctx-signal-poll.mjs saw: the run's signal appears when a run begins and is gone when it ends, though that extension handles no event after session_start. A scenario without a run sees none throughout.
		poll []ctxSignalRow
	}{
		{
			name: "idle",
			drive: func(t *testing.T, p *rpcProcess, report string) {
				p.sendJSON(map[string]any{"id": "idle", "type": "prompt", "message": "/probe"})
				awaitCtxSignalRow(t, p, report, "command")
			},
			want: []ctxSignalRow{ctxSignalWant("session_start", ctxSignalNone), ctxSignalWant("command", ctxSignalNone)},
			poll: []ctxSignalRow{ctxSignalPollWant("undefined")},
		},
		{
			name: "abort a running tool",
			drive: func(t *testing.T, p *rpcProcess, report string) {
				p.sendJSON(map[string]any{"id": "idle", "type": "prompt", "message": "/probe"})
				awaitCtxSignalRow(t, p, report, "command")
				p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "Run: extension echo hello"})
				p.await("tool update", func(r rpcRecord) bool { return r["type"] == "tool_execution_update" })
				// A command issued during the run gets the run's signal.
				p.sendJSON(map[string]any{"id": "during", "type": "prompt", "message": "/probe"})
				p.await("command during the run", func(r rpcRecord) bool { return r["id"] == "during" && r["type"] == "response" })
				p.sendJSON(map[string]any{"id": "abort", "type": "abort"})
				awaitCtxSignalRow(t, p, report, "agent_settled")
			},
			want: []ctxSignalRow{
				ctxSignalWant("session_start", ctxSignalNone),
				ctxSignalWant("command", ctxSignalNone),
				ctxSignalWant("agent_start", live),
				ctxSignalWant("tool_execution_start", live),
				ctxSignalWant("tool_call", live),
				ctxSignalWant("tool_execute", ctxSignalRow{Signal: "s1", Aborted: new(false), ParamAborted: new(false), SameAsCtx: new(true)}),
				ctxSignalWant("command", live),
				ctxSignalWant("tool_execute.held", ctxSignalRow{Signal: "s1", Aborted: new(true), Outcome: "aborted", ParamAborted: new(true)}),
				ctxSignalWant("agent_end", aborted),
				ctxSignalWant("agent_settled", ctxSignalNone),
			},
			poll: []ctxSignalRow{ctxSignalPollWant("undefined"), ctxSignalPollWant("live"), ctxSignalPollWant("undefined")},
		},
		{
			name: "nested tool calls",
			env:  []string{"CTX_SIGNAL_NESTED=1"},
			drive: func(t *testing.T, p *rpcProcess, report string) {
				p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "Run: extension echo hello"})
				awaitCtxSignalRow(t, p, report, "inner.explicit.held")
				p.sendJSON(map[string]any{"id": "abort", "type": "abort"})
				awaitCtxSignalRow(t, p, report, "agent_settled")
			},
			want: []ctxSignalRow{
				ctxSignalWant("session_start", ctxSignalNone),
				ctxSignalWant("agent_start", live),
				ctxSignalWant("tool_execution_start", live),
				ctxSignalWant("tool_call", live),
				ctxSignalWant("tool_execute", ctxSignalRow{Signal: "s1", Aborted: new(false), ParamAborted: new(false), SameAsCtx: new(true)}),
				// A nested call without a signal option runs with the calling tool's signal: the run's.
				ctxSignalWant("tool_execution_start", live),
				ctxSignalWant("tool_call", live),
				ctxSignalWant("inner.default", ctxSignalRow{Signal: "s1", Aborted: new(false), ParamAborted: new(false), SameAsCtx: new(true)}),
				// A nested call with its own signal runs with that signal, and aborting it leaves the run's signal alone.
				ctxSignalWant("tool_execution_start", live),
				ctxSignalWant("tool_call", live),
				ctxSignalWant("inner.explicit", ctxSignalRow{Signal: "s1", Aborted: new(false), ParamAborted: new(false), SameAsCtx: new(false)}),
				ctxSignalWant("inner.explicit.held", ctxSignalRow{Signal: "s1", Aborted: new(false), Outcome: "aborted", ParamAborted: new(true)}),
				ctxSignalWant("tool_execute.held", ctxSignalRow{Signal: "s1", Aborted: new(true), Outcome: "aborted", ParamAborted: new(true)}),
				ctxSignalWant("agent_end", aborted),
				ctxSignalWant("agent_settled", ctxSignalNone),
			},
			poll: []ctxSignalRow{ctxSignalPollWant("undefined"), ctxSignalPollWant("live"), ctxSignalPollWant("undefined")},
		},
		{
			name: "abort while a turn_start handler is in flight",
			env:  []string{"CTX_SIGNAL_HOLD_TURN_START=1"},
			drive: func(t *testing.T, p *rpcProcess, report string) {
				p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "Run: extension echo hello"})
				awaitCtxSignalRow(t, p, report, "turn_start")
				// The run is short; abort only once the timer extension has seen it, since a Windows timer ticks about every
				// 15.6 ms and could otherwise miss the whole run.
				awaitCtxSignalPollRow(t, p, report, "live")
				p.sendJSON(map[string]any{"id": "abort", "type": "abort"})
				awaitCtxSignalRow(t, p, report, "agent_settled")
			},
			want: []ctxSignalRow{
				ctxSignalWant("session_start", ctxSignalNone),
				ctxSignalWant("agent_start", live),
				ctxSignalWant("turn_start", live),
				ctxSignalWant("turn_start.held", ctxSignalRow{Signal: "s1", Aborted: new(true), Outcome: "aborted"}),
				ctxSignalWant("agent_end", aborted),
				ctxSignalWant("agent_settled", ctxSignalNone),
			},
			poll: []ctxSignalRow{ctxSignalPollWant("undefined"), ctxSignalPollWant("live"), ctxSignalPollWant("undefined")},
		},
	}
	impls := []struct {
		name  string
		start func(t *testing.T, extraEnv ...string) (*rpcProcess, string)
	}{{"pig", startCtxSignalPig}, {"pi", startCtxSignalPi}}
	for _, impl := range impls {
		for _, scenario := range scenarios {
			t.Run(impl.name+"/"+scenario.name, func(t *testing.T) {
				p, report := impl.start(t, scenario.env...)
				awaitReady(p)
				scenario.drive(t, p, report)
				awaitCtxSignalPoll(t, p, report, scenario.poll)
				if got := readCtxSignalReport(t, report); !reflect.DeepEqual(got, scenario.want) {
					t.Fatalf("ctx.signal rows:\n got %s\nwant %s", formatCtxSignalRows(got), formatCtxSignalRows(scenario.want))
				}
			})
		}
	}
}

func formatCtxSignalRows(rows []ctxSignalRow) string {
	var b strings.Builder
	for _, row := range rows {
		encoded, _ := json.Marshal(row)
		b.WriteString("\n  " + string(encoded))
	}
	return b.String()
}
