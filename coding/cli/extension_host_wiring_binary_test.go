package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runHostWiringMode runs the real binary in mode with the given extensions and no prompt, and returns the lines the extensions wrote to their report. Print and JSON mode read an empty stdin; RPC mode reads a closed one, which disposes the runtime as rpc-mode.ts does at input end.
func runHostWiringMode(t *testing.T, mode string, extraArgs []string, extensions ...string) []string {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	home, cwd := t.TempDir(), t.TempDir()
	report := filepath.Join(t.TempDir(), "report.jsonl")
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--session-dir", t.TempDir()}
	for _, path := range extensions {
		fixture, err := filepath.Abs(filepath.Join("testdata", path))
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", fixture)
	}
	args = append(args, extraArgs...)
	switch mode {
	case "print":
		args = append(args, "--print")
	case "json", "rpc":
		args = append(args, "--mode", mode)
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig"), "WIRING_REPORT="+report)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s mode: %v\n%s", mode, err, out)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("%s mode wrote no report: %v\n%s", mode, err, out)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// Pi binds setModel, setThinkingLevel, isProjectTrusted and compact to the Session in every mode (agent-session.ts:3343-3349, 3355, 3369-3379 _bindExtensionCore), and print and RPC mode only add their command-context actions (print-mode.ts:76-104, rpc-mode.ts:319-351). The expected records are Pi 0.99.1's, probed with this fixture in -p, --mode json and --mode rpc: --no-approve leaves the project untrusted, setModel to a model whose provider has no credentials resolves false without a model_select or a model_change (agent-session.ts:3343-3344), setModel to the provider's second model resolves true after one model_select (agent-session.ts:2372-2384, 2411), an empty Session's compaction reports compact()'s own error (agent-session.ts:2700-2702) through onError, and the changed thinking level emits thinking_level_select and appends a thinking_level_change. Pig's print, JSON and RPC modes left all four unbound, so the extension saw a trusted project, a "not ready" rejection, no level change and "compaction is not available".
func TestHeadlessExtensionSessionActionsMatchPi(t *testing.T) {
	t.Parallel()
	want := []string{
		`{"event":"trusted","value":false}`,
		`{"event":"setModelNoAuth","value":false}`,
		`{"event":"model_select","model":"probe/probe-model-2"}`,
		`{"event":"setModel","value":true}`,
		`{"event":"compact","error":"Nothing to compact (session too small)"}`,
		`{"event":"thinking_level_select","level":"high"}`,
		`{"event":"entries","value":["model_change:probe/probe-model","thinking_level_change:medium","model_change:probe/probe-model-2","thinking_level_change:high"]}`,
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			got := runHostWiringMode(t, mode, []string{"--no-approve"}, "host-wiring-session-actions.mjs")
			if !slices.Equal(got, want) {
				t.Fatalf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// A virtual model an SDK extension registers while its factory runs is queued in the extension runtime (loader.ts:480-490) and flushed into the model runtime when the Session binds its runner (runner.ts:497-513, agent-session.ts:3562), before session_start (agent-session.ts:3194). Pig's extension host queued it in its own runtime, which no production path bound, so ctx.modelRegistry.find never saw it in any mode.
func TestSubprocessVirtualModelReachesTheModelRuntime(t *testing.T) {
	t.Parallel()
	want := []string{`{"event":"virtual_model","value":"auto"}`}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			got := runHostWiringMode(t, mode, nil, "host-wiring-session-actions.mjs", "host-wiring-virtual-model.py")
			var records []string
			for _, line := range got {
				if strings.Contains(line, `"virtual_model"`) {
					records = append(records, line)
				}
			}
			if !slices.Equal(records, want) {
				t.Fatalf("virtual model records %v, want %v (all records:\n%s)", records, want, strings.Join(got, "\n"))
			}
		})
	}
}

// Pi flushes the virtual models extensions queued while they loaded into the model runtime after their provider registrations and before the Session exists (agent-session-services.ts:158-193). A registration that fails there is a startup error diagnostic, so Pi reports it and exits 1, with no -ne hint because no extension failed to load (main.ts:907-916). Probed with Pi 0.99.1 in -p, --mode json and --mode rpc on an equivalent JavaScript extension: `Error: Extension "<path>" error: Virtual model probe/probe-model conflicts with a physical model.` and exit status 1. Pig bound the host's virtual models only after the Session existed, so the failure reached no listener and startup went on.
func TestExtensionVirtualModelFlushFailureStopsStartup(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	conflict, err := filepath.Abs(filepath.Join("testdata", "host-wiring-virtual-model-conflict.py"))
	if err != nil {
		t.Fatal(err)
	}
	actions, err := filepath.Abs(filepath.Join("testdata", "host-wiring-session-actions.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	want := `Error: Extension "` + conflict + `" error: Virtual model probe/probe-model conflicts with a physical model.` + "\n"
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--session-dir", t.TempDir(), "-e", actions, "-e", conflict}
			if mode == "print" {
				args = append(args, "--print")
			} else {
				args = append(args, "--mode", mode)
			}
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig"), "WIRING_REPORT="+filepath.Join(t.TempDir(), "report.jsonl"))
			cmd.Stdin = strings.NewReader("")
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("%s mode: %v, want exit status 1\nstdout:\n%s\nstderr:\n%s", mode, err, stdout.String(), stderr.String())
			}
			if stderr.String() != want || stdout.String() != "" {
				t.Fatalf("%s mode stderr %q, stdout %q; want stderr %q and no stdout", mode, stderr.String(), stdout.String(), want)
			}
		})
	}
}
