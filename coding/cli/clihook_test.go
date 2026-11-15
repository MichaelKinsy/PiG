package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/clihook"
)

// clihookChildEnv makes the test binary a program that links a fused member's CLI hook and runs the command line through Main: the value names the hook.
const clihookChildEnv = "PIG_TEST_CLIHOOK_CHILD"

// clihookExitEnv is the status the child's hook returns when it serves the invocation, and the one its extension records after a print run.
const clihookExitEnv = "PIG_TEST_CLIHOOK_EXIT"

// clihookRetryEnv is "max,base,cap" for the retry policy the child's hook asks for.
const clihookRetryEnv = "PIG_TEST_CLIHOOK_RETRY"

// clihookRewritten is the command line the "rewrite" hook hands PiG whatever it was given.
var clihookRewritten = []string{"--offline", "--no-session", "--model", "test-faux/faux-1", "-p", "reply with exactly: rewritten"}

// clihookMember is the extension of a fused member that owns a print run's output: it prints the answer on clihook.Stdout, or the words of the failure on stderr, and records the status.
func clihookMember(api extension.API) error {
	api.OnAgentEnd(func(_ context.Context, evt extension.AgentEndEvent) error {
		var last *agent.AssistantMessage
		for _, message := range evt.Messages {
			if message.Assistant != nil {
				last = message.Assistant
			}
		}
		if last == nil {
			return nil
		}
		if last.StopReason == ai.StopReasonError {
			_, _ = fmt.Fprintln(os.Stderr, "member says: the run failed")
		} else if out := clihook.Stdout(); out != nil {
			for _, block := range last.Content {
				if text, ok := block.(ai.TextContent); ok {
					_, _ = fmt.Fprintf(out, "member: %s\n", strings.TrimSpace(text.Text))
				}
			}
		}
		if code, err := strconv.Atoi(os.Getenv(clihookExitEnv)); err == nil {
			clihook.SetExitCode(code)
		}
		return nil
	})
	return nil
}

// TestClihookChildProgram is the child of TestClihookOnTheProductionPath: it does nothing unless the parent names a hook.
func TestClihookChildProgram(t *testing.T) {
	name := os.Getenv(clihookChildEnv)
	if name == "" {
		t.Skip("the child program of the clihook tests")
	}
	options := &MainOptions{}
	exit, _ := strconv.Atoi(os.Getenv(clihookExitEnv))
	switch name {
	case "none":
	case "pass":
		clihook.Register(func(env clihook.Env) clihook.Result { return clihook.Result{Args: env.Args} })
	case "serve":
		clihook.Register(func(env clihook.Env) clihook.Result {
			_, _ = fmt.Fprintf(env.Stdout, "served %s\n", strings.Join(env.Args, " "))
			return clihook.Result{Handled: true, Exit: exit, Args: env.Args}
		})
	case "rewrite":
		clihook.Register(func(clihook.Env) clihook.Result { return clihook.Result{Args: clihookRewritten} })
	case "own":
		clihook.Register(func(env clihook.Env) clihook.Result { return clihook.Result{Args: env.Args, OwnPrintOutput: true} })
		options.ExtensionFactories = []extension.InlineExtension{extension.ExtensionFactory(clihookMember)}
	case "retry":
		clihook.Register(func(env clihook.Env) clihook.Result {
			var policy clihook.RetryPolicy
			if _, err := fmt.Sscanf(os.Getenv(clihookRetryEnv), "%d,%d,%d", &policy.MaxRetries, &policy.BaseDelayMs, &policy.MaxAgentDelayMs); err != nil {
				t.Errorf("%s = %q: %v", clihookRetryEnv, os.Getenv(clihookRetryEnv), err)
			}
			return clihook.Result{Args: env.Args, Retry: &policy}
		})
	default:
		t.Fatalf("unknown hook %q", name)
	}
	Main(flag.Args(), options)
	// Main returned: the run ended without exitProcess. The test binary would add its own "PASS" to stdout.
	os.Exit(0)
}

type clihookRun struct {
	stdout, stderr string
	exit           int
}

// runClihookChild runs the command line as a program whose fused member registered the named hook.
func runClihookChild(t *testing.T, hook string, env []string, args ...string) clihookRun {
	t.Helper()
	home := t.TempDir()
	command := exec.CommandContext(t.Context(), os.Args[0], append([]string{"-test.run=^TestClihookChildProgram$", "--"}, args...)...)
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", clihookChildEnv+"="+hook)
	command.Env = append(command.Env, env...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	run := clihookRun{}
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("%s %v: %v", hook, args, err)
		}
		run.exit = exit.ExitCode()
	}
	run.stdout, run.stderr = stdout.String(), stderr.String()
	return run
}

// printArgs is a print run of the test provider that answers prompt (reply with exactly: ok) or, for any other prompt, fails with `test-faux: unhandled request`.
func printArgs(mode, prompt string) []string {
	return []string{"--offline", "--no-session", "--model", "test-faux/faux-1", "--mode", mode, "-p", prompt}
}

const clihookAnswer = "reply with exactly: ok"

// A fused member's CLI hook is called by Main on the production path, before PiG parses a word of the command line (record D653). Every row runs the real command line in a child process that registered the hook, and compares it with the same command line without a hook.
// mutation-checked, each red in the row named: Main falling through after a Handled result (the exitProcess call replaced by `_ = res.Exit`) fails "serves the invocation" (stdout carries PiG's help after the hook's line); `args = res.Args` dropped fails "rewrites the command line" (`Error: Unknown option: --no-such-flag`); PiG's stdout not discarded under ownOutput fails "owns the print output" (stdout `member: ok\nok\n`); the result line written to opts.stderr under ownOutput fails "owns the failed run's stderr line" (stderr carries PiG's `test-faux: unhandled request "boom"` after the member's); `ownPrintOutput` never set fails the same three rows; the clihook.ExitCode block dropped fails "owns the exit status" (exit 0, want 3); Result.Retry ignored fails "retry policy replaces the built-in" (0 retries ends with exit 0 and retry-ok); the result line's stderr always discarded fails "no hook is the baseline" (stderr empty, want PiG's line).
func TestClihookOnTheProductionPath(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the command line in child processes")
	}
	const unhandled = `test-faux: unhandled request "boom"`
	t.Run("no hook is the baseline", func(t *testing.T) {
		t.Parallel()
		none := runClihookChild(t, "none", nil, printArgs("text", clihookAnswer)...)
		if none.exit != 0 || none.stdout != "ok\n" {
			t.Fatalf("no hook: exit %d, stdout %q, stderr %q, want the answer and exit 0", none.exit, none.stdout, none.stderr)
		}
		failed := runClihookChild(t, "none", nil, printArgs("text", "boom")...)
		if failed.exit != 1 || failed.stdout != "" || !strings.Contains(failed.stderr, unhandled+"\n") {
			t.Fatalf("no hook, failing run: exit %d, stdout %q, stderr %q, want exit 1 and PiG's stderr line %q", failed.exit, failed.stdout, failed.stderr, unhandled)
		}
	})
	t.Run("a hook that changes nothing is a run without a hook", func(t *testing.T) {
		t.Parallel()
		for _, prompt := range []string{clihookAnswer, "boom"} {
			for _, mode := range []string{"text", "json"} {
				none := runClihookChild(t, "none", nil, printArgs(mode, prompt)...)
				pass := runClihookChild(t, "pass", nil, printArgs(mode, prompt)...)
				if mode == "json" {
					// The JSON events carry ids and timestamps; the rows below compare their shape.
					if pass.exit != none.exit || strings.Count(pass.stdout, "\n") != strings.Count(none.stdout, "\n") || pass.stderr != none.stderr {
						t.Fatalf("%s %q: with a pass-through hook %+v, without %+v", mode, prompt, pass, none)
					}
					continue
				}
				if pass != none {
					t.Fatalf("%s %q: with a pass-through hook %+v, without %+v", mode, prompt, pass, none)
				}
			}
		}
	})
	t.Run("serves the invocation", func(t *testing.T) {
		t.Parallel()
		for _, exit := range []string{"7", "0"} {
			// --help is what PiG would print; a hook that served the invocation leaves PiG with nothing to parse.
			run := runClihookChild(t, "serve", []string{clihookExitEnv + "=" + exit}, "--help", "--no-such-flag")
			want, _ := strconv.Atoi(exit)
			if run.exit != want || run.stdout != "served --help --no-such-flag\n" || run.stderr != "" {
				t.Fatalf("exit %s: got exit %d, stdout %q, stderr %q, want the hook's line only", exit, run.exit, run.stdout, run.stderr)
			}
		}
	})
	t.Run("rewrites the command line", func(t *testing.T) {
		t.Parallel()
		// The command line as given would fail in PiG; what PiG parses is the hook's Args.
		run := runClihookChild(t, "rewrite", nil, "--no-such-flag", "ignored")
		if run.exit != 0 || run.stdout != "rewritten\n" {
			t.Fatalf("exit %d, stdout %q, stderr %q, want the rewritten run's answer", run.exit, run.stdout, run.stderr)
		}
	})
	t.Run("owns the print output", func(t *testing.T) {
		t.Parallel()
		for _, mode := range []string{"text", "json"} {
			run := runClihookChild(t, "own", nil, printArgs(mode, clihookAnswer)...)
			if run.exit != 0 || run.stdout != "member: ok\n" {
				t.Fatalf("%s: exit %d, stdout %q, stderr %q, want the member's line only (PiG writes nothing to stdout)", mode, run.exit, run.stdout, run.stderr)
			}
		}
	})
	t.Run("owns the exit status", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			record string
			want   int
		}{{"3", 3}, {"0", 0}, {"", 0}} {
			env := []string{}
			if tc.record != "" {
				env = append(env, clihookExitEnv+"="+tc.record)
			}
			run := runClihookChild(t, "own", env, printArgs("text", clihookAnswer)...)
			if run.exit != tc.want || run.stdout != "member: ok\n" {
				t.Fatalf("recorded %q: exit %d, stdout %q, stderr %q, want exit %d", tc.record, run.exit, run.stdout, run.stderr, tc.want)
			}
		}
	})
	t.Run("owns the failed run's stderr line", func(t *testing.T) {
		t.Parallel()
		// A failed run keeps PiG's status 1 whatever the member recorded, and the stderr line is the member's alone.
		for _, record := range []string{"", "3"} {
			env := []string{}
			if record != "" {
				env = append(env, clihookExitEnv+"="+record)
			}
			run := runClihookChild(t, "own", env, printArgs("text", "boom")...)
			if run.exit != 1 || run.stdout != "" || run.stderr != "member says: the run failed\n" {
				t.Fatalf("recorded %q: exit %d, stdout %q, stderr %q, want exit 1, no stdout and only the member's line", record, run.exit, run.stdout, run.stderr)
			}
		}
	})
	t.Run("retry policy replaces the built-in", func(t *testing.T) {
		t.Parallel()
		const retryable = "Trigger: retryable provider error"
		// Built-in: three retries after 2000 ms, so the second request answers.
		none := runClihookChild(t, "none", nil, printArgs("text", retryable)...)
		if none.exit != 0 || none.stdout != "retry-ok\n" {
			t.Fatalf("no hook: exit %d, stdout %q, stderr %q, want the retry's answer", none.exit, none.stdout, none.stderr)
		}
		// No retries: the first failure ends the run.
		off := runClihookChild(t, "retry", []string{clihookRetryEnv + "=0,1,1"}, printArgs("text", retryable)...)
		if off.exit != 1 || off.stdout != "" || !strings.Contains(off.stderr, "please retry your request") {
			t.Fatalf("0 retries: exit %d, stdout %q, stderr %q, want exit 1 and the provider's error", off.exit, off.stdout, off.stderr)
		}
		// One retry after 1 ms.
		one := runClihookChild(t, "retry", []string{clihookRetryEnv + "=1,1,1"}, printArgs("text", retryable)...)
		if one.exit != 0 || one.stdout != "retry-ok\n" {
			t.Fatalf("1 retry: exit %d, stdout %q, stderr %q, want the retry's answer", one.exit, one.stdout, one.stderr)
		}
	})
}
