//go:build integration

// Extension behavioral tests: prove real extensions load, register, and
// function through the actual pig binary started in tmux.
//
// These tests exist because synthetic fixtures (testdata/fixture-ext/) mask
// real bugs. The schema→parameters wire format mismatch (2026-05-08) was
// only caught in production because no test exercised a real SDK-based
// extension through the binary. These tests close that gap.
//
// Run with:
//
//	go test -tags=integration ./tests/integration/ -run TestExt -v
//
// Live tests (TestExt_Live_*) require auth and skip if not available:
//
//	go test -tags=integration ./tests/integration/ -run TestExt_Live -v
//
// Prerequisites:
//   - external subagent extension source available via PIG_SUBAGENT_EXTENSION_DIR

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// extIsolatedHome creates a temp PIG_HOME with auth copied from the real
// ~/.pig/agent/auth.json (or ~/.pi/agent/auth.json as fallback).
// Skips the test if no auth is available.
func extIsolatedHome(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()

	// Try both ~/.pig and ~/.pi. Prefer whichever has a non-expired token.
	// This handles the common case where pig's ghu_ token expired because
	// pi refreshed its Copilot token more recently (they share the same
	// GitHub OAuth app but maintain separate auth files).
	type authCandidate struct {
		data    []byte
		expires int64  // ms since epoch (Copilot API token expiry)
		refresh string // ghu_ token (long-lived, used to refresh Copilot token)
	}
	var candidates []authCandidate
	for _, root := range []string{
		filepath.Join(home, ".pig"),
		filepath.Join(home, ".pi"),
	} {
		src := filepath.Join(root, "agent", "auth.json")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		var creds map[string]any
		if json.Unmarshal(data, &creds) != nil || len(creds) == 0 {
			continue
		}
		var expires int64
		var refresh string
		if gc, ok := creds["github-copilot"].(map[string]any); ok {
			if exp, ok := gc["expires"].(float64); ok {
				expires = int64(exp)
			}
			if ref, ok := gc["refresh"].(string); ok {
				refresh = ref
			}
		}
		candidates = append(candidates, authCandidate{data: data, expires: expires, refresh: refresh})
	}
	if len(candidates) == 0 {
		t.Skip("no auth.json found in ~/.pig or ~/.pi: skipping live test")
	}

	// Pick the candidate with the latest expiry (most recently refreshed).
	// Note: `expires` tracks the short-lived Copilot API token (~30 min),
	// NOT the long-lived ghu_ GitHub access token. Even when `expires` is
	// in the past, pig will auto-refresh using the ghu_ token stored in
	// the `refresh` field. We only skip if there's no refresh token at all.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.expires > best.expires {
			best = c
		}
	}
	if best.refresh == "" {
		t.Skip("no refresh token (ghu_) in auth: run 'pig login'")
	}

	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), best.data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Copy agent definitions so /agents and dispatch work.
	for _, root := range []string{
		filepath.Join(home, ".pig"),
		filepath.Join(home, ".pi"),
	} {
		src := filepath.Join(root, "agents")
		entries, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(tmp, "agents")
		if err := os.MkdirAll(dst, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err == nil {
				if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		break // first dir with agents wins
	}

	return tmp
}

// subagentSourceDir returns the absolute path to an external subagent
// extension source directory. Skips the test if not found.
func subagentSourceDir(t *testing.T) string {
	t.Helper()
	var candidates []string
	if env := os.Getenv("PIG_SUBAGENT_EXTENSION_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	if repoRoot, err := findRepoRoot(); err == nil {
		candidates = append(candidates, filepath.Join(repoRoot, "..", "pig-extensions", "native", "subagent"))
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	t.Skipf("external subagent extension not found; set PIG_SUBAGENT_EXTENSION_DIR (checked %v)", candidates)
	return ""
}

// writeTestExtensionSettings selects the external extension through the normal
// settings-backed exact-path source.
func writeTestExtensionSettings(t *testing.T, pigHome string, extensionSources ...string) {
	t.Helper()
	agentDir := filepath.Join(pigHome, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"extensions": extensionSources})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExt_StartupWithSubagent proves pig starts cleanly with the real
// subagent extension loaded. This is the test that would have caught:
// - The schema→parameters wire format mismatch (extension register timeout)
// - The nil-deref panic with --no-extensions (typed-nil interface trap)
func TestExt_StartupWithSubagent(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, subSrc)

	h := newHarness(t)
	h.start("PIG_HOME=" + pigHome)

	// Loaded resources appear after session_start; the footer alone can paint while the extension is still loading.
	pane := h.expectContains(startupReadyWait, interactiveReadyMarker, "[Extensions]")
	if strings.Contains(pane, "panic") {
		t.Fatalf("startup panic:\n%s", pane)
	}
	if strings.Contains(pane, "warning: extension") {
		t.Fatalf("extension warning on startup:\n%s", pane)
	}
}

// TestExt_SubagentCommandsRegistered proves the subagent extension's
// slash commands (/subs, /agents) are registered and executable.
func TestExt_SubagentCommandsRegistered(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, subSrc)

	h := newHarness(t)
	h.start("PIG_HOME=" + pigHome)
	h.expectContains(startupReadyWait, interactiveReadyMarker, "[Extensions]")

	// /subs should execute without "unknown command" error.
	h.send("/subs\r")
	time.Sleep(2 * time.Second)
	pane := h.capture()
	if strings.Contains(pane, "unknown command") {
		t.Fatalf("/subs not registered: extension commands not loaded:\n%s", pane)
	}

	// /agents should also work.
	h.send("/agents\r")
	time.Sleep(2 * time.Second)
	pane = h.capture()
	if strings.Contains(pane, "unknown command") {
		t.Fatalf("/agents not registered:\n%s", pane)
	}
}

// TestExt_NoExtensionsFlag proves --no-extensions starts cleanly when settings contain an extension path. Catches the typed-nil interface panic.
func TestExt_NoExtensionsFlag(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, subSrc)

	h := newHarness(t)
	// Override the binary command to include --no-extensions.
	// We can't use h.start() because it doesn't pass extra flags.
	bin := buildBinary(t)
	cmd := fmt.Sprintf("PIG_HOME=%s %s --no-extensions 2>/dev/null", pigHome, bin)
	h.send("clear && " + cmd + "\r")

	pane := h.expectContains(startupReadyWait, interactiveReadyMarker)
	if strings.Contains(pane, "panic") {
		t.Fatalf("--no-extensions panic:\n%s", pane)
	}
}

// TestExt_ReloadPreservesExtensions proves /reload doesn't break
// extension commands.
func TestExt_ReloadPreservesExtensions(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, subSrc)

	h := newHarness(t)
	h.start("PIG_HOME=" + pigHome)
	h.expectContains(startupReadyWait, interactiveReadyMarker, "[Extensions]")

	// /subs works before reload.
	h.send("/subs\r")
	time.Sleep(2 * time.Second)

	// Reload.
	h.send("/reload\r")
	time.Sleep(3 * time.Second)
	pane := h.capture()
	if strings.Contains(pane, "panic") || strings.Contains(pane, "extension reload:") {
		t.Fatalf("reload error:\n%s", pane)
	}

	// /subs still works after reload.
	h.send("/subs\r")
	time.Sleep(2 * time.Second)
	pane = h.capture()
	if strings.Contains(pane, "unknown command") {
		t.Fatalf("/subs broken after reload:\n%s", pane)
	}
}

// TestExt_SessionStartPrecedesLoadedResources proves why extension startup waits include the resource section instead of accepting the first footer paint.
func TestExt_SessionStartPrecedesLoadedResources(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	ext := filepath.Join(dir, "startup-probe.mjs")
	source := `import { existsSync, writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_start", async () => {
    writeFileSync(process.env.PIG_STARTUP_STARTED, "started");
    while (!existsSync(process.env.PIG_STARTUP_RELEASE)) {
      await new Promise(resolve => setTimeout(resolve, 10));
    }
  });
}
`
	if err := os.WriteFile(ext, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	t.Cleanup(func() {
		if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Errorf("release startup probe: %v", err)
		}
	})
	h.startArgsAt([]string{"--model", "test-faux/faux-1", "-e", ext}, dir,
		"PIG_TEST_FAUX=1", "PIG_STARTUP_STARTED="+started, "PIG_STARTUP_RELEASE="+release)
	if !waitFor(startupReadyWait, func() bool {
		_, err := os.Stat(started)
		return err == nil
	}) {
		t.Fatal("extension never entered session_start")
	}
	pane := h.expectContains(startupReadyWait, interactiveReadyMarker)
	if strings.Contains(pane, "[Extensions]") {
		t.Fatalf("loaded resources appeared before session_start completed:\n%s", pane)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.expectContains(startupReadyWait, interactiveReadyMarker, "[Extensions]", "startup-probe")
	h.send("/hotkeys\r")
	h.expectContains(3*time.Second, "Ctrl+R            : Rename session")
}

// TestExt_StartupWithoutConfig proves pig starts with no configured or discovered extensions.
func TestExt_StartupWithoutConfig(t *testing.T) {
	pigHome := t.TempDir()

	h := newHarness(t)
	h.startArgsAt([]string{"--model", "test-faux/faux-1"}, t.TempDir(), "PIG_HOME="+pigHome, "PIG_TEST_FAUX=1")
	pane := h.expectContains(startupReadyWait, interactiveReadyMarker, "faux-1")
	if strings.Contains(pane, "[Extensions]") {
		t.Fatalf("empty fixture discovered ambient extensions:\n%s", pane)
	}
	if strings.Contains(pane, "panic") {
		t.Fatalf("startup without config panic:\n%s", pane)
	}
}

// TestExt_ConventionDirectoryDiscovery proves pig discovers extensions
// from ~/.pig/agent/extensions/<name>/ convention directories.
func TestExt_ConventionDirectoryDiscovery(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()

	// Create convention directory: symlink the subagent source.
	convDir := filepath.Join(pigHome, "agent", "extensions", "subagent")
	if err := os.MkdirAll(filepath.Dir(convDir), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, subSrc, convDir)

	h := newHarness(t)
	h.start("PIG_HOME=" + pigHome)
	pane := h.expectContains(startupReadyWait, interactiveReadyMarker, "[Extensions]")
	if strings.Contains(pane, "panic") {
		t.Fatalf("convention dir panic:\n%s", pane)
	}

	// /subs should work (proves convention discovery found the extension).
	h.send("/subs\r")
	time.Sleep(2 * time.Second)
	pane = h.capture()
	if strings.Contains(pane, "unknown command") {
		t.Fatalf("/subs not available via convention directory:\n%s", pane)
	}
}

// TestExt_CachedBinaryUsedOnSecondStartup proves the content-hash
// cache works: first startup builds, second startup is faster.
func TestExt_CachedBinaryUsedOnSecondStartup(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := t.TempDir()
	writeTestExtensionSettings(t, pigHome, subSrc)

	// Cache dir now respects PIG_HOME.
	cacheDir := filepath.Join(pigHome, "cache", "ext")

	bin := buildBinary(t)

	// First startup: cold cache: must build.
	start1 := time.Now()
	s1 := "ext-cache-1-" + randID()
	if err := tmuxCommand("new-session", "-d", "-s", s1, "-x", "100", "-y", "30").Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", s1).Run() })
	if err := tmuxCommand("send-keys", "-t", s1, "-l",
		fmt.Sprintf("PIG_HOME=%s %s\r", pigHome, bin)).Run(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", s1, "-p").Output()
		if strings.Contains(string(out), interactiveReadyMarker) && strings.Contains(string(out), "[Extensions]") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	cold := time.Since(start1)
	if err := tmuxCommand("send-keys", "-t", s1, "C-d").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	// Verify cache was populated.
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) == 0 {
		t.Fatal("cache dir empty after first startup: builder didn't cache")
	}

	// Second startup: warm cache: should be faster.
	start2 := time.Now()
	s2 := "ext-cache-2-" + randID()
	if err := tmuxCommand("new-session", "-d", "-s", s2, "-x", "100", "-y", "30").Run(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmuxCommand("kill-session", "-t", s2).Run() })
	if err := tmuxCommand("send-keys", "-t", s2, "-l",
		fmt.Sprintf("PIG_HOME=%s %s\r", pigHome, bin)).Run(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", s2, "-p").Output()
		if strings.Contains(string(out), interactiveReadyMarker) && strings.Contains(string(out), "[Extensions]") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	warm := time.Since(start2)
	_ = tmuxCommand("send-keys", "-t", s2, "C-d").Run()

	t.Logf("cold startup: %s, warm startup: %s", cold, warm)
	// Warm should be noticeably faster (no go build).
	if warm > cold {
		t.Errorf("warm startup (%s) slower than cold (%s): cache is not working", warm, cold)
	}
}

// ── Live extension tool tests ────────────────────────────────────────────────
//
// These require auth (github-copilot) and use a cheap model to prove
// extension tools actually execute end-to-end through the LLM.

// TestExt_Live_SubagentCheckReturnsResult asks the LLM to call
// check_subagent (with no args), which should return "no active dispatches"
// or similar. This proves the full round-trip:
// LLM → Host → subprocess → SDK handler → result → Host → LLM → output
func TestExt_Live_SubagentCheckReturnsResult(t *testing.T) {
	subSrc := subagentSourceDir(t)
	pigHome := extIsolatedHome(t) // copies auth from ~/.pig
	writeTestExtensionSettings(t, pigHome, subSrc)

	bin := buildBinary(t)
	cwd := t.TempDir()

	// Use --print mode for deterministic output capture.
	cmd := exec.Command(bin,
		"--cwd", cwd,
		"--model", "github-copilot/gpt-5-mini",
		"--print", "Use the check_subagent tool with no arguments to check if there are any active sub-agents. Report what the tool returned.")
	cmd.Env = append(os.Environ(),
		"PIG_HOME="+pigHome,
		"PIG_QUIET_STARTUP=1",
	)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		// Skip on auth failures: these tests are about extension behavior,
		// not credential management.
		if strings.Contains(output, "401") || strings.Contains(output, "Bad credentials") || strings.Contains(output, "refresh") {
			t.Skipf("auth expired: run 'pig login github-copilot' to fix:\n%s", output)
		}
		t.Fatalf("pig --print failed: %v\noutput:\n%s", err, output)
	}

	// The check_subagent handler returns dispatch status.
	// With no dispatches, it should say something about no active dispatches.
	// We're testing that the tool was called and returned: not the exact wording.
	if len(output) == 0 {
		t.Fatal("empty output: tool may not have been called")
	}
	t.Logf("check_subagent output:\n%s", output)

	// Verify the tool was actually invoked (not just described).
	// The --print output includes tool call results. Look for evidence
	// the tool ran (result text or tool execution marker).
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "dispatch") && !strings.Contains(lower, "sub-agent") && !strings.Contains(lower, "subagent") && !strings.Contains(lower, "active") {
		t.Errorf("output doesn't mention dispatches/subagents: tool may not have executed:\n%s", output)
	}
}

// TestExt_Live_SubagentDispatchAndCheck dispatches a real background
// sub-agent (worker) with a trivial task, waits, then checks status.
// This is the full end-to-end proof: tool dispatch → subprocess spawn →
// output file → status check.
func TestExt_Live_SubagentDispatchAndCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long live test in short mode")
	}

	subSrc := subagentSourceDir(t)
	pigHome := extIsolatedHome(t)
	writeTestExtensionSettings(t, pigHome, subSrc)

	bin := buildBinary(t)
	cwd := t.TempDir()

	// Write a simple file the worker can read.
	if err := os.WriteFile(filepath.Join(cwd, "task.txt"), []byte("Say DONE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Interactive test via tmux: dispatch then check.
	session := "ext-dispatch-" + randID()
	defer func() { _ = tmuxCommand("kill-session", "-t", session).Run() }()

	if err := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "36").Run(); err != nil {
		t.Fatal(err)
	}

	launch := fmt.Sprintf("cd %s && PIG_HOME=%s PIG_QUIET_STARTUP=1 %s --model github-copilot/gpt-5-mini\n",
		cwd, pigHome, bin)
	if err := tmuxCommand("send-keys", "-t", session, "-l", launch).Run(); err != nil {
		t.Fatal(err)
	}

	// Wait for startup.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
		if strings.Contains(string(out), interactiveReadyMarker) && strings.Contains(string(out), "[Extensions]") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Ask LLM to dispatch a worker sub-agent.
	prompt := "Use the dispatch_subagent tool to dispatch a worker agent in background mode with the task: 'Read task.txt and report its contents'. Just dispatch it, nothing else.\n"
	if err := tmuxCommand("send-keys", "-t", session, "-l", prompt).Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := tmuxCommand("send-keys", "-t", session, "C-m").Run(); err != nil {
		t.Fatal(err)
	}

	// Wait for the dispatch to complete (LLM response).
	time.Sleep(15 * time.Second)

	// Check status with /subs.
	if err := tmuxCommand("send-keys", "-t", session, "-l", "/subs\r").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)

	out, _ := tmuxCommand("capture-pane", "-t", session, "-p").Output()
	pane := string(out)

	t.Logf("pane after dispatch+check:\n%s", pane)

	// Skip on auth failure: can't test tool dispatch without LLM.
	if strings.Contains(pane, "401") || strings.Contains(pane, "Bad credentials") {
		_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
		t.Skip("auth expired: run 'pig login github-copilot' to fix")
	}

	// The dispatch should have produced some output: either running or completed.
	if strings.Contains(pane, "unknown command: /subs") {
		t.Fatal("/subs not registered: extension not loaded")
	}

	// Verify the LLM actually called the dispatch tool (not just echoed the prompt).
	lower := strings.ToLower(pane)
	if !strings.Contains(lower, "dispatch") && !strings.Contains(lower, "s1") && !strings.Contains(lower, "worker") {
		t.Errorf("no evidence dispatch_subagent tool was called:\n%s", pane)
	}

	_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
}
