package cli

// pi: packages/coding-agent/src/main.ts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// unmakeableAgentDirForCLI is an agent directory under a regular file: PIG_CODING_AGENT_DIR names it, and nothing can make it.
func unmakeableAgentDirForCLI(t *testing.T) (agentDir, cwd string) {
	t.Helper()
	root := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cwd = filepath.Join(root, "cwd")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "file", "agent"), cwd
}

// A new session's directory is made when the startup creates the session (main.ts:744 createSessionManager: SessionManager.create makes sessionDir, or getDefaultSessionDir's, session-manager.ts:597-602,1018-1020), before the project trust store, the model or any auth check: a directory that cannot be made ends the startup with Node's mkdir error for the SESSION directory. PiG made it later, in the mode, so the trust store's own mkdir error (for the agent directory) came first.
func TestStartupMakesTheDirectoryOfANewSessionBeforeAnythingElse(t *testing.T) {
	agentDir, cwd := unmakeableAgentDirForCLI(t)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	var want string
	pioracle.Run(t, `
let message = "ok";
try { const { SessionManager } = await load("pi-coding-agent/core/session-manager.js"); SessionManager.create(input.cwd, undefined); } catch (error) { message = error.message; }
emit(message);`, map[string]string{"cwd": cwd}, &want, "PI_CODING_AGENT_DIR="+agentDir)
	if !strings.HasPrefix(want, "ENOTDIR: not a directory, mkdir '"+filepath.Join(agentDir, "sessions")+string(filepath.Separator)) {
		t.Fatalf("Pi's SessionManager.create = %q, want ENOTDIR for the session directory under the agent directory", want)
	}
	selection, err := resolveStartupSessionSelectionIn(Args{}, cwd, "")
	if err == nil || err.Error() != want {
		t.Fatalf("selection = %+v, error %v; want Pi's %q", selection, err, want)
	}
	// An explicit directory is the one made, and a directory that can be made is made private (D132).
	explicit := filepath.Join(t.TempDir(), "made", "sessions")
	if _, err := resolveStartupSessionSelectionIn(Args{}, cwd, explicit); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(explicit); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("explicit session directory = %v, %v; want it made with mode 0700", info, err)
	}
	if _, err := resolveStartupSessionSelectionIn(Args{NoSession: true}, cwd, ""); err != nil {
		t.Fatalf("--no-session makes no session directory (main.ts:411), error %v", err)
	}
}

func runPigWithUnmakeableAgentDir(t *testing.T, bin, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	agentDir, cwd := unmakeableAgentDirForCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--model", "test-faux/faux-1", "--no-extensions", "--offline"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+filepath.Dir(cwd), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("pig %v did not exit\nstdout: %q\nstderr: %s", args, out.String(), errOut.String())
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run pig: %v", err)
	}
	return strings.ReplaceAll(out.String(), agentDir, "<agent>"), strings.ReplaceAll(errOut.String(), agentDir, "<agent>"), code
}

// Print and RPC mode on an agent directory that cannot be made. Pi 1.5.2 (probed with the real CLI, test/parity scenario startup/25) ends both at startup with exit 1 and an uncaught Node error and writes no response: with --no-session at the project trust store's lock (trust-manager.ts acquireTrustLockSync: mkdir of the agent directory), with the default session manager earlier, at the session directory (main.ts:744). PiG prints the same error text in its one-line form, and never its own "open auth storage" text.
func TestPrintAndRPCModeEndWithNodesMkdirErrorWhereTheAgentDirectoryCannotBeMade(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the pig binary")
	}
	bin := buildPigBinaryForSignalTest(t)
	modes := map[string][]string{
		"print": {"-p", "reply with exactly: ok"},
		"rpc":   {"--mode", "rpc"},
	}
	for mode, args := range modes {
		for name, row := range map[string]struct {
			extra []string
			want  string
		}{
			"no session":              {[]string{"--no-session"}, "ENOTDIR: not a directory, mkdir '<agent>'"},
			"default session manager": {nil, "ENOTDIR: not a directory, mkdir '" + filepath.Join("<agent>", "sessions", "--")},
		} {
			t.Run(mode+"/"+name, func(t *testing.T) {
				stdout, stderr, code := runPigWithUnmakeableAgentDir(t, bin, `{"id":"state","type":"get_state"}`+"\n", append(append([]string{}, row.extra...), args...)...)
				if code != 1 || stdout != "" || !strings.Contains(stderr, row.want) || strings.Contains(stderr, "auth storage") {
					t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1, no stdout and %q", code, stdout, stderr, row.want)
				}
			})
		}
	}
}
