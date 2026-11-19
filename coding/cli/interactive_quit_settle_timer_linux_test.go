//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// D649, the interactive cell of claim 4. The user quits with ctrl+d while an agent_settled handler waits on a timer and another extension's session_shutdown handler never
// settles. Pi 1.5.2 (tmux): its loop stays alive for the handler's timer, so the quit lasts about the timer (2397 to 2415 ms for 3 s). A Node handler holds pig the same way.
// A Go SDK handler on the same timer does not: pig exits at once (92 to 95 ms).
func TestInteractiveQuitWithATimerSettleHandlerAndAHangingShutdownComparedWithPi(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not on PATH:", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piCLI := filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	fixture := filepath.Join(root, "coding", "cli", "testdata", "rpc-shutdown.mjs")
	faux := filepath.Join(root, "test", "parity", "testdata", "test-faux-provider.ts")
	pigBinary := buildPigBinaryForSignalTest(t)
	goExt := buildGoSDKFixture(t, "rpc-shutdown-go")
	common := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", "test-faux/faux-1"}
	legs := []struct {
		name  string
		holds bool
		argv  func() []string
		env   func(root, agent string) []string
		extra []string
	}{
		{"pi", true, func() []string {
			return append([]string{node, piCLI}, append(slices.Clone(common), "-e", faux, "-e", fixture)...)
		},
			func(root, agent string) []string { return []string{"PI_CODING_AGENT_DIR=" + agent, "PI_OFFLINE=1"} }, []string{"RPC_SHUTDOWN_SETTLED_WAIT=4000"}},
		{"pig-node", true, func() []string { return append([]string{pigBinary}, append(slices.Clone(common), "-e", fixture)...) },
			func(root, agent string) []string {
				return []string{"PIG_CODING_AGENT_DIR=" + agent, "PIG_HOME=" + root, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}
			}, []string{"RPC_SHUTDOWN_SETTLED_WAIT=4000"}},
		{"pig-go", false, func() []string {
			return append([]string{pigBinary}, append(slices.Clone(common), "-e", fixture, "-e", goExt)...)
		},
			func(root, agent string) []string {
				return []string{"PIG_CODING_AGENT_DIR=" + agent, "PIG_HOME=" + root, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic"}
			}, []string{"RPC_SHUTDOWN_GO_SETTLED_TIMER_MS=4000"}},
	}
	for _, leg := range legs {
		t.Run(leg.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			agent, project, report := filepath.Join(dir, "agent"), filepath.Join(dir, "project"), filepath.Join(dir, "report.txt")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			seedFirstRunDone(t, agent)
			env := append([]string{"HOME=" + dir, "RPC_SHUTDOWN_REPORT=" + report, "RPC_SHUTDOWN_BLOCK_QUIT=1"}, leg.env(dir, agent)...)
			env = append(env, leg.extra...)
			p := startQuitPane(t, project, filepath.Join(dir, "stderr.log"), env, leg.argv()...)
			p.await("the editor", func() bool { return strings.Contains(p.screen(), "faux-1") })
			p.text("hello")
			p.keys("Enter")
			p.await("the settle tail", func() bool { return slices.Contains(readRPCShutdownReport(t, report), "agent_settled") })
			pressed := time.Now()
			p.keys("C-d")
			p.await("the exit", func() bool { _, exited := p.status(); return exited })
			elapsed := time.Since(pressed)
			const timer = 4000 * time.Millisecond
			if leg.holds && elapsed < timer*6/10 {
				t.Fatalf("the quit ended %s after the key, want it held by the %s timer\n%s", elapsed, timer, p.screen())
			}
			if !leg.holds && elapsed >= timer*4/10 {
				t.Fatalf("the quit ended %s after the key, want it not held by the %s timer\n%s", elapsed, timer, p.screen())
			}
		})
	}
}
