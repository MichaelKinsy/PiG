package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// replaced2860Fixtures returns the -e argument of each SDK realization of testdata/session-replaced-2860: the Node module, the Python module, and the Go and Rust executables built from their testdata sources.
func replaced2860Fixtures(t *testing.T) map[string]string {
	t.Helper()
	for _, tool := range []string{"node", "python3", "cargo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for the SDK realizations of #2860: %v", tool, err)
		}
	}
	node, err := filepath.Abs(filepath.Join("testdata", "session-replaced-2860.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	python, err := filepath.Abs(filepath.Join("testdata", "session-replaced-2860.py"))
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cargo := exec.CommandContext(ctx, "cargo", "build", "--release", "--quiet")
	cargo.Dir = filepath.Join("testdata", "session-replaced-2860-rs")
	cargo.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
	if out, err := cargo.CombinedOutput(); err != nil {
		t.Fatalf("build session-replaced-2860-rs: %v\n%s", err, out)
	}
	return map[string]string{
		"node":   node,
		"python": python,
		"go":     buildGoSDKFixture(t, "session-replaced-2860-go"),
		"rust":   filepath.Join(target, "release", testExecutable("session-replaced-2860-rs")),
	}
}

// runReplaced2860 runs pig in print mode with the fixture and messages, and returns the fixture's log with each process id replaced by its first-seen ordinal.
func runReplaced2860(t *testing.T, bin, fixture string, messages ...string) []string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "2860.log")
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--session-dir", filepath.Join(dir, "sessions"), "--model", "test-faux/faux-1", "-e", fixture, "-p"}
	// A blocked extension keeps pig running, so each run has a bound.
	ctx := testbudget.Context(t)
	cmd := exec.CommandContext(ctx, bin, append(args, messages...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+filepath.Join(dir, "home"), "PIG_CODING_AGENT_DIR="+filepath.Join(dir, "agent"), "PIG_TEST_FAUX=1",
		"PIG_TEST_2860_LOG="+log, "PIG_TEST_2860_MARKS="+filepath.Join(dir, "marks.json"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig -p %q: %v\n%s", messages, err, out)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	ordinals := map[string]int{}
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		// Python's text-mode files end lines with CRLF on Windows; TrimSpace above strips only the last line's CR, which would give the final record's process id a different key.
		line = strings.TrimRight(line, "\r")
		name, pid, found := strings.Cut(line, ":")
		if found && (name == "start" || name == "shutdown" || name == "with") {
			if _, seen := ordinals[pid]; !seen {
				ordinals[pid] = len(ordinals) + 1
			}
			line = name + ":" + strconv.Itoa(ordinals[pid])
		}
		lines = append(lines, line)
	}
	return lines
}

// Pi's 2860-replaced-session-context.test.ts:147, 211 and 243 run a withSession callback from an extension command after newSession, fork and switchSession. Each SDK realization runs the same bodies through print mode: the callback receives a context of the replacement Session after it is rebound (start:2 precedes with:1), the command's captured context and API throw or fail, the replacement context's sendUserMessage returns after the replacement Session's turn, its session and state reads see the replacement Session, and a callback's error rejects the call with that error. Each Session's extensions run in their own process (D70), so process ordinals name the instances. The faux model answers "reply with exactly: <text>" with <text>, in place of the test's scripted replies.
func TestReplacedSession2860AcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes in four languages")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixtures := replaced2860Fixtures(t)
	cases := []struct {
		name     string
		messages []string
		want     []string
	}{
		{
			name:     "new",
			messages: []string{"/repro"},
			want: []string{"start:1", "shutdown:1", "start:2", "with:1", "replacement:true", "staleCtx:true", "stalePi:true", "idle:true", "model:faux-1",
				"conversation:user:reply with exactly: hello reply|assistant:hello reply", "shutdown:2"},
		},
		{
			name:     "callback error",
			messages: []string{"/throw-it"},
			want:     []string{"start:1", "shutdown:1", "start:2", "caught:callback failed", "shutdown:2"},
		},
		{
			// A replacement context the callback kept and used after the call returned must not block the extension process.
			name:     "kept context",
			messages: []string{"/keep-it"},
			want:     []string{"start:1", "shutdown:1", "start:2", "kept:returned", "shutdown:2"},
		},
		{
			name:     "fork",
			messages: []string{"reply with exactly: seed reply", "/fork-it"},
			want: []string{"start:1", "shutdown:1", "start:2",
				"conversation:user:reply with exactly: seed reply|assistant:seed reply|user:reply with exactly: fork reply|assistant:fork reply", "shutdown:2"},
		},
		{
			name:     "switch",
			messages: []string{"reply with exactly: root reply", "/mark original", "/new", "reply with exactly: target reply", "/mark target", "/switch original", "/switch-it"},
			want: []string{"start:1", "shutdown:1", "start:2", "shutdown:2", "start:3", "shutdown:3", "start:4", "switched:true",
				"conversation:user:reply with exactly: target reply|assistant:target reply|user:reply with exactly: switch reply|assistant:switch reply", "shutdown:4"},
		},
	}
	for _, sdk := range []string{"node", "go", "python", "rust"} {
		t.Run(sdk, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					if got := runReplaced2860(t, bin, fixtures[sdk], tc.messages...); !slices.Equal(got, tc.want) {
						t.Fatalf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
					}
				})
			}
		})
	}
}
