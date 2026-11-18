package packagemanager

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// #11264 (Pi b513b64d8ac8): interactive mode runs installs while the TUI draws on the terminal, so their output is captured.
// The tests below are Pi's "captured command output" in test/package-manager.test.ts, each with Pi's input and expectation.

// capturedOutputChild is the child process of the tests below: the test binary itself, in the mode its environment names.
func TestCapturedOutputChild(t *testing.T) {
	switch os.Getenv("PIG_CAPTURED_OUTPUT_CHILD") {
	case "fail":
		// process.stdout.write('npm out\n'); process.stderr.write('npm ERR! broken\n'); process.exit(3)
		_, _ = os.Stdout.WriteString("npm out\n")
		_, _ = os.Stderr.WriteString("npm ERR! broken\n")
		os.Exit(3)
	case "background":
		// spawn(process.execPath, ['-e', 'setTimeout(() => {}, 5000)'], { stdio: 'inherit', detached: true }).unref()
		cmd := exec.Command(os.Args[0], "-test.run=^TestCapturedOutputChild$")
		cmd.Env = append(os.Environ(), "PIG_CAPTURED_OUTPUT_CHILD=sleep")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		detachForTest(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "fail-with":
		// The output is the text in PIG_CAPTURED_OUTPUT_TEXT, repeated PIG_CAPTURED_OUTPUT_COUNT times.
		count := 0
		for _, digit := range os.Getenv("PIG_CAPTURED_OUTPUT_COUNT") {
			count = count*10 + int(digit-'0')
		}
		_, _ = os.Stdout.WriteString(os.Getenv("PIG_CAPTURED_OUTPUT_PREFIX") + strings.Repeat(os.Getenv("PIG_CAPTURED_OUTPUT_TEXT"), count))
		os.Exit(1)
	}
}

func capturedChild(t *testing.T, mode string, env ...string) (output CommandOutput, name string, args []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range append([]string{"PIG_CAPTURED_OUTPUT_CHILD=" + mode}, env...) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	return CommandOutputCapture, executable, []string{"-test.run=^TestCapturedOutputChild$"}
}

func TestCapturedCommandReportsTheEndOfAFailingCommandsOutputInTheError(t *testing.T) {
	output, name, args := capturedChild(t, "fail")
	err := RunPackageProcess(output, "", name, args...)
	want := " failed with code 3:\nnpm out\nnpm ERR! broken"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

func TestCapturedCommandReturnsWhenTheCommandExitsAlthoughABackgroundProcessStillHoldsItsOutput(t *testing.T) {
	output, name, args := capturedChild(t, "background")
	started := time.Now()
	if err := RunPackageProcess(output, "", name, args...); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("the call took %v, want less than 3s", elapsed)
	}
}

func TestCapturedGitCommandStopsGitAndSSHFromPromptingAndNamesTheFix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	writeResolveFile(t, filepath.Join(binDir, "git"), "#!/bin/sh\necho \"prompt=$GIT_TERMINAL_PROMPT ssh=$GIT_SSH_COMMAND\"\necho \"fatal: could not read Username for 'https://example.com': terminal prompts disabled\" >&2\nexit 128\n")
	if err := os.Chmod(filepath.Join(binDir, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_SSH_COMMAND", "")
	if err := os.Unsetenv("GIT_SSH_COMMAND"); err != nil {
		t.Fatal(err)
	}

	err := RunPackageProcess(CommandOutputCapture, "", "git", "clone", "https://example.com/private.git")
	if err == nil {
		t.Fatal("the failing git succeeded")
	}
	for _, want := range []string{"prompt=0 ssh=ssh -o BatchMode=yes", "Set up a git credential helper or an ssh agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestResolveAnnouncesAPackageItInstalls(t *testing.T) {
	_, _, sm, pm := resolveFixtureWithHome(t)
	npm := filepath.Join(t.TempDir(), "npm")
	if err := os.WriteFile(npm, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetNpmCommand([]string{npm}); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:new-package"}}); err != nil {
		t.Fatal(err)
	}
	var events []ProgressEvent
	pm.SetProgressCallback(func(event ProgressEvent) { events = append(events, event) })

	if _, err := pm.Resolve(nil); err != nil {
		t.Fatal(err)
	}

	found := false
	for _, event := range events {
		found = found || event.Type == "start" && event.Action == "install" && event.Source == "npm:new-package" && event.Message != nil && *event.Message == "Installing npm:new-package..."
	}
	if !found {
		t.Fatalf("events = %+v, want a start event of the install of npm:new-package with its message", events)
	}
}

// Pi keeps String.slice(-8000) of the output: the last 8000 UTF-16 code units, so a 4-byte character counts 2 and a cut inside one leaves a lone surrogate, which Node writes as U+FFFD.
// Probed on the oracle (Node): "😀".repeat(5000) keeps 4000 characters; "😀" + "a".repeat(7999) keeps U+FFFD followed by 7999 "a".
func TestCapturedOutputKeepsTheLast8000UTF16CodeUnits(t *testing.T) {
	for name, tc := range map[string]struct{ prefix, text, count, want string }{
		"ascii":              {"", "a", "9000", strings.Repeat("a", 8000)},
		"astral characters":  {"", "\U0001F600", "5000", strings.Repeat("\U0001F600", 4000)},
		"cut in a surrogate": {"\U0001F600", "a", "7999", "\uFFFD" + strings.Repeat("a", 7999)},
		"short output":       {"", "ab", "3", "ababab"},
	} {
		t.Run(name, func(t *testing.T) {
			output, command, args := capturedChild(t, "fail-with",
				"PIG_CAPTURED_OUTPUT_PREFIX="+tc.prefix, "PIG_CAPTURED_OUTPUT_TEXT="+tc.text, "PIG_CAPTURED_OUTPUT_COUNT="+tc.count)
			err := RunPackageProcess(output, "", command, args...)
			if err == nil {
				t.Fatal("the failing command succeeded")
			}
			_, got, found := strings.Cut(err.Error(), " failed with code 1:\n")
			if !found || got != tc.want {
				t.Fatalf("error tail has %d bytes, want the %d bytes of %.20q: error = %.80q", len(got), len(tc.want), tc.want, err)
			}
		})
	}
}

func TestCapturedCommandThatCannotStartReportsTheStartError(t *testing.T) {
	err := RunPackageProcess(CommandOutputCapture, "", filepath.Join(t.TempDir(), "missing-command"))
	if err == nil {
		t.Fatal("a missing command succeeded")
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		t.Fatalf("error = %v, want a start error, not an exit status", err)
	}
}
