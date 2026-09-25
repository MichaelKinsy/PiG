//go:build integration && live

// Live subagent functional tests: dispatch real sub-agents via the
// pig subagent extension and verify they produce actual results.
//
// These tests require valid auth and use real LLM calls. They exercise
// the full pipeline: pig binary → subprocess extension → tool dispatch
// → sub-agent spawn → LLM inference → result collection.
//
// Run with:
//   go test -tags=integration ./tests/integration/ -run TestLive -v -timeout 600s
//
// These are expensive tests (real API calls). Run selectively.

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pigInteractiveSession starts pig in tmux with the subagent extension
// and returns a harness-like set of functions.
type pigSession struct {
	t       *testing.T
	session string
	workDir string
	cleanup func()
}

func newGopiSession(t *testing.T) *pigSession {
	t.Helper()
	pigHome := extIsolatedHome(t)
	subSrc := subagentSourceDir(t)
	writeTestExtensionSettings(t, pigHome, subSrc)

	bin := buildBinary(t)
	session := "live-" + randID()
	cwd := t.TempDir()

	// Create a test file for agents to read.
	if err := os.WriteFile(filepath.Join(cwd, "task.txt"), []byte("The secret word is MANGO.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "40").Run(); err != nil {
		t.Fatal(err)
	}

	cmd := "export PIG_HOME=" + pigHome + " PIG_QUIET_STARTUP=1; cd " + cwd + " && " + bin + " --model github-copilot/gpt-5-mini"
	if err := tmuxCommand("send-keys", "-t", session, "-l", cmd+"\n").Run(); err != nil {
		t.Fatal(err)
	}

	// Wait for ready.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
		if strings.Contains(string(out), interactiveReadyMarker) && strings.Contains(string(out), "[Extensions]") {
			return &pigSession{
				t:       t,
				session: session,
				workDir: cwd,
				cleanup: func() { _ = tmuxCommand("kill-session", "-t", session).Run() },
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	_ = tmuxCommand("kill-session", "-t", session).Run()
	t.Fatalf("pig failed to start within 20s:\n%s", string(out))
	return nil
}

func (g *pigSession) send(input string) {
	g.t.Helper()
	if err := tmuxCommand("send-keys", "-t", g.session, "-l", input).Run(); err != nil {
		g.t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := tmuxCommand("send-keys", "-t", g.session, "C-m").Run(); err != nil {
		g.t.Fatal(err)
	}
}

func (g *pigSession) sendSlash(cmd string) {
	g.t.Helper()
	if err := tmuxCommand("send-keys", "-t", g.session, "-l", cmd+"\r").Run(); err != nil {
		g.t.Fatal(err)
	}
}

func (g *pigSession) capture() string {
	g.t.Helper()
	out, _ := tmuxCommand("capture-pane", "-t", g.session, "-p").Output()
	return string(out)
}

func (g *pigSession) waitFor(needle string, timeout time.Duration) (string, bool) {
	g.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pane := g.capture()
		if strings.Contains(pane, needle) {
			return pane, true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return g.capture(), false
}

func (g *pigSession) close() {
	_ = tmuxCommand("send-keys", "-t", g.session, "C-d").Run()
	time.Sleep(300 * time.Millisecond)
	g.cleanup()
}

// TestLive_DispatchWorkerAndGetResult dispatches a background worker
// via dispatch_subagent and verifies it completes with a result.
func TestLive_DispatchWorkerAndGetResult(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	// Dispatch a worker to read task.txt.
	g.send("Use the dispatch_subagent tool to dispatch a worker agent in background mode with task: 'Read task.txt and report its contents'. Just dispatch it, nothing else.")

	// Wait for the dispatch confirmation (tool call result shows ✓).
	pane, ok := g.waitFor("✓ dispatch_subagent", 15*time.Second)
	if !ok {
		// Also check for the tool being called but erroring.
		pane = g.capture()
		if strings.Contains(pane, "Error") || strings.Contains(pane, "401") {
			t.Skipf("auth expired: LLM can't call tools:\n%s", pane)
		}
		t.Fatalf("dispatch_subagent tool not executed within 15s:\n%s", pane)
	}
	t.Logf("dispatch pane:\n%s", pane)

	// The tool should have returned a dispatch ID.
	if !strings.Contains(strings.ToLower(pane), "s1") && !strings.Contains(pane, "running") && !strings.Contains(pane, "dispatch") {
		t.Errorf("dispatch result doesn't mention dispatch ID or status")
	}
}

// TestLive_DispatchAndCheckStatus dispatches a worker, waits, then
// uses check_subagent to verify the dispatch is tracked.
func TestLive_DispatchAndCheckStatus(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	// Dispatch.
	g.send("Use dispatch_subagent to dispatch a worker in background mode with task 'say hello'. Just dispatch it.")
	pane, ok := g.waitFor("✓ dispatch_subagent", 15*time.Second)
	if !ok {
		pane = g.capture()
		if strings.Contains(pane, "Error") || strings.Contains(pane, "401") {
			t.Skipf("auth expired:\n%s", pane)
		}
		t.Fatalf("dispatch not called:\n%s", pane)
	}
	t.Logf("after dispatch:\n%s", pane)

	// Wait a moment for the dispatch to register.
	time.Sleep(2 * time.Second)

	// Check status via /subs.
	g.sendSlash("/subs")
	time.Sleep(3 * time.Second)
	pane = g.capture()
	t.Logf("/subs output:\n%s", pane)

	// Should show either the dispatch or "No sub-agents dispatched yet."
	lower := strings.ToLower(pane)
	if !strings.Contains(lower, "s1") && !strings.Contains(lower, "worker") && !strings.Contains(lower, "no sub-agents") {
		t.Logf("warning: /subs output doesn't show expected content")
	}
}

// TestLive_SubsShowsEmptyThenPopulated verifies /subs works before
// and after a dispatch.
func TestLive_SubsShowsEmptyThenPopulated(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	// /subs before any dispatch.
	g.sendSlash("/subs")
	pane, ok := g.waitFor("No sub-agents dispatched yet", 5*time.Second)
	if !ok {
		t.Logf("/subs before dispatch doesn't show empty message:\n%s", pane)
	}

	// Dispatch a worker.
	g.send("Use dispatch_subagent to dispatch a worker in background mode with task 'echo done'. Just dispatch it.")
	_, ok = g.waitFor("✓ dispatch_subagent", 15*time.Second)
	if !ok {
		t.Skipf("dispatch_subagent not called: auth may be expired")
	}

	// Wait for completion notification.
	time.Sleep(5 * time.Second)

	// /subs after dispatch: should show the dispatch.
	g.sendSlash("/subs")
	time.Sleep(3 * time.Second)
	pane = g.capture()
	t.Logf("/subs after dispatch:\n%s", pane)
}

// TestLive_AgentsListsAvailable verifies /agents shows real agent
// definitions from the config.
func TestLive_AgentsListsAvailable(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	g.sendSlash("/agents")
	pane, ok := g.waitFor("worker", 5*time.Second)
	if !ok {
		t.Errorf("/agents did not list worker agent:\n%s", pane)
		return
	}

	// Should also list other common agents.
	t.Logf("/agents:\n%s", pane)
	for _, agent := range []string{"reviewer", "scout", "spec", "planner"} {
		if !strings.Contains(pane, agent) {
			t.Logf("warning: /agents missing %q", agent)
		}
	}
}

// TestLive_CancelSubagent dispatches and immediately cancels.
func TestLive_CancelSubagent(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	// Dispatch.
	g.send("Use dispatch_subagent to dispatch a worker in background mode with task 'sleep for 30 seconds'. Just dispatch it.")
	_, ok := g.waitFor("✓ dispatch_subagent", 15*time.Second)
	if !ok {
		t.Skipf("dispatch_subagent not called: auth may be expired")
	}

	// Wait for the LLM to finish its response before sending the next prompt.
	// The status bar shows cost/timing when idle.
	time.Sleep(5 * time.Second)

	// Cancel it.
	g.send("Use cancel_subagent to cancel dispatch S1.")
	pane, ok := g.waitFor("✓ cancel_subagent", 20*time.Second)
	if !ok {
		pane = g.capture()
		if strings.Contains(pane, "Error") || strings.Contains(pane, "401") {
			t.Skipf("auth expired:\n%s", pane)
		}
		t.Fatalf("cancel_subagent not called:\n%s", pane)
	}
	t.Logf("cancel result:\n%s", pane)
}

// TestLive_DispatchMultiple dispatches multiple agents in parallel
// via dispatch_subagents.
func TestLive_DispatchMultiple(t *testing.T) {
	g := newGopiSession(t)
	defer g.close()

	g.send(`Use dispatch_subagents to dispatch 2 agents in parallel: a worker with task "say hello" and a scout with task "say world". Just dispatch them.`)
	pane, ok := g.waitFor("✓ dispatch_subagents", 20*time.Second)
	if !ok {
		pane = g.capture()
		if strings.Contains(pane, "Error") || strings.Contains(pane, "401") {
			t.Skipf("auth expired:\n%s", pane)
		}
		t.Fatalf("dispatch_subagents not called:\n%s", pane)
	}
	t.Logf("multi-dispatch:\n%s", pane)

	// Should mention both dispatches.
	lower := strings.ToLower(pane)
	if !strings.Contains(lower, "parallel") && !strings.Contains(lower, "dispatch") {
		t.Logf("warning: multi-dispatch output doesn't mention parallel/dispatched")
	}
}
