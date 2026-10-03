package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// .upstream/v0.87.1/packages/coding-agent/test/args.test.ts:203,208
// main.ts:694-701 validates even an explicitly empty name when creating a Session.
func TestCLIRejectsEmptySessionNameUpstream(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, name := range []string{"", "   "} {
		t.Run("name="+name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), binary, "--offline", "--no-extensions", "--name", name, "--no-session", "-p")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir())
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if err == nil || cmd.ProcessState.ExitCode() != 1 || stdout.String() != "" || stderr.String() != "Error: --name requires a non-empty value\n" {
				t.Fatalf("err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
		})
	}
}

// Ports packages/coding-agent/test/args.test.ts:208 through main.ts's normalization caller.
func TestCLINormalizesSessionNameUpstream(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, "--offline", "--no-extensions", "--name", "  named session  ", "--session-dir", dir, "--model", "test-faux/faux-1", "-p", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("named turn failed: %v: %s", err, output)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %v, error = %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry struct{ Type, Name string }
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Type == "session_info" {
			if entry.Name != "named session" {
				t.Fatalf("session name = %q, want named session", entry.Name)
			}
			return
		}
	}
	t.Fatal("normalized session name was not persisted")
}

func TestCLIVersionPrecedesSessionValidationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/main.ts:619-622 returns version before Session validation.
	binary := buildPigBinaryForSignalTest(t)
	for _, args := range [][]string{{"--name", "", "--version"}, {"--name", "   ", "--version"}, {"--session-id", "invalid/id", "--version"}} {
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+t.TempDir())
		output, err := cmd.CombinedOutput()
		if err != nil || string(output) != cliVersionString()+"\n" {
			t.Fatalf("%q: err=%v output=%q", args, err, output)
		}
	}
}

// Pi main.ts:680-700 selects and checks the Session before validating --name. Fork creation and custom-ID warnings are observable even when the name is invalid.
func TestCLISelectsSessionBeforeNameValidation(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	piRoot, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != "1.0.0" {
		t.Fatalf("pinned Pi version=%q, err=%v", pkg.Version, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	source := filepath.Join(cwd, "source.jsonl")
	startupNameSessionFixture(t, cwd, source)
	sourceBefore, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	missingCWD := filepath.Join(cwd, "missing-cwd")
	missingSource := filepath.Join(cwd, "missing-cwd.jsonl")
	startupNameSessionFixture(t, missingCWD, missingSource)
	// Both CLIs report process.cwd(), which getcwd returns without symlinks (macOS /var is /private/var).
	processCWD := canonicalTestPath(t, cwd)
	// The rows own their homes and session directories and only read source, so they run together; the group ends when every row has, before the source Session is compared.
	t.Run("rows", func(t *testing.T) {
		for _, tc := range []struct {
			name, stderr string
			args         []string
			fork         bool
		}{
			{"missing session", "No session found matching 'nosuch-id'\n", []string{"--session", "nosuch-id"}, false},
			{"missing fork", "No session found matching 'nosuch-id'\n", []string{"--fork", "nosuch-id"}, false},
			{"missing stored cwd", fmt.Sprintf("Stored session working directory does not exist: %s\nSession file: %s\nCurrent working directory: %s\n", missingCWD, missingSource, processCWD), []string{"--session", missingSource}, false},
			{"fork before validation", "Error: --name requires a non-empty value\n", []string{"--fork", source}, true},
			{"new id warning before validation", "Warning: No project session found with id 'new-session-id'; creating a new session with that id.\nError: --name requires a non-empty value\n", []string{"--session-id", "new-session-id"}, false},
			{"help still validates name", "Error: --name requires a non-empty value\n", []string{"--help", "--resume"}, false},
			{"no session ignores resume", "Error: --name requires a non-empty value\n", []string{"--no-session", "--resume"}, false},
			{"model listing ignores resume", "Error: --name requires a non-empty value\n", []string{"--list-models", "--resume"}, false},
		} {
			for _, mode := range []string{"text", "json", "rpc"} {
				for _, name := range []string{"", " \t "} {
					t.Run(tc.name+"/"+mode+"/name="+name, func(t *testing.T) {
						t.Parallel()
						for _, system := range []struct {
							name, executable string
							prefix           []string
						}{{"pi", node, []string{filepath.Join(piRoot, "dist", "cli.js")}}, {"pig", binary, nil}} {
							home, dir := t.TempDir(), t.TempDir()
							args := append([]string{}, system.prefix...)
							args = append(args, "--offline", "--no-extensions", "--mode", mode, "--name", name, "--session-dir", dir)
							if mode != "rpc" {
								args = append(args, "-p")
							}
							args = append(args, tc.args...)
							cmd := exec.CommandContext(t.Context(), system.executable, args...)
							cmd.Dir = cwd
							cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "pig"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "pi"), "FORCE_COLOR=0", "NO_COLOR=1")
							var stdout, stderr bytes.Buffer
							cmd.Stdout, cmd.Stderr = &stdout, &stderr
							err := cmd.Run()
							var exit *exec.ExitError
							want := tc.stderr
							if runtime.GOOS == "windows" && system.name == "pi" {
								// Node's process.cwd() keeps the 8.3 short form (RUNNER~1) the runner's temp path was given in; PiG resolves it to the long name.
								want = strings.ReplaceAll(want, processCWD, cwd)
							}
							if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.String() != "" || stderr.String() != want {
								t.Errorf("%s: err=%v stdout=%q stderr=%q; want exit=1 stdout=empty stderr=%q", system.name, err, stdout.String(), stderr.String(), want)
							}
							if tc.fork {
								files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
								if err != nil || len(files) != 1 {
									t.Errorf("%s: fork selection produced %v, err=%v; want one requested fork before name validation", system.name, files, err)
								} else if names := startupSessionInfoNames(t, files[0]); len(names) != 0 {
									t.Errorf("%s: invalid name persisted: %q", system.name, names)
								}
							}
						}
					})
				}
			}
		}
	})
	if after, err := os.ReadFile(source); err != nil || !bytes.Equal(after, sourceBefore) {
		t.Fatalf("fork selection modified source Session: %v", err)
	}
}

func BenchmarkParseFlagsEndOfOptions(b *testing.B) {
	args := []string{"--provider", "anthropic", "--model", "claude-sonnet", "--print", "--thinking", "high", "--", "@prompt.md", "- Do the task"}
	for b.Loop() {
		_ = parseFlags(args)
	}
}
