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

// What two runs of one mode never share: Session ids and times.
var framePacingModesVolatile = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z|"timestamp":[0-9]+`)

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
