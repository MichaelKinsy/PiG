//go:build integration

package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// harness manages a single tmux session running /tmp/pig-test.
type harness struct {
	t       *testing.T
	session string
	bin     string
	// pane is the process ID of the session's pane shell. tmux starts each pane's shell as the leader of a terminal session, so the program a test launches and everything that program starts belong to terminal session pane until one of them calls setsid.
	pane int
	// livePaneProcesses lists the processes of terminal session pane that have not exited. A test replaces it to see when kill begins to wait.
	livePaneProcesses func(session int) ([]int, error)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	requireTmux(t)
	bin := buildBinary(t)
	// The first TempDir call registers the removal of the test's temporary directories, and cleanups run last in, first out. Calling it before registering kill makes kill run before that removal even when the test creates its first directory only after newHarness.
	_ = t.TempDir()
	id := randID()
	session := "pig-it-" + id
	cmd := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "36")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}
	// Force extended-keys on for this session so logical keystrokes
	// like S-Enter encode as CSI-u when the app enables them. The
	// user's personal tmux config has these set globally, but we set
	// them per-session for reproducibility in CI.
	if out, err := tmuxCommand("set-option", "-t", session, "-g", "extended-keys", "on").CombinedOutput(); err != nil {
		t.Fatalf("tmux set-option extended-keys: %v\n%s", err, out)
	}
	// extended-keys-format arrived in tmux 3.5; older tmux (Ubuntu 24.04
	// ships 3.4) has no such option and always uses its one format.
	if out, err := tmuxCommand("set-option", "-t", session, "-g", "extended-keys-format", "csi-u").CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "invalid option") {
			t.Fatalf("tmux set-option extended-keys-format: %v\n%s", err, out)
		}
		t.Logf("tmux has no extended-keys-format option (tmux 3.5+); using its default key format")
	}
	pane, err := paneSessionID(session)
	if err != nil {
		_ = tmuxCommand("kill-session", "-t", session).Run()
		t.Fatal(err)
	}
	h := &harness{t: t, session: session, bin: bin, pane: pane, livePaneProcesses: livePaneProcesses}
	t.Cleanup(h.kill)
	return h
}

// start launches pig in the session. Unless the caller supplies its own
// PIG_HOME, the run gets an empty ephemeral one: without it pig reads the
// developer's real ~/.pig and the session inherits their model, extensions and
// footer, so assertions depend on whoever ran the test.
func (h *harness) start(extraEnv ...string) {
	h.t.Helper()
	h.startArgs(nil, extraEnv...)
}

// startArgs launches Pig with additional command-line arguments. Integration tests exercise behavior after startup, so the harness stores an explicit trust decision in its isolated home.
func (h *harness) startArgs(args []string, extraEnv ...string) {
	h.t.Helper()
	h.startArgsAt(args, "", extraEnv...)
}

func (h *harness) startArgsAt(args []string, cwd string, extraEnv ...string) {
	h.t.Helper()
	h.startArgsWithProjectTrustAt(args, cwd, true, extraEnv...)
}

func (h *harness) startArgsWithProjectTrust(args []string, trusted bool, extraEnv ...string) {
	h.t.Helper()
	h.startArgsWithProjectTrustAt(args, "", trusted, extraEnv...)
}

func (h *harness) startArgsWithProjectTrustAt(args []string, cwd string, trusted bool, extraEnv ...string) {
	h.t.Helper()
	env := append([]string{}, extraEnv...)
	pigHome := ""
	offlineSet := false
	for _, assignment := range env {
		if value, ok := strings.CutPrefix(assignment, "PIG_HOME="); ok {
			pigHome = value
		}
		if strings.HasPrefix(assignment, "PIG_OFFLINE=") {
			offlineSet = true
		}
	}
	if pigHome == "" {
		pigHome = h.t.TempDir()
		env = append([]string{"PIG_HOME=" + pigHome}, env...)
	}
	if !offlineSet {
		env = append([]string{"PIG_OFFLINE=1"}, env...)
	}
	if trusted {
		trustIntegrationProjectAt(h.t, pigHome, cwd)
	}
	seedIntegrationSettings(h.t, pigHome)
	envPrefix := strings.Join(env, " ")
	if envPrefix != "" {
		envPrefix += " "
	}
	cmd := envPrefix + h.bin
	if cwd != "" {
		cmd = "cd '" + strings.ReplaceAll(cwd, "'", "'\\''") + "' && " + cmd
	}
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	cmd += " 2>/dev/null"
	h.send("clear && " + cmd + "\r")
	time.Sleep(2500 * time.Millisecond)
}

func TestHarnessUntrustedProjectStillPrompts(t *testing.T) {
	h := newHarness(t)
	h.startArgsWithProjectTrust(nil, false)
	h.expectContains(startupReadyWait, "Trust project folder?")
}

func (h *harness) send(s string) {
	h.t.Helper()
	cmd := tmuxCommand("send-keys", "-t", h.session, "-l", s)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("tmux send-keys: %v\n%s", err, out)
	}
}

// sendKey sends a logical keystroke (e.g. "S-Enter", "C-c", "Up")
// via tmux's named-key form: NO `-l` flag, so tmux treats the
// argument as a key name and encodes it according to the
// extended-keys protocol the running app has enabled.
//
// Use this (not send) when the test needs to exercise the
// modifier-key encoding path. send injects raw bytes and bypasses
// the encoding entirely.
func (h *harness) sendKey(name string) {
	h.t.Helper()
	cmd := tmuxCommand("send-keys", "-t", h.session, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("tmux send-keys %s: %v\n%s", name, err, out)
	}
}

// paste delivers text the way a terminal delivers Cmd+V or Ctrl+Shift+V:
// wrapped in bracketed-paste markers once pig has enabled that mode.
func (h *harness) paste(text string) {
	h.t.Helper()
	buffer := "pig-it-paste-" + randID()
	if out, err := tmuxCommand("set-buffer", "-b", buffer, text).CombinedOutput(); err != nil {
		h.t.Fatalf("tmux set-buffer: %v\n%s", err, out)
	}
	if out, err := tmuxCommand("paste-buffer", "-p", "-d", "-b", buffer, "-t", h.session).CombinedOutput(); err != nil {
		h.t.Fatalf("tmux paste-buffer: %v\n%s", err, out)
	}
}

// sendEnter sends a literal CR (the Enter key as pig sees it).

// capture returns the visible pane text.
func (h *harness) capture() string {
	h.t.Helper()
	cmd := tmuxCommand("capture-pane", "-t", h.session, "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("tmux capture-pane: %v\n%s", err, out)
	}
	return string(out)
}

// interactiveReadyMarker is Pi's auto-compaction footer, painted in regular and fullscreen modes even with quietStartup. Startup help is optional and is not a readiness signal. Interaction assertions still prove that input dispatch and extension startup have completed.
const interactiveReadyMarker = "(auto)"

// hotkeysLastRow is the action text of the last row Pi's /hotkeys draws (interactive-mode.ts:6955-7023): the Navigation, Editing and Other tables come first, and an Extensions table follows only when an extension registers a shortcut. With none registered, that row is the bottom line of the chat, so it stays on screen after the earlier rows scroll off a pane, and it appears only once the command has dispatched. A row from an earlier table scrolls away.
const hotkeysLastRow = "Run bash command (excluded from context)"

// startupReadyWait bounds process spawn, resource loading and initial paint. Interaction waits stay short so a genuine hang still fails fast.
const startupReadyWait = 30 * time.Second

// expectContains polls capture until it contains every needle, or times out. Each needle is searched independently.
func (h *harness) expectContains(timeout time.Duration, needles ...string) string {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	var pane string
	for time.Now().Before(deadline) {
		pane = h.capture()
		ok := true
		for _, n := range needles {
			if !strings.Contains(pane, n) {
				ok = false
				break
			}
		}
		if ok {
			return pane
		}
		time.Sleep(150 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %v\n--- pane ---\n%s", needles, pane)
	return pane
}

// paneExitWait bounds how long kill waits for the session's processes to exit after tmux hangs them up. Pig exits within seconds of a hangup, so a process still running after this is a hung shutdown.
const paneExitWait = 30 * time.Second

// kill ends the session and returns once every process in it has exited; see hangUpSession.
func (h *harness) kill() {
	_ = hangUpSession(h.t, h.session, h.pane, h.livePaneProcesses)
}

// killSession is kill for a tmux session a test started without the harness. It looks the pane up at the call, so a session that is already gone is only hung up, and it returns the error of tmux kill-session.
func killSession(t testing.TB, session string) error {
	t.Helper()
	pane, err := paneSessionID(session)
	if err != nil {
		return tmuxCommand("kill-session", "-t", session).Run()
	}
	return hangUpSession(t, session, pane, livePaneProcesses)
}

// hangUpSession ends the tmux session whose pane shell has process ID pane and returns once every process in it has exited. tmux kill-session returns when its server has dropped the session; it hangs the pane's terminal up and does not wait for anything the pane started. Pig handles the hangup as a termination signal, and before it exits it writes its exit record into the agent directory, so a test that removes its PIG_HOME right after kill-session races that write and fails with "directory not empty". It returns the error of tmux kill-session; a process that does not exit, or a failure to list the processes, fails t.
func hangUpSession(t testing.TB, session string, pane int, live func(session int) ([]int, error)) error {
	t.Helper()
	out, killErr := tmuxCommand("kill-session", "-t", session).CombinedOutput()
	if killErr != nil {
		killErr = fmt.Errorf("%w: %s", killErr, bytes.TrimSpace(out))
	}
	var alive []int
	var err error
	if !waitFor(paneExitWait, func() bool {
		alive, err = live(pane)
		return err != nil || len(alive) == 0
	}) {
		t.Errorf("processes %v of tmux session %s still running %s after kill-session", alive, session, paneExitWait)
	}
	if err != nil {
		t.Errorf("list processes of tmux session %s: %v", session, err)
	}
	return killErr
}

// paneSessionID returns the ID of the terminal session that the pane shell of tmux session session leads; see harness.pane.
func paneSessionID(session string) (int, error) {
	out, err := tmuxCommand("list-panes", "-t", session, "-F", "#{pane_pid}").Output()
	if err != nil {
		return 0, fmt.Errorf("tmux list-panes -t %s: %w", session, err)
	}
	pane, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("tmux pane_pid %q: %w", out, err)
	}
	return pane, nil
}

// livePaneProcesses lists the processes of terminal session sid that have not exited. A zombie has exited and writes nothing more; it stays in the process table until its parent reaps it, which on a loaded host can lag the exit by seconds, so it does not count.
func livePaneProcesses(sid int) ([]int, error) {
	members, err := exec.Command("pgrep", "-s", strconv.Itoa(sid)).Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 1 {
			return nil, nil // pgrep exits 1 when no process matches.
		}
		return nil, fmt.Errorf("pgrep -s %d: %w", sid, err)
	}
	var stderr bytes.Buffer
	states := exec.Command("ps", "-o", "pid=,stat=", "-p", strings.Join(strings.Fields(string(members)), ","))
	states.Stderr = &stderr
	out, err := states.Output()
	if err != nil {
		// ps exits 1 without a word when every listed process has gone since pgrep listed it.
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 1 && stderr.Len() == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("ps: %w: %s", err, stderr.String())
	}
	var live []int
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("ps reported %q, want a process ID and a state", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("ps process ID %q: %w", fields[0], err)
		}
		if !strings.HasPrefix(fields[1], "Z") {
			live = append(live, pid)
		}
	}
	return live, nil
}

// buildBinary returns the cached path to a built ./cmd/pig binary.
// The actual build happens in TestMain so the binary survives across
// per-test t.TempDir cleanups.
var binaryPath string

func buildBinary(t *testing.T) string {
	t.Helper()
	if binaryPath == "" {
		t.Skip("binary not built (TestMain didn't run)")
	}
	return binaryPath
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func randID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// trustIntegrationProject records only this launch's fixture decision. Explicit
// untrusted-startup tests bypass it and continue exercising the real prompt.
func trustIntegrationProject(t *testing.T, pigHome string) {
	t.Helper()
	trustIntegrationProjectAt(t, pigHome, "")
}

func trustIntegrationProjectAt(t *testing.T, pigHome, cwd string) {
	t.Helper()
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := codingagent.NewProjectTrustStore(filepath.Join(pigHome, "agent")).Set(cwd, new(true)); err != nil {
		t.Fatalf("store project trust: %v", err)
	}
}

// seedIntegrationSettings gives the isolated agent directory a settings.json when it has none, so an interactive start skips the first-time setup dialog (D88), as Pi's does once settings.json exists.
func seedIntegrationSettings(t *testing.T, pigHome string) {
	t.Helper()
	agentDir := filepath.Join(pigHome, "agent")
	path := filepath.Join(agentDir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		return
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
