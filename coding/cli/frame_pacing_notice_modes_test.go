package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// What two runs of one mode never share: Session ids and times, the turn's measured duration among them (durationMs of the assistant message, whole milliseconds from the start of the request to its final message, as Pi measures it: event-stream.ts:99-129). The faux model's turn takes 0 ms on a quiet host and 1 ms or more when the host is busy.
var framePacingModesVolatile = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z|"timestamp":[0-9]+|"durationMs":[0-9]+`)

// Print, JSON and RPC mode read neither terminal.framePacing nor terminal.screenWriter and draw no frame, so the notice interactive mode shows when the adaptive frame pacing is selected without the screen writer (D625, codingagent showFramePacingNotice) is not theirs: such a run writes the bytes of a run that selects nothing, on stdout and on stderr, whether the settings file or PIG_FRAME_PACING selects the pacing.
func TestFramePacingNoticeStaysOutOfPrintJSONAndRPC(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []struct {
		name, stdin string
		args        []string
	}{
		{"print", "", []string{"-p", "What is 20+22?"}},
		{"json", "", []string{"--mode", "json", "What is 20+22?"}},
		{"rpc", `{"id":"commands","type":"get_commands"}` + "\n", []string{"--mode", "rpc"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			home := t.TempDir()
			agentDir, cwd := filepath.Join(home, "agent"), filepath.Join(home, "cwd")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			run := func(settings string, env ...string) (stdout, stderr string) {
				t.Helper()
				writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), settings)
				cmd := exec.Command(binary, append([]string{"--model", "test-faux/faux-1", "--offline", "--no-session"}, mode.args...)...)
				cmd.Dir = cwd
				cmd.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
					return strings.HasPrefix(entry, "PIG_FRAME_PACING=") || strings.HasPrefix(entry, "PIG_TUI_SCREEN_WRITER=")
				})
				cmd.Env = append(cmd.Env, "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
				cmd.Env = append(cmd.Env, env...)
				cmd.Stdin = strings.NewReader(mode.stdin)
				var out, errOut bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				if err := cmd.Run(); err != nil {
					t.Fatalf("settings %s, environment %v: %v\nstderr:\n%s", settings, env, err, errOut.String())
				}
				return framePacingModesVolatile.ReplaceAllString(out.String(), "-"), framePacingModesVolatile.ReplaceAllString(errOut.String(), "-")
			}
			stdout, stderr := run(`{}`)
			if stdout == "" {
				t.Fatal("the run that selects nothing wrote nothing to stdout: the mode did not run")
			}
			if again, againErr := run(`{}`); again != stdout || againErr != stderr {
				t.Fatalf("two runs that select nothing differ, so nothing can be compared with them:\nstdout %q\nstdout %q\nstderr %q\nstderr %q", stdout, again, stderr, againErr)
			}
			for _, selection := range []struct {
				name, settings string
				env            []string
			}{
				{"the settings file selects adaptive", `{"terminal":{"framePacing":"adaptive"}}`, nil},
				{"the settings file selects adaptive and the writer pi", `{"terminal":{"framePacing":"adaptive","screenWriter":"pi"}}`, nil},
				{"the environment selects adaptive", `{}`, []string{"PIG_FRAME_PACING=adaptive"}},
			} {
				gotOut, gotErr := run(selection.settings, selection.env...)
				if gotOut != stdout {
					t.Errorf("%s: stdout differs from the run that selects nothing:\n got %q\nwant %q", selection.name, gotOut, stdout)
				}
				if gotErr != stderr {
					t.Errorf("%s: stderr differs from the run that selects nothing:\n got %q\nwant %q", selection.name, gotErr, stderr)
				}
			}
		})
	}
}

// The end of the assistant's message in the json subtest's two runs that select nothing, as the red of 2026-11-18 printed them while the host's load was 40 to 95: the first run's turn took 1 ms and the second's 0 ms; a starved run measured 24 ms. The log had replaced each run's "timestamp" with "-", so each run's own stands here. The expression leaves out both times and nothing beside them: the members around them stay, in their places, and so do members that only end in the same letters or hold a value that is not a count of milliseconds.
func TestFramePacingModesVolatileCoversTheTimes(t *testing.T) {
	end := func(timestamp, duration string) string {
		return `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"42"}],"api":"test-faux","provider":"test-faux","model":"faux-1","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":` + timestamp + `,"durationMs":` + duration + `,"thinkingLevel":"off"}}`
	}
	first := framePacingModesVolatile.ReplaceAllString(end("1794513125383", "1"), "-")
	for _, later := range []struct{ timestamp, duration string }{{"1794513129017", "0"}, {"1794513129017", "24"}, {"1794513129017", "1"}} {
		if got := framePacingModesVolatile.ReplaceAllString(end(later.timestamp, later.duration), "-"); got != first {
			t.Errorf("two runs that differ only in their times compare as different:\n%s\n%s", first, got)
		}
	}
	if want := `"stopReason":"stop",-,-,"thinkingLevel":"off"}}`; !strings.HasSuffix(first, want) {
		t.Errorf("more than the times is left out of the comparison, or one of them is gone: %s\nwant it to end with %s", first, want)
	}
	for _, kept := range []string{`"durationMs":"x"`, `"totalDurationMs":5`, `"durationMs":null`} {
		if got := framePacingModesVolatile.ReplaceAllString(kept, "-"); got != kept {
			t.Errorf("%s is not a time and stays in the comparison, but it became %s", kept, got)
		}
	}
}
