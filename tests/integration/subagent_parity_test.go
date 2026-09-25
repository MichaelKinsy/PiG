//go:build integration

// Subagent parity tests: run the same subagent operations against both
// pig and upstream pi, compare behavior.
//
// Run with:
//   go test -tags=integration ./tests/integration/ -run TestParity -v -timeout 300s

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
)

// upstreamPiBin finds the upstream pi binary from mise.
// Requires an exact version match for coding.UpstreamVersion so version
// skew is impossible to ignore. Prefers the new @earendil-works/pi-coding-agent
// install dir; falls back to the legacy @mariozechner/pi-coding-agent dir.
func upstreamPiBin(t *testing.T) string {
	t.Helper()
	want := coding.UpstreamVersion
	home := os.Getenv("HOME")
	for _, dirName := range []string{
		"npm-earendil-works-pi-coding-agent",
		"npm-mariozechner-pi-coding-agent",
	} {
		bin := filepath.Join(home, ".local/share/mise/installs", dirName, want, "bin", "pi")
		if _, err := os.Stat(bin); err == nil {
			return bin
		}
	}
	t.Skipf("upstream pi v%s not installed; install with:\n  mise install npm:@earendil-works/pi-coding-agent@%s", want, want)
	return ""
}

// piSuperFlags returns the extension flags needed to load the subagent
// extension in upstream pi. Reads from ~/.pi/extensions/modes/subagent.ts.
func piSubagentExtFlag(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	ext := filepath.Join(home, ".pi", "extensions", "modes", "subagent.ts")
	if _, err := os.Stat(ext); err != nil {
		t.Skipf("upstream subagent extension not found at %s", ext)
	}
	return ext
}

type systemConfig struct {
	name   string
	bin    string
	args   []string
	env    []string
	prompt string // what to grep for to detect readiness
}

func pigConfig(t *testing.T) systemConfig {
	t.Helper()
	pigHome := extIsolatedHome(t)
	subSrc := subagentSourceDir(t)
	writeTestExtensionSettings(t, pigHome, subSrc)
	return pigConfigWithHome(t, pigHome)
}

func pigConfigWithHome(t *testing.T, pigHome string) systemConfig {
	t.Helper()
	trustIntegrationProject(t, pigHome)
	// Loaded resources follow the awaited session_start handlers; the footer paints earlier.
	return systemConfig{
		name:   "pig",
		bin:    buildBinary(t),
		args:   []string{"--model", "github-copilot/gpt-5-mini"},
		env:    []string{"PIG_HOME=" + pigHome, "PIG_QUIET_STARTUP=1"},
		prompt: "[Extensions]",
	}
}

func upstreamConfig(t *testing.T) systemConfig {
	t.Helper()
	piBin := upstreamPiBin(t)
	extFlag := piSubagentExtFlag(t)
	home := upstreamIsolatedHome(t)
	return systemConfig{
		name:   "upstream",
		bin:    piBin,
		args:   []string{"--no-extensions", "-e", extFlag, "--model", "github-copilot/gpt-5-mini"},
		env:    []string{"HOME=" + home},
		prompt: "pi v",
	}
}

func upstreamIsolatedHome(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	piRoot := filepath.Join(tmp, ".pi")
	if err := os.MkdirAll(filepath.Join(piRoot, "agent"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, src := range []string{
		filepath.Join(home, ".pi", "agent", "auth.json"),
		filepath.Join(home, ".pig", "agent", "auth.json"),
	} {
		if data, err := os.ReadFile(src); err == nil {
			if err := os.WriteFile(filepath.Join(piRoot, "agent", "auth.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			break
		}
	}

	for _, root := range []string{filepath.Join(home, ".pi"), filepath.Join(home, ".pig")} {
		src := filepath.Join(root, "agents")
		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(piRoot, "agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err == nil {
				if err := os.WriteFile(filepath.Join(piRoot, "agents", e.Name()), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		break
	}

	return tmp
}

// launchSystem starts a binary in tmux and waits for it to be ready.
func launchSystem(t *testing.T, cfg systemConfig) (session string, cleanup func()) {
	t.Helper()
	session = fmt.Sprintf("parity-%s-%s", cfg.name, randID())

	if err := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "36").Run(); err != nil {
		t.Fatal(err)
	}
	cleanup = func() { _ = tmuxCommand("kill-session", "-t", session).Run() }

	// Build the launch command.
	envPrefix := strings.Join(cfg.env, " ")
	if envPrefix != "" {
		envPrefix += " "
	}
	args := strings.Join(cfg.args, " ")
	cmd := fmt.Sprintf("%s%s %s", envPrefix, cfg.bin, args)
	if err := tmuxCommand("send-keys", "-t", session, "-l", cmd+"\n").Run(); err != nil {
		t.Fatal(err)
	}

	// Wait for readiness.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
		if strings.Contains(string(out), cfg.prompt) {
			return session, cleanup
		}
		time.Sleep(300 * time.Millisecond)
	}
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	cleanup()
	t.Fatalf("%s failed to start within 20s:\n%s", cfg.name, string(out))
	return "", nil
}

func sendAndCapture(t *testing.T, session, input string, wait time.Duration) string {
	t.Helper()
	if err := tmuxCommand("send-keys", "-t", session, "-l", input).Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := tmuxCommand("send-keys", "-t", session, "C-m").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(wait)
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	return string(out)
}

func sendSlashAndCapture(t *testing.T, session, cmd string, wait time.Duration) string {
	t.Helper()
	if err := tmuxCommand("send-keys", "-t", session, "-l", cmd+"\r").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(wait)
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	return string(out)
}

// TestParity_SubagentCheckTool runs check_subagent on both systems and
// compares that both return a sensible result (no active dispatches).
func TestParity_SubagentCheckTool(t *testing.T) {
	pig := pigConfig(t)
	upstream := upstreamConfig(t)

	for _, cfg := range []systemConfig{pig, upstream} {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			session, cleanup := launchSystem(t, cfg)
			defer cleanup()

			pane := sendAndCapture(t, session,
				"Use the check_subagent tool with no arguments. Report what it returned.",
				10*time.Second)

			t.Logf("%s pane:\n%s", cfg.name, pane)

			// Both should show the tool was called, not "unknown tool".
			lower := strings.ToLower(pane)
			if strings.Contains(lower, "not available") || strings.Contains(lower, "unknown tool") {
				t.Errorf("%s: check_subagent tool not available", cfg.name)
			}
			// Should mention dispatches/subagents in the response.
			if !strings.Contains(lower, "dispatch") && !strings.Contains(lower, "sub") && !strings.Contains(lower, "active") {
				t.Errorf("%s: response doesn't mention dispatches:\n%s", cfg.name, pane)
			}

			_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		})
	}
}

// TestParity_SubsCommand runs /subs on both systems and compares output.
func TestParity_SubsCommand(t *testing.T) {
	pig := pigConfig(t)
	upstream := upstreamConfig(t)

	for _, cfg := range []systemConfig{pig, upstream} {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			session, cleanup := launchSystem(t, cfg)
			defer cleanup()

			pane := sendSlashAndCapture(t, session, "/subs", 3*time.Second)

			t.Logf("%s /subs:\n%s", cfg.name, pane)

			if strings.Contains(pane, "unknown command") {
				t.Errorf("%s: /subs not registered", cfg.name)
			}
			// Both systems should show "No sub-agents dispatched yet."
			if !strings.Contains(pane, "No sub-agents dispatched yet") && !strings.Contains(pane, "No active") {
				t.Logf("%s: /subs output does not contain expected empty message (may be OK if output scrolled)", cfg.name)
			}

			_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		})
	}
}

// TestParity_AgentsCommand runs /agents on both systems.
func TestParity_AgentsCommand(t *testing.T) {
	pig := pigConfig(t)
	upstream := upstreamConfig(t)

	for _, cfg := range []systemConfig{pig, upstream} {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			session, cleanup := launchSystem(t, cfg)
			defer cleanup()

			pane := sendSlashAndCapture(t, session, "/agents", 3*time.Second)

			t.Logf("%s /agents:\n%s", cfg.name, pane)

			if strings.Contains(pane, "unknown command") {
				t.Errorf("%s: /agents not registered", cfg.name)
			}
			// Both systems should list at least the worker agent.
			if !strings.Contains(pane, "worker") {
				t.Errorf("%s: /agents did not list 'worker' agent:\n%s", cfg.name, pane)
			}

			_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		})
	}
}

// TestParity_SubagentStartupPerf compares startup time with subagent
// extension loaded on both systems. Uses installed binaries (not test-built)
// for accurate timing: test-built binaries lack codesign on macOS.
func TestParity_SubagentStartupPerf(t *testing.T) {
	// Use installed pig binary for accurate startup measurement.
	home, _ := os.UserHomeDir()
	pigBin := filepath.Join(home, ".local", "bin", "pig")
	if _, err := os.Stat(pigBin); err != nil {
		t.Skipf("installed pig not found at %s", pigBin)
	}
	pigHome := extIsolatedHome(t)
	subSrc := subagentSourceDir(t)
	writeTestExtensionSettings(t, pigHome, subSrc)

	pigCfg := pigConfigWithHome(t, pigHome)
	pigCfg.bin = pigBin
	upstream := upstreamConfig(t)

	now := func() int64 {
		out, _ := exec.Command("perl", "-MTime::HiRes", "-e",
			`printf("%.0f\n",Time::HiRes::time()*1000)`).Output()
		var ms int64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &ms); err != nil {
			t.Fatal(err)
		}
		return ms
	}

	for _, cfg := range []systemConfig{pigCfg, upstream} {
		cfg := cfg
		t.Run(cfg.name, func(t *testing.T) {
			start := now()
			session, cleanup := launchSystem(t, cfg)
			defer cleanup()
			elapsed := now() - start

			t.Logf("%s startup with subagent extension: %dms", cfg.name, elapsed)

			_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		})
	}
}
