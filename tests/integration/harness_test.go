//go:build integration

package integration

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
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
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH:", err)
	}
	bin := buildBinary(t)
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
	h := &harness{t: t, session: session, bin: bin}
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

func (h *harness) kill() {
	_ = tmuxCommand("kill-session", "-t", h.session).Run()
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
