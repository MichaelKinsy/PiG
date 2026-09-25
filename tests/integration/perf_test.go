//go:build integration

// Performance probes that are not yet migrated into parity/scenarios/*.toml.
//
// The authoritative startup/perf gate now lives in parity scenario
// 00-startup-banner.toml. Keep only probes here that do NOT have a scenario
// equivalent yet.
//
// Run with:
//   go test -tags=integration ./tests/integration/ -run TestPerf -v -timeout 120s

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// launchDirect starts a binary directly via tmux (no shell), waits for
// the prompt pattern, and returns elapsed milliseconds.
func launchDirect(t *testing.T, name, bin string, args []string, env []string, prompt string) (session string, elapsedMs int64, cleanup func()) {
	t.Helper()
	session = fmt.Sprintf("perf-%s-%s", name, randID())

	// Build the command string with env vars prepended.
	parts := append(append([]string(nil), env...), bin)
	parts = append(parts, args...)
	cmdStr := strings.Join(parts, " ") + "; sleep 999"

	t0 := time.Now()
	if err := tmuxCommand("new-session", "-d", "-s", session, "-x", "120", "-y", "35", cmdStr).Run(); err != nil {
		t.Fatal(err)
	}
	cleanup = func() { _ = tmuxCommand("kill-session", "-t", session).Run() }

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
		if strings.Contains(string(out), prompt) {
			return session, time.Since(t0).Milliseconds(), cleanup
		}
		time.Sleep(50 * time.Millisecond)
	}
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	t.Logf("%s did not show prompt within 15s:\n%s", name, string(out))
	return session, time.Since(t0).Milliseconds(), cleanup
}

// installedGopiBin returns the path to the installed pig binary.
func installedGopiBin(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	bin := filepath.Join(home, ".local", "bin", "pig")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("installed pig not found at %s", bin)
	}
	return bin
}

// TestPerf_DirectStartupNoExtensions measures startup without extensions
// to isolate extension loading overhead.
func TestPerf_DirectStartupNoExtensions(t *testing.T) {
	pigBin := installedGopiBin(t)
	piBin := upstreamPiBin(t)

	pigHome := extIsolatedHome(t)
	// No extension settings are written; the home is clean.

	type run struct {
		name   string
		bin    string
		args   []string
		env    []string
		prompt string
	}

	systems := []run{
		{
			name:   "pig",
			bin:    pigBin,
			args:   []string{"--model", "github-copilot/gpt-5-mini", "--no-extensions"},
			env:    []string{"PIG_HOME=" + pigHome, "PIG_QUIET_STARTUP=1"},
			prompt: interactiveReadyMarker,
		},
		{
			name:   "pi",
			bin:    piBin,
			args:   []string{"--no-extensions", "--model", "github-copilot/gpt-5-mini"},
			env:    []string{},
			prompt: "pi v",
		},
	}

	const runs = 3
	for _, sys := range systems {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			var times []int64
			for i := range runs {
				session, ms, cleanup := launchDirect(t, sys.name, sys.bin, sys.args, sys.env, sys.prompt)
				times = append(times, ms)
				_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
				cleanup()
				t.Logf("  run %d: %dms", i+1, ms)
			}

			sum := int64(0)
			for _, ms := range times {
				sum += ms
			}
			t.Logf("%s no-ext startup mean: %dms", sys.name, sum/int64(len(times)))
		})
	}
}

// TestPerf_PrintMode measures headless --print mode latency (includes
// LLM round-trip). This is where pig's Go advantage shows most clearly.
func TestPerf_PrintMode(t *testing.T) {
	pigBin := installedGopiBin(t)
	piBin := upstreamPiBin(t)
	piExt := piSubagentExtFlag(t)

	pigHome := extIsolatedHome(t)
	subSrc := subagentSourceDir(t)
	writeTestExtensionSettings(t, pigHome, subSrc)

	type run struct {
		name string
		bin  string
		args []string
		env  []string
	}

	systems := []run{
		{
			name: "pig",
			bin:  pigBin,
			args: []string{"--print", "say the word PINEAPPLE and nothing else", "--model", "github-copilot/gpt-5-mini"},
			env:  []string{"PIG_HOME=" + pigHome},
		},
		{
			name: "pi",
			bin:  piBin,
			args: []string{"--print", "say the word PINEAPPLE and nothing else", "--no-extensions", "-e", piExt, "--model", "github-copilot/gpt-5-mini"},
			env:  []string{},
		},
	}

	const runs = 3
	for _, sys := range systems {
		sys := sys
		t.Run(sys.name, func(t *testing.T) {
			var times []int64
			for i := range runs {
				cmd := exec.Command(sys.bin, sys.args...)
				cmd.Env = append(os.Environ(), sys.env...)
				t0 := time.Now()
				out, err := cmd.CombinedOutput()
				ms := time.Since(t0).Milliseconds()
				times = append(times, ms)
				output := strings.TrimSpace(string(out))
				if len(output) > 100 {
					output = output[:100] + "..."
				}
				if err != nil {
					t.Logf("  run %d: %dms (error: %v) output=%q", i+1, ms, err, output)
				} else {
					t.Logf("  run %d: %dms output=%q", i+1, ms, output)
				}
			}

			sum := int64(0)
			for _, ms := range times {
				sum += ms
			}
			t.Logf("%s print-mode mean: %dms", sys.name, sum/int64(len(times)))
		})
	}
}
