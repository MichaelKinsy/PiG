package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func captureCLIErrorStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	os.Stderr = old
	_ = w.Close()
	return <-done
}

// chalk takes its level from stdout (supports-color), whatever stream it writes to: Pi under script(1) prints
// "\x1b[31mError: @file arguments are not supported in RPC mode\x1b[39m" on the terminal, and the plain text when only stdout is
// redirected; FORCE_COLOR=1 colors it with no terminal at all. In a test stdout and stderr are pipes, so only FORCE_COLOR decides.
func TestCLIErrorColorFollowsChalkStdoutLevel(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	if got, want := captureCLIErrorStderr(t, func() { printCLIError("boom") }), "\x1b[31mError: boom\x1b[39m\n"; got != want {
		t.Errorf("FORCE_COLOR=1: got %q, want %q", got, want)
	}
	t.Setenv("FORCE_COLOR", "0")
	if got, want := captureCLIErrorStderr(t, func() { printCLIError("boom") }), "Error: boom\n"; got != want {
		t.Errorf("FORCE_COLOR=0: got %q, want %q", got, want)
	}
}

// main.ts rejects @file arguments in RPC mode (exit 1), and handleConfigCommand prints chalk.red("Project is not trusted. Use --approve
// to modify local resource config.") without an Error label. Both probed on Pi 1.1.0 with FORCE_COLOR=1.
func TestRPCFileArgsAndUntrustedConfigMatchPi(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	run := func(args ...string) (string, int) {
		t.Helper()
		home := t.TempDir()
		work := t.TempDir()
		cmd := exec.Command(bin, args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "HOME="+home, "FORCE_COLOR=1")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), code
	}
	if out, code := run("--mode", "rpc", "@notes.txt"); code != 1 || out != "\x1b[31mError: @file arguments are not supported in RPC mode\x1b[39m\n" {
		t.Errorf("--mode rpc @file: exit %d, output %q", code, out)
	}
	if out, code := run("config", "-l", "--no-approve"); code != 1 || !strings.HasSuffix(out, "\x1b[31mProject is not trusted. Use --approve to modify local resource config.\x1b[39m\n") {
		t.Errorf("untrusted config -l: exit %d, output %q", code, out)
	}
}
