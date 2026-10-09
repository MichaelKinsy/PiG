//go:build linux

package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// startupDriftFixture is an agent directory whose extension was written for an older SDK.
func startupDriftFixture(t *testing.T) (home, agentDir, extension string) {
	t.Helper()
	home = t.TempDir()
	agentDir = filepath.Join(home, "agent")
	extension = filepath.Join(agentDir, "extensions", "ask")
	writeOldExtension(t, extension)
	return home, agentDir, extension
}

func runDriftStartup(t *testing.T, binary, home, agentDir string, extraEnv []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"--model", "test-faux/faux-1", "--offline"}, args...)...)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PI_HOME="+filepath.Join(home, "pi"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic")
	cmd.Env = append(cmd.Env, extraEnv...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), exitCode
}

// Print mode prints the changed APIs and the exact command, exits 1 as it
// does for any extension load error, writes nothing to stdout, and does not
// touch the extension.
func TestStartupNamesSDKDriftAndExitsInPrintMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, agentDir, extension := startupDriftFixture(t)
	stdout, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "hello")
	if code != 1 || stdout != "" {
		t.Fatalf("exit=%d stdout=%q\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		`Extension "ask" was written for an older SDK:`,
		"Context.GetSessionID: func() string -> func() (string, error) (SDK 0.3.0)",
		"SendMessageOptions.TriggerTurn: bool -> *bool (SDK 0.2.0)",
		"Run: pig extension upgrade " + extension + "\n",
		"written for an older SDK (",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if readFile(t, filepath.Join(extension, "extension.go")) != oldExtensionSource {
		t.Fatal("print mode edited the extension")
	}
	if _, err := os.Stat(filepath.Join(home, "pig", "state", "extension-upgrade")); err == nil {
		t.Fatal("print mode wrote an upgrade backup")
	}
}

// extensionsAutoUpgrade upgrades at startup, prints one line, and the run then
// loads the extension and finishes.
func TestStartupAutoUpgradeLoadsTheUpgradedExtension(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, agentDir, extension := startupDriftFixture(t)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "reply with exactly: ready")
	if code != 0 || strings.TrimSpace(stdout) != "ready" {
		t.Fatalf("exit=%d stdout=%q\n%s", code, stdout, stderr)
	}
	if lines := strings.Count(stderr, `upgraded extension "ask" for the current SDK`); lines != 1 || strings.Contains(stderr, "Failed to load extension") {
		t.Fatalf("stderr:\n%s", stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(extension, "extension.go")), "sdk.Bool(true)") {
		t.Fatal("the extension was not upgraded")
	}
}

// The upgrade loads the extensions again in the same process: a fork that
// startup already made, and the Session id it carries, are not made a second
// time.
func TestStartupAutoUpgradeKeepsTheForkedSession(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, agentDir, extension := startupDriftFixture(t)
	if _, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "--no-extensions", "-p", "reply with exactly: ready"); code != 0 {
		t.Fatalf("seed session: exit=%d\n%s", code, stderr)
	}
	sessions, err := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*.jsonl"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("seed sessions = %v (%v)", sessions, err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	stdout, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "--fork", sessions[0], "--session-id", id, "-p", "reply with exactly: ready")
	if code != 0 || strings.TrimSpace(stdout) != "ready" || strings.Contains(stderr, "already exists") {
		t.Fatalf("exit=%d stdout=%q\n%s", code, stdout, stderr)
	}
	if !strings.Contains(readFile(t, filepath.Join(extension, "extension.go")), "sdk.Bool(true)") {
		t.Fatal("the extension was not upgraded")
	}
	after, err := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*.jsonl"))
	if err != nil || len(after) != 2 || !slices.ContainsFunc(after, func(path string) bool { return strings.HasSuffix(path, "_"+id+".jsonl") }) {
		t.Fatalf("sessions after the fork = %v, want the seed and one fork with id %s", after, id)
	}
}

// A project settings file cannot turn the upgrade on.
func TestStartupIgnoresAutoUpgradeInAProjectFile(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, agentDir, extension := startupDriftFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pig", "settings.json"), []byte(`{"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "hello")
	if code != 1 || readFile(t, filepath.Join(extension, "extension.go")) != oldExtensionSource {
		t.Fatalf("exit=%d\n%s", code, stderr)
	}
}

// The restarted process never upgrades again: an extension that cannot be fixed fails startup as it always has.
func TestStartupAutoUpgradeLeavesAnExtensionItCannotFix(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	home, agentDir, extension := startupDriftFixture(t)
	source := strings.Replace(oldExtensionSource, "id := ctx.GetSessionID()\n\t\treturn ctx.SendMessage(\"id\", id, true, sdk.SendMessageOptions{TriggerTurn: true})", "return ctx.SetEditorComponent(42)", 1)
	writeStartupFixtureFile(t, filepath.Join(extension, "extension.go"), source)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"extensionsAutoUpgrade":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runDriftStartup(t, binary, home, agentDir, nil, "-p", "hello")
	if code != 1 || !strings.Contains(stderr, `could not upgrade extension "ask"`) || !strings.Contains(stderr, "Context.SetEditorComponent") {
		t.Fatalf("exit=%d\n%s", code, stderr)
	}
	if readFile(t, filepath.Join(extension, "extension.go")) != source {
		t.Fatal("an extension with no mechanical rewrite was edited")
	}
}

// Interactive startup lists the extension and asks; yes upgrades, rebuilds and
// starts the session, and no leaves the extension and ends startup.
func TestInteractiveStartupAsksToUpgrade(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	t.Run("yes", func(t *testing.T) {
		home, agentDir, extension := startupDriftFixture(t)
		session := startSpriteSession(t, binary, filepath.Join(home, "pig"), agentDir)
		session.await("asking", 0, "Run pig extension upgrade ")
		session.await("the question", 0, "now? [y/N] ")
		mark := session.send("y\r")
		session.await("upgrading", mark, "rebuilt: ok")
		session.await("starting the session", mark, "[Extensions]")
		if !strings.Contains(readFile(t, filepath.Join(extension, "extension.go")), "sdk.Bool(true)") {
			t.Fatal("the extension was not upgraded")
		}
	})
	t.Run("no", func(t *testing.T) {
		home, agentDir, extension := startupDriftFixture(t)
		session := startSpriteSession(t, binary, filepath.Join(home, "pig"), agentDir)
		session.await("the question", 0, "now? [y/N] ")
		session.send("\r")
		select {
		case <-session.exited:
		case <-time.After(testbudget.Wait(t)):
			t.Fatalf("pig did not exit after no; last output: %q", session.tail())
		}
		if !strings.Contains(session.tail(), "Failed to load extension") || readFile(t, filepath.Join(extension, "extension.go")) != oldExtensionSource {
			t.Fatalf("output: %q", session.tail())
		}
	})
}
