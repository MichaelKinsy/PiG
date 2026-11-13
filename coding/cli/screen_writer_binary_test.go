// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package cli

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The parts of a JSON or RPC run that differ between two runs of the same command: the session's id, alone and in its file's name, and the times:
// when a message was made, and how long a turn took (durationMs, which the faux model's reply makes 0 in most runs and 1 in some).
var screenWriterRunVarying = regexp.MustCompile(`[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}|\d{4}-\d{2}-\d{2}T\d{2}[:-]\d{2}[:-]\d{2}[.-]\d{3}Z|"timestamp":\d+|"durationMs":\d+`)

// A function of the screen writer in an allocation profile's string table: one of internal/screenwriter, or one of the tui's named for it.
var screenWriterFunction = regexp.MustCompile(`github\.com/MichaelKinsy/PiG/(?:internal/screenwriter\.[\w.()*]+|[\w/]+\.[\w.()*]*ScreenWriter[\w.()*]*)`)

// Two runs of one command differ in their times and in nothing else that screenWriterRunVarying has to cover. The two lines are an RPC
// run's end of the assistant's message as two runs of the same command wrote it while the machine was busy: the first run's turn took
// 1 ms and five of the eleven after it 0 ms, and TestNoScreenWriterOutsideInteractiveMode/rpc compared the durations.
func TestScreenWriterRunVaryingCoversTheTimes(t *testing.T) {
	const first = `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","timestamp":1794513125383,"durationMs":1,"thinkingLevel":"off"}}`
	const later = `{"type":"message_end","message":{"role":"assistant","stopReason":"stop","timestamp":1794513129017,"durationMs":0,"thinkingLevel":"off"}}`
	a, b := screenWriterRunVarying.ReplaceAllString(first, "<varies>"), screenWriterRunVarying.ReplaceAllString(later, "<varies>")
	if a != b {
		t.Errorf("two runs that differ only in their times compare as different:\n%s\n%s", a, b)
	}
	if !strings.Contains(a, `"stopReason":"stop"`) || !strings.Contains(a, `"thinkingLevel":"off"`) {
		t.Errorf("more than the times is left out of the comparison: %s", a)
	}
}

// The screen writer (D624) is built when an interactive run starts and never before, and the built binary shows it: under every value of
// PIG_TUI_SCREEN_WRITER and of terminal.screenWriter, `pig --version` and a print, a JSON and an RPC run write the same bytes (but for
// screenWriterRunVarying), and none of their allocations, package initializers included, is made by a function of the screen writer.
// The allocations are PIG_PROFILE=allocs with every allocation sampled (GODEBUG=memprofilerate=1).
func TestNoScreenWriterOutsideInteractiveMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	fixture := newStartupFixture(t)
	ctx := testbudget.Context(t)
	model := []string{"--model", "test-faux/faux-1", "--no-extensions"}
	for _, mode := range []struct {
		name, stdin string
		args        []string
	}{
		{"version", "", []string{"--version"}},
		{"print", "", startupPrompt},
		{"json", "", append(model[:len(model):len(model)], "--mode", "json", "reply with exactly: ok")},
		{"rpc", `{"id":"1","type":"get_state"}` + "\n" + `{"id":"2","type":"prompt","message":"reply with exactly: ok"}` + "\n", append(model[:len(model):len(model)], "--mode", "rpc")},
	} {
		t.Run(mode.name, func(t *testing.T) {
			var first string
			for _, setting := range []string{"", "writer", "pi"} {
				settings := filepath.Join(fixture.agentDir, "settings.json")
				if err := os.RemoveAll(settings); err != nil {
					t.Fatal(err)
				}
				if setting != "" {
					if err := os.WriteFile(settings, []byte(`{"terminal":{"screenWriter":"`+setting+`"}}`), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				for _, variable := range []string{"unset", "", "1", "pi"} {
					output, functions := fixture.runForScreenWriter(ctx, t, variable, mode.stdin, mode.args...)
					if len(functions) != 0 {
						t.Errorf("terminal.screenWriter %q, PIG_TUI_SCREEN_WRITER %q: allocations by %q", setting, variable, functions)
					}
					output = screenWriterRunVarying.ReplaceAllString(output, "<varies>")
					if first == "" {
						first = output
					}
					if output == "" || output != first {
						t.Errorf("terminal.screenWriter %q, PIG_TUI_SCREEN_WRITER %q wrote\n%s\nwant what the first run wrote\n%s", setting, variable, output, first)
					}
				}
			}
		})
	}
}

// runForScreenWriter runs pig with args and PIG_TUI_SCREEN_WRITER set to variable ("unset" leaves it out), and returns what it wrote to
// stdout and stderr and the screen writer's functions in its allocation profile. A run with stdin is an RPC run: it is sent stdin and
// ends when its agent has ended and stdin is closed.
func (f *startupFixture) runForScreenWriter(ctx context.Context, t *testing.T, variable, stdin string, args ...string) (output string, functions []string) {
	t.Helper()
	dir, err := os.MkdirTemp(f.tmp, "profile-")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, f.binary, args...)
	command.Dir = f.cwd
	command.Env = append(measuredEnvironment(os.Environ()), "TMPDIR="+f.tmp, "PIG_HOME="+f.home, "PIG_CODING_AGENT_DIR="+f.agentDir, "PIG_PROFILE=allocs", "PIG_PROFILE_DIR="+dir, "GODEBUG=memprofilerate=1",
		"GOMAXPROCS=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	if variable != "unset" {
		command.Env = append(command.Env, "PIG_TUI_SCREEN_WRITER="+variable)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(input, stdin); err != nil {
		t.Fatal(err)
	}
	if stdin == "" {
		_ = input.Close()
	}
	var wrote strings.Builder
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		wrote.WriteString(line)
		if stdin != "" && strings.Contains(line, `"type":"agent_end"`) {
			_ = input.Close()
		}
		if err != nil {
			break
		}
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("pig %v: %v\n%s%s", args, err, wrote.String(), stderr.String())
	}
	files, _ := filepath.Glob(filepath.Join(dir, "pig-*-allocs.pprof"))
	if len(files) != 1 {
		t.Fatalf("want one allocation profile, got %v", files)
	}
	file, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	unpacked, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := io.ReadAll(unpacked)
	// The profile holds the package initializers: the process's terminal is built by one.
	if err != nil || !bytes.Contains(profile, []byte("/tui.newProcessTerminal")) {
		t.Fatalf("allocation profile %s: %v; it does not hold the tui's initializer", files[0], err)
	}
	// stderr names the profile's file, which differs by run.
	return wrote.String() + strings.ReplaceAll(stderr.String(), files[0], "<profile>"), screenWriterFunction.FindAllString(string(profile), -1)
}
