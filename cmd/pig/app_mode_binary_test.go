//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// runPigForModeTest runs the pig binary with the faux provider and returns its
// stdout, stderr, and exit code. stdin and stdout are the given files; a nil
// stdout captures into a pipe.
func runPigForModeTest(t *testing.T, bin string, stdin, stdout *os.File, args ...string) (string, string, int) {
	t.Helper()
	return runPigForModeTestIn(t, bin, t.TempDir(), stdin, stdout, args...)
}

// runPigForModeTestIn is runPigForModeTest with the agent directory, which
// holds the run's session files, chosen by the caller. Runs sharing an agent
// directory also share a working directory, so --continue finds the session.
func runPigForModeTestIn(t *testing.T, bin, agentDir string, stdin, stdout *os.File, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	workDir := filepath.Join(agentDir, "work")
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"--model", "test-faux/faux-1", "--no-extensions"}, args...)...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"PIG_HOME="+t.TempDir(),
		"PIG_CODING_AGENT_DIR="+agentDir,
		"PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO=parity-basic",
	)
	cmd.Stdin = stdin
	var outBuf, errBuf bytes.Buffer
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = &outBuf
	}
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("pig %v did not exit (interactive mode waiting for keys?)\nstdout: %q\nstderr: %s", args, outBuf.String(), errBuf.String())
	}
	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run pig: %v", err)
	}
	return outBuf.String(), errBuf.String(), code
}

// TestAppModeFollowsStandardStreams runs the pig binary the way a shell does
// and pins upstream main.ts mode selection end to end: a stdout that is not a
// terminal, or a stdin that is not a terminal, selects print mode even when
// the other stream is a terminal.
func TestAppModeFollowsStandardStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)

	// `pig "q" > out.txt` from a terminal: stdin is a terminal, stdout a file.
	t.Run("stdout redirected to a file", func(t *testing.T) {
		master, slave := openModePTY(t)
		defer func() { _ = master.Close() }()
		defer func() { _ = slave.Close() }()
		go func() { _, _ = io.Copy(io.Discard, master) }()
		outPath := filepath.Join(t.TempDir(), "out.txt")
		out, err := os.Create(outPath)
		if err != nil {
			t.Fatal(err)
		}
		_, stderr, code := runPigForModeTest(t, bin, slave, out, "reply with exactly: redirected-ok")
		_ = out.Close()
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 || string(data) != "redirected-ok\n" {
			t.Fatalf("exit %d, file %q, stderr %s; want print mode output %q", code, data, stderr, "redirected-ok\n")
		}
	})

	// `pig < /dev/null`: no message, stdin not a terminal. Upstream runs print
	// mode, sends nothing, and exits 0.
	t.Run("stdin not a terminal without a message", func(t *testing.T) {
		devNull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = devNull.Close() }()
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil)
		if code != 0 || strings.TrimSpace(stdout) != "" {
			t.Fatalf("exit %d, stdout %q, stderr %s; want exit 0 and no output", code, stdout, stderr)
		}
	})
}

// TestPrintModeWithoutMessageReportsLastMessage ports upstream
// print-mode.test.ts "returns non-zero on assistant error": text mode reports
// the session's last message even when no prompt is sent, so `pig -c -p` on a
// session that ended in an error prints that error and exits 1.
func TestPrintModeWithoutMessageReportsLastMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	agentDir := t.TempDir()
	const failure = `test-faux: unhandled request "no scripted answer"`
	_, stderr, code := runPigForModeTestIn(t, bin, agentDir, devNull, nil, "-p", "no scripted answer")
	if code != 1 || strings.TrimSpace(stderr) != failure {
		t.Fatalf("first run: exit %d, stderr %q; want exit 1 with %q", code, stderr, failure)
	}
	stdout, stderr, code := runPigForModeTestIn(t, bin, agentDir, devNull, nil, "-c", "-p")
	if code != 1 || strings.TrimSpace(stderr) != failure || stdout != "" {
		t.Fatalf("no-message run: exit %d, stdout %q, stderr %q; want exit 1 with %q", code, stdout, stderr, failure)
	}
}

// TestPrintModeSendsEveryPositionalMessage pins upstream main.ts, which
// passes every positional message after the first to print mode, and
// print-mode.ts, which sends each as its own prompt after the initial message.
// Pig sent only the first and exited 0.
func TestPrintModeSendsEveryPositionalMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	t.Run("text", func(t *testing.T) {
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "-p", "reply with exactly: first", "reply with exactly: second")
		if code != 0 || stdout != "second\n" {
			t.Fatalf("exit %d, stdout %q, stderr %s; want the last prompt's answer %q", code, stdout, stderr, "second\n")
		}
	})

	t.Run("json", func(t *testing.T) {
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "--mode", "json", "reply with exactly: first", "reply with exactly: second")
		if code != 0 {
			t.Fatalf("exit %d, stderr %s", code, stderr)
		}
		var prompts []string
		for _, line := range jsonModeLines(t, stdout) {
			message, _ := line["message"].(map[string]any)
			if line["type"] != "message_end" || message["role"] != "user" {
				continue
			}
			content, _ := message["content"].([]any)
			for _, block := range content {
				if block, ok := block.(map[string]any); ok && block["type"] == "text" {
					prompts = append(prompts, block["text"].(string))
				}
			}
		}
		if want := []string{"reply with exactly: first", "reply with exactly: second"}; strings.Join(prompts, "|") != strings.Join(want, "|") {
			t.Fatalf("user messages = %q, want %q", prompts, want)
		}
	})

	// Upstream json mode with no message writes the session header and sends
	// nothing; pig refused to start.
	t.Run("json without a message", func(t *testing.T) {
		stdout, stderr, code := runPigForModeTest(t, bin, devNull, nil, "--mode", "json")
		lines := jsonModeLines(t, stdout)
		if code != 0 || len(lines) != 1 || lines[0]["type"] != "session" {
			t.Fatalf("exit %d, stdout %q, stderr %s; want only the session header", code, stdout, stderr)
		}
	})
}

// TestPrintModeWaitsForExtensionPromptFromAgentSettled pins upstream print
// mode, which binds the session to extensions: a prompt an agent_settled
// handler sends runs before the initial prompt resolves, so the printed
// answer is that run's. Pig bound no session actions in print mode, so
// sendUserMessage failed and the first answer was printed.
func TestPrintModeWaitsForExtensionPromptFromAgentSettled(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-actions.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--model", "test-faux/faux-1", "-e", fixture, "-p", "reply with exactly: loop-start")
	cmd.Dir = t.TempDir()
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.Stdin = devNull
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "loop-done\n" {
		t.Fatalf("stdout = %q, want the extension-sent prompt's answer %q\nstderr: %s", stdout.String(), "loop-done\n", stderr.String())
	}
}

// pig --fork <id> --no-session exits 1 with Pi's error before any session
// work (main.ts validateForkFlags).
func TestForkWithNoSessionExitsWithUpstreamError(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	stdout, stderr, code := runPigForModeTest(t, bin, nil, nil, "-p", "--fork", "abc", "--no-session", "hello")
	if code != 1 || stderr != "Error: --fork cannot be combined with --no-session\n" || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// Pi prints the version before validateForkFlags runs, so a conflicting
// --fork does not stop --version (main.ts order).
func TestVersionPrecedesForkConflictCheck(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	stdout, stderr, code := runPigForModeTest(t, bin, nil, nil, "--version", "--fork", "x", "--no-session")
	if code != 0 || stderr != "" || strings.TrimSpace(stdout) != strings.TrimSpace(cliVersionString()) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// Pi exports before validateForkFlags runs, so a conflicting --fork does not
// stop --export (main.ts order).
func TestExportPrecedesForkConflictCheck(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	header := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}` + "\n"
	if err := os.WriteFile(session, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.html")
	_, stderr, code := runPigForModeTest(t, bin, nil, nil, "--export", session, out, "--fork", "x", "--no-session")
	if code != 0 || !strings.HasPrefix(stderr, "Exported to: ") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
