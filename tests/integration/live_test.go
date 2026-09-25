//go:build integration && live

// Live integration tests: require a real github-copilot OAuth token
// at $PIG_HOME/agent/auth.json (or ~/.pig/agent/auth.json).
//
// Run with:
//
//	go test -tags="integration live" ./tests/integration/... -run TestLive -v
//
// Tests skip if no auth file is present. Use automation/dev/login-copilot.sh
// to set one up.
//
// Each test isolates its own PIG_HOME (a tmp dir with the user's
// real auth.json copied in) so sessions don't pollute the user's dir.
//
// All tests use github-copilot/gpt-5-mini for speed + cost.

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// liveModel is the canonical cheap model used across the integration suite.
// Kept in sync with the literal model strings in parity_test.go, perf_test.go,
// subagent_parity_test.go, ext_test.go. AGENTS.md
// "Test models" section is the doc anchor.
const liveModel = "github-copilot/gpt-5-mini"

// liveAuthFile returns the path to the user's real auth.json.
func liveAuthFile(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	pigHome := os.Getenv("PIG_HOME")
	if pigHome == "" {
		pigHome = filepath.Join(home, ".pig")
	}
	return filepath.Join(pigHome, "agent", "auth.json")
}

// requireLiveAuth skips the test if no live auth credential exists.
// Returns the path to the source auth.json so isolatedHome can copy it.
func requireLiveAuth(t *testing.T) string {
	t.Helper()
	authFile := liveAuthFile(t)
	data, err := os.ReadFile(authFile)
	if err != nil {
		t.Skipf("no live auth at %s: run automation/dev/login-copilot.sh to enable live tests", authFile)
	}
	var creds map[string]any
	if err := json.Unmarshal(data, &creds); err != nil {
		t.Skipf("auth.json malformed: %v", err)
	}
	if len(creds) == 0 {
		t.Skipf("auth.json empty: run automation/dev/login-copilot.sh")
	}
	if _, ok := creds["github-copilot"]; !ok {
		t.Skipf("no github-copilot credential: run automation/dev/login-copilot.sh")
	}
	return authFile
}

// isolatedHome creates a tmp PIG_HOME with the user's auth copied
// in. Sessions written by the test go into this tmp dir, not the
// user's real ~/.pig. Returns the PIG_HOME path; agent dir is at
// <home>/agent.
func isolatedHome(t *testing.T) string {
	t.Helper()
	src := requireLiveAuth(t)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(agentDir, "auth.json")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// runPrint runs `pig --print <prompt>` with isolated PIG_HOME +
// fresh cwd. Returns stdout (the model reply) and the cwd used.
func runPrint(t *testing.T, pigHome, model, prompt string, extraArgs ...string) (string, string) {
	t.Helper()
	bin := buildBinary(t)
	cwd := t.TempDir()
	args := append([]string{"--cwd", cwd, "--model", model, "--print", prompt}, extraArgs...)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"PIG_HOME="+pigHome,
		"PIG_QUIET_STARTUP=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig --print failed: %v\noutput:\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), cwd
}

// TestLive_PrintModeBasicReply: simplest smoke test: auth resolves,
// model responds, no tool use needed.
func TestLive_PrintModeBasicReply(t *testing.T) {
	home := isolatedHome(t)
	out, _ := runPrint(t, home, liveModel,
		"Reply with EXACTLY the single word PONG and nothing else. No punctuation, no quotes, no other words.")
	// Model often appends punctuation; accept anything containing PONG.
	if !strings.Contains(strings.ToUpper(out), "PONG") {
		t.Fatalf("expected PONG in reply, got:\n%s", out)
	}
}

// TestLive_PrintModeUsesBashTool: prompt that requires a tool call.
// We use a sentinel file in the cwd and ask the model to read it via
// bash; the reply should contain the sentinel.
func TestLive_PrintModeUsesBashTool(t *testing.T) {
	home := isolatedHome(t)
	bin := buildBinary(t)
	cwd := t.TempDir()
	sentinel := "ZEPHYR-7142-MAGENTA"
	if err := os.WriteFile(filepath.Join(cwd, "secret.txt"), []byte(sentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin,
		"--cwd", cwd,
		"--model", liveModel,
		"--print", "Use the bash tool to cat secret.txt in the current directory and report its contents. Just print the contents in your reply.")
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_QUIET_STARTUP=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), sentinel) {
		t.Fatalf("expected sentinel %q in output (proves bash tool ran); got:\n%s", sentinel, out)
	}
}

// TestLive_InteractiveTwoTurnsPersistThenResume: launches pig
// interactively in tmux, sends two prompts, exits cleanly, then
// inspects the session JSONL on disk and resumes via --continue.
//
// Proves end-to-end: TUI input → agent.Send → on-disk persistence →
// resume rebuilds the same conversation.
func TestLive_InteractiveTwoTurnsPersistThenResume(t *testing.T) {
	home := isolatedHome(t)
	bin := buildBinary(t)
	cwd := t.TempDir()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	session := "pig-live-" + randID()
	defer func() { _ = tmuxCommand("kill-session", "-t", session).Run() }()

	if out, err := tmuxCommand("new-session", "-d", "-s", session, "-x", "130", "-y", "36").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}

	// Launch pig in the tmux session.
	launch := fmt.Sprintf("cd %s && PIG_HOME=%s PIG_QUIET_STARTUP=1 %s --model %s\n",
		cwd, home, bin, liveModel)
	if out, err := tmuxCommand("send-keys", "-t", session, "-l", launch).CombinedOutput(); err != nil {
		t.Fatalf("send launch: %v\n%s", err, out)
	}
	time.Sleep(3500 * time.Millisecond)

	sendLine := func(s string) {
		if out, err := tmuxCommand("send-keys", "-t", session, "-l", s).CombinedOutput(); err != nil {
			t.Fatalf("send-keys -l: %v\n%s", err, out)
		}
		time.Sleep(300 * time.Millisecond)
		// C-m = ASCII \r = Submit (Enter sometimes ambiguous in tmux).
		if out, err := tmuxCommand("send-keys", "-t", session, "C-m").CombinedOutput(); err != nil {
			t.Fatalf("send-keys C-m: %v\n%s", err, out)
		}
	}

	sessDir := filepath.Join(home, "agent", "sessions")

	// waitForMessages polls the session JSONL on disk until it contains
	// at least `wantTotal` non-header entries. This is the most
	// reliable signal: persistence happens after agent.Send returns
	// successfully, so a count >= N proves N turns completed.
	waitForMessages := func(wantTotal int, timeout time.Duration) {
		deadline := time.Now().Add(timeout)
		lastSeen := -1
		for time.Now().Before(deadline) {
			files := walkSessionFiles(t, sessDir)
			if len(files) > 0 {
				lines := readLines(t, files[0])
				n := len(lines) - 1 // minus header
				if n != lastSeen {
					lastSeen = n
				}
				if n >= wantTotal {
					time.Sleep(300 * time.Millisecond)
					return
				}
			}
			time.Sleep(400 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d entries on disk (last seen: %d)", wantTotal, lastSeen)
	}

	// Turn 1: introduce a memorable token the model will be asked to
	// recall in turn 2.
	token := "ASTROLITH-" + randID()[:6]
	sendLine(fmt.Sprintf("Remember this token: %s. Just acknowledge it.", token))
	waitForMessages(2, 60*time.Second) // 1 user + 1 assistant

	// Turn 2: ask for the token back. This proves multi-turn context.
	sendLine("Repeat the token I asked you to remember, exactly as written. Just the token.")
	waitForMessages(4, 60*time.Second) // 2 user + 2 assistant

	// Verify the conversation captured turn 2's token recall on disk.
	capLines := readLines(t, walkSessionFiles(t, sessDir)[0])
	if !linesContainAny(capLines[1:], token) {
		t.Fatalf("expected token %q in some message entry on disk; got %d lines:\n%s",
			token, len(capLines), strings.Join(capLines, "\n"))
	}

	// Quit cleanly.
	_ = tmuxCommand("send-keys", "-t", session, "C-d").Run()
	time.Sleep(500 * time.Millisecond)

	// Verify session JSONL on disk has the conversation.
	entries := walkSessionFiles(t, sessDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 session file under %s, got %d", sessDir, len(entries))
	}
	jsonl := entries[0]
	lines := readLines(t, jsonl)
	// 1 header + at least 4 messages (2 user + 2 assistant). Tool
	// calls in between would add more: accept >= 5.
	if len(lines) < 5 {
		t.Fatalf("session %s has %d lines, expected >= 5 (1 header + 2 turns):\n%s",
			jsonl, len(lines), strings.Join(lines, "\n"))
	}
	// Header sanity.
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header line not JSON: %v", err)
	}
	if header["type"] != "session" || header["version"].(float64) != 3 {
		t.Errorf("header wrong shape: %+v", header)
	}
	// Token must appear in some message entry's content.
	found := false
	for _, l := range lines[1:] {
		if strings.Contains(l, token) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("token %q not found in any message entry in %s", token, jsonl)
	}

	// Now resume: launch pig with --continue, verify banner shows
	// resumed-N-messages and that asking for the token works again
	// without restating it.
	resumeSession := "pig-live-resume-" + randID()
	defer func() { _ = tmuxCommand("kill-session", "-t", resumeSession).Run() }()
	if out, err := tmuxCommand("new-session", "-d", "-s", resumeSession, "-x", "130", "-y", "36").CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v\n%s", err, out)
	}
	resumeCmd := fmt.Sprintf("cd %s && PIG_HOME=%s PIG_QUIET_STARTUP=1 %s --model %s --continue\n",
		cwd, home, bin, liveModel)
	if err := tmuxCommand("send-keys", "-t", resumeSession, "-l", resumeCmd).Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3500 * time.Millisecond)

	out, _ := tmuxCommand("capture-pane", "-t", resumeSession, "-p").Output()
	if !strings.Contains(string(out), "resumed") {
		t.Errorf("expected 'resumed' in banner; got:\n%s", out)
	}

	// Ask for the token again; should still know it.
	if err := tmuxCommand("send-keys", "-t", resumeSession, "-l",
		"What was the token? Just print it.").Run(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := tmuxCommand("send-keys", "-t", resumeSession, "C-m").Run(); err != nil {
		t.Fatal(err)
	}
	// Resume-side: poll for one more user+assistant pair.
	wantAfter := len(lines) - 1 + 2 // existing + 1 user + 1 assistant
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		now := readLines(t, jsonl)
		if len(now)-1 >= wantAfter {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	finalLines := readLines(t, jsonl)
	if !linesContainAny(finalLines, token) {
		t.Fatalf("after resume, no entry contained token %q. Disk has %d lines:\n%s",
			token, len(finalLines), strings.Join(finalLines, "\n"))
	}
	_ = tmuxCommand("send-keys", "-t", resumeSession, "C-d").Run()
}

// ─── helpers ────────────────────────────────────────────────────────────────

// walkSessionFiles returns all *.jsonl files under sessDir (recursive).
func walkSessionFiles(t *testing.T, sessDir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(sessDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := raw[:0]
	for _, l := range raw {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// linesContainAny returns true if any line contains needle.
func linesContainAny(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// ─── SDK consumer tests ──────────────────────────────────────────────────────
//
// These exercise the public coding/ package directly, the same way
// any external library consumer would. No subprocess, no TUI: just
// coding.NewServices → coding.NewRuntime → rt.New → sess.Send.
//
// PR F5: proves the SDK is usable end-to-end against a
// real LLM with the same auth path the pig binary uses.

// TestLive_SDKEmbeddedSessionSendReceives: minimum viable embed test.
// Constructs Services + Runtime + Session via the public API, sends
// a single prompt, asserts the reply is non-empty and includes a
// number that resembles "2".
func TestLive_SDKEmbeddedSessionSendReceives(t *testing.T) {
	pigHome := isolatedHome(t)
	t.Setenv("PIG_HOME", pigHome)
	cwd := t.TempDir()

	svcs, err := coding.NewServices(coding.ServicesOptions{
		CWD:      cwd,
		AgentDir: filepath.Join(pigHome, "agent"),
	})
	if err != nil {
		t.Fatalf("NewServices: %v", err)
	}
	rt, err := coding.NewRuntime(coding.RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()

	model, err := coding.BuildModel(liveModel, svcs)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	sess, err := rt.New(coding.SessionStartOptions{
		Model:        model,
		SystemPrompt: "You are concise. Reply in under 10 words.",
	})
	if err != nil {
		t.Fatalf("rt.New: %v", err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()

	ctx, cancel := contextWithDeadline(t, 60*time.Second)
	defer cancel()

	msgs, err := sess.Send(ctx, "What is 1 + 1? Reply with the number only.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages returned")
	}
	reply := lastAssistantTextSDK(msgs)
	if reply == "" {
		t.Fatalf("no assistant text in reply\nmsgs: %#v", msgs)
	}
	if !strings.Contains(reply, "2") {
		t.Errorf("expected reply to mention 2; got %q", reply)
	}
	t.Logf("SDK reply: %s", reply)
	t.Logf("Session JSONL: %s", sess.Path())

	// Verify the session was actually persisted to disk.
	if _, err := os.Stat(sess.Path()); err != nil {
		t.Errorf("session JSONL missing on disk: %v", err)
	}
}

// TestLive_SDKMultiSessionSharingRuntime: two sessions on the same
// Runtime, each independent. Proves Runtime is reusable and sessions
// don't leak state into each other.
func TestLive_SDKMultiSessionSharingRuntime(t *testing.T) {
	pigHome := isolatedHome(t)
	t.Setenv("PIG_HOME", pigHome)
	cwd := t.TempDir()

	svcs, err := coding.NewServices(coding.ServicesOptions{
		CWD:      cwd,
		AgentDir: filepath.Join(pigHome, "agent"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := coding.NewRuntime(coding.RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()

	model, err := coding.BuildModel(liveModel, svcs)
	if err != nil {
		t.Fatal(err)
	}
	startOpts := coding.SessionStartOptions{
		Model:        model,
		SystemPrompt: "Reply with a single short word.",
	}

	sess1, err := rt.New(startOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess1.Close(); err != nil {
			t.Error(err)
		}
	}()
	sess2, err := rt.New(startOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess2.Close(); err != nil {
			t.Error(err)
		}
	}()

	if sess1.ID() == sess2.ID() {
		t.Fatal("two New sessions should have distinct IDs")
	}
	if sess1.Path() == sess2.Path() {
		t.Fatal("two New sessions should have distinct paths")
	}

	ctx, cancel := contextWithDeadline(t, 60*time.Second)
	defer cancel()

	if _, err := sess1.Send(ctx, "Say the word 'apple'."); err != nil {
		t.Fatalf("sess1.Send: %v", err)
	}
	if _, err := sess2.Send(ctx, "Say the word 'banana'."); err != nil {
		t.Fatalf("sess2.Send: %v", err)
	}

	r1 := lastAssistantTextSDK(sess1.Messages())
	r2 := lastAssistantTextSDK(sess2.Messages())
	if r1 == r2 {
		t.Errorf("sessions should have independent history; both replied %q", r1)
	}
	t.Logf("sess1 reply: %s", r1)
	t.Logf("sess2 reply: %s", r2)
}

// ─── SDK helpers ─────────────────────────────────────────────────────────────

func contextWithDeadline(t *testing.T, d time.Duration) (ctx context.Context, cancel func()) {
	t.Helper()
	return context.WithTimeout(context.Background(), d)
}

func lastAssistantTextSDK(msgs []agent.AgentMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Assistant == nil {
			continue
		}
		var b strings.Builder
		for _, c := range msgs[i].Assistant.Content {
			if tc, ok := c.(ai.TextContent); ok {
				b.WriteString(tc.Text)
			}
		}
		return b.String()
	}
	return ""
}
