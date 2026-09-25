//go:build integration

// session_dir_test.go: --session-dir must place sessions where the caller
// asked, in every non-interactive mode.
//
// Upstream builds a single SessionManager from the resolved session dir
// (main.ts createSessionManager) and hands it to every mode, so the flag is
// uniform there. pig resolves the same value but dispatched print/json/rpc
// before applying it, silently writing to PIG_HOME instead.

package integration

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sessionFiles lists session JSONL files beneath root.
func sessionFiles(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil //nolint:nilerr // a missing tree simply has no sessions
		}
		if strings.HasSuffix(path, ".jsonl") {
			found = append(found, path)
		}
		return nil
	})
	return found
}

// runWithSessionDir runs one non-interactive turn with --session-dir and
// returns the chosen dir and the isolated PIG_HOME it must not write into.
func runWithSessionDir(t *testing.T, modeArgs []string, prompt string) (sessionDir, pigHome string) {
	t.Helper()
	sessionDir = filepath.Join(t.TempDir(), "chosen-sessions")
	pigHome = extIsolatedHome(t)

	args := append([]string{"--no-extensions", "--model", "test-faux/echo", "--session-dir", sessionDir}, modeArgs...)
	cmd := exec.Command(buildBinary(t), append(args, prompt)...)
	cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_QUIET_STARTUP=1", "PIG_TEST_FAUX=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	return sessionDir, pigHome
}

func TestSessionDirHonoredInPrintMode(t *testing.T) {
	dir, home := runWithSessionDir(t, nil, "What is 20+22?")

	if got := sessionFiles(t, dir); len(got) == 0 {
		t.Errorf("no session written under --session-dir %s", dir)
	}
	if stray := sessionFiles(t, filepath.Join(home, "agent", "sessions")); len(stray) > 0 {
		t.Errorf("--session-dir was ignored; session written to PIG_HOME instead: %v", stray)
	}
}

func TestSessionDirHonoredInJSONMode(t *testing.T) {
	dir, home := runWithSessionDir(t, []string{"--mode", "json"}, "What is 20+22?")

	if got := sessionFiles(t, dir); len(got) == 0 {
		t.Errorf("no session written under --session-dir %s", dir)
	}
	if stray := sessionFiles(t, filepath.Join(home, "agent", "sessions")); len(stray) > 0 {
		t.Errorf("--session-dir was ignored; session written to PIG_HOME instead: %v", stray)
	}
}

// Writing to the directory is only half the contract: resume must look there
// too, otherwise a caller can persist a session it can never reload.
func TestSessionDirUsedForResumeLookup(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "chosen-sessions")
	pigHome := extIsolatedHome(t)
	const id = "session-dir-resume"

	run := func(prompt string) string {
		t.Helper()
		cmd := exec.Command(buildBinary(t),
			"--no-extensions", "--mode", "json", "--model", "test-faux/echo",
			"--session-dir", sessionDir, "--session-id", id, prompt)
		cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_QUIET_STARTUP=1", "PIG_TEST_FAUX=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run failed: %v\n%s", err, out)
		}
		return string(out)
	}

	run("Remember this exact code: the letters A L P H A and the digits 7 7 4 9")
	if out := run("What was the code I told you to remember?"); !strings.Contains(out, "ALPHA-7749") {
		t.Fatalf("resume did not read from --session-dir; want ALPHA-7749 in:\n%s", out)
	}
}

// runRPCTurn drives one prompt through --mode rpc and returns the session dir
// and PIG_HOME it was given. RPC exits on stdin EOF, so stdin is held open
// until agent_settled shows the turn finished and the session is flushed;
// closing it earlier truncates the run before anything reaches disk.
func runRPCTurn(t *testing.T, extra ...string) (sessionDir, pigHome string) {
	t.Helper()
	sessionDir = filepath.Join(t.TempDir(), "chosen-sessions")
	pigHome = extIsolatedHome(t)

	args := append([]string{
		"--no-extensions", "--mode", "rpc",
		"--model", "test-faux/echo", "--session-dir", sessionDir,
	}, extra...)
	cmd := exec.Command(buildBinary(t), args...)
	cmd.Env = append(os.Environ(), "PIG_HOME="+pigHome, "PIG_QUIET_STARTUP=1", "PIG_TEST_FAUX=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, `{"id":"p1","type":"prompt","message":"What is 20+22?"}`+"\n"); err != nil {
		t.Fatal(err)
	}

	settled := make(chan struct{})
	go func() {
		defer close(settled)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), `"agent_settled"`) {
				return
			}
		}
	}()

	select {
	case <-settled:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("rpc turn did not settle within 60s")
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("rpc run failed: %v", err)
	}
	return sessionDir, pigHome
}

func TestRPCModeHonorsSessionFlags(t *testing.T) {
	t.Run("session-dir", func(t *testing.T) {
		dir, home := runRPCTurn(t)
		if len(sessionFiles(t, dir)) == 0 {
			t.Errorf("no session written under --session-dir %s", dir)
		}
		if stray := sessionFiles(t, filepath.Join(home, "agent", "sessions")); len(stray) > 0 {
			t.Errorf("--session-dir ignored; wrote to PIG_HOME: %v", stray)
		}
	})

	// The strictest of the three: persisting a session the caller asked not to
	// keep leaks transcript content to disk.
	t.Run("no-session", func(t *testing.T) {
		dir, home := runRPCTurn(t, "--no-session")
		for _, loc := range []string{dir, filepath.Join(home, "agent", "sessions")} {
			if got := sessionFiles(t, loc); len(got) > 0 {
				t.Errorf("--no-session ignored; session persisted at %v", got)
			}
		}
	})

	t.Run("session-id", func(t *testing.T) {
		const id = "rpc-flag-probe"
		dir, _ := runRPCTurn(t, "--session-id", id)
		var named int
		for _, f := range sessionFiles(t, dir) {
			if strings.Contains(f, id) {
				named++
			}
		}
		if named != 1 {
			t.Errorf("--session-id ignored; %d files named %q in %v", named, id, sessionFiles(t, dir))
		}
	})
}
