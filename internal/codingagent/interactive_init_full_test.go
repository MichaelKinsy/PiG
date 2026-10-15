//go:build linux

package codingagent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const initFullChildEnv = "PIG_TEST_INIT_FULL_CHILD"

// InteractiveMode.init() (interactive-mode.ts:690-1030) against a real Session on a pseudo-terminal: Init completes, leaves the editor, footer
// and containers configured from the settings and the Session, hides the cursor while it runs, and Stop restores the terminal.
// Init needs a terminal for raw mode, so it runs in a child process on a pty, which reports the state it left in a file.
func TestInitBuildsTheInteractiveModeFromTheSettingsAndTheSession(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("pseudo-terminal number: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave: %v", err)
	}
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitBuildsTheInteractiveModeChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), initFullChildEnv+"="+report, "TERM=xterm-256color", "PIG_HOME="+t.TempDir())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, master)
	}()
	waitErr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		_ = master.Close()
		<-copied
	}
	raw, readErr := os.ReadFile(report)
	if waitErr != nil || readErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestInitBuildsTheInteractiveModeChild")) {
		t.Fatalf("child failed: %v %v\n%s\n%q", waitErr, readErr, raw, captured.Bytes()[max(0, captured.Len()-600):])
	}
	var report2 struct {
		Failure  string
		InitErr  string
		Snapshot icodingagent.InitSnapshot
		Marks    []string
	}
	if err := json.Unmarshal(raw, &report2); err != nil {
		t.Fatal(err)
	}
	if report2.Failure != "" {
		t.Fatal(report2.Failure)
	}
	if report2.InitErr != "" {
		t.Fatalf("Init failed: %s (marks %q)", report2.InitErr, report2.Marks)
	}
	snapshot := report2.Snapshot
	if !snapshot.Initialized || snapshot.EditorPaddingX != 2 || snapshot.AutocompleteMax != 7 {
		t.Errorf("editor = padding %d, autocomplete %d, initialized %v; want the settings' 2 and 7", snapshot.EditorPaddingX, snapshot.AutocompleteMax, snapshot.Initialized)
	}
	if snapshot.SteeringMode != "all" || snapshot.FollowUpMode != "all" {
		t.Errorf("queue modes = %q/%q, want all/all from the settings", snapshot.SteeringMode, snapshot.FollowUpMode)
	}
	if snapshot.AutoCompact {
		t.Error("the footer shows auto-compact although the settings disable it")
	}
	if snapshot.StatusName != "initial session" || !snapshot.TimingsBound {
		t.Errorf("footer name %q, timings bound %v; want the session's name and the agent's timings", snapshot.StatusName, snapshot.TimingsBound)
	}
	if !snapshot.HasUI || !snapshot.ExtensionSession {
		t.Errorf("extension context has UI %v and session %v, want both", snapshot.HasUI, snapshot.ExtensionSession)
	}
	if snapshot.StartupHeaderPending {
		t.Error("the startup header is still pending after Init")
	}
	if !snapshot.HideThinking {
		t.Error("hideThinkingBlock from the settings did not reach the mode")
	}
	if snapshot.ChatChildren == 0 || !strings.Contains(captured.String(), "saved question") {
		t.Errorf("the saved transcript was not rendered (%d chat children)", snapshot.ChatChildren)
	}
	if snapshot.WidgetChildren != 1 || !snapshot.KeybindingsSet || !snapshot.SlashRegistrySet {
		t.Errorf("widgets %d, keybindings %v, slash registry %v", snapshot.WidgetChildren, snapshot.KeybindingsSet, snapshot.SlashRegistrySet)
	}
	if !snapshot.FocusIsEditor {
		t.Error("the editor does not hold the focus after Init")
	}
	if snapshot.FooterCwd == "" || !strings.Contains(strings.Join(snapshot.SlashCommands, " "), "model") || strings.Join(snapshot.PromptTemplates, " ") != "review" || snapshot.ResourceSourceInfo != 1 {
		t.Errorf("footer cwd %q, slash commands %q, prompt templates %q, source info %d; want the cwd, the built-ins, the review template and the provider's map", snapshot.FooterCwd, snapshot.SlashCommands, snapshot.PromptTemplates, snapshot.ResourceSourceInfo)
	}
	if snapshot.Teardowns < 8 {
		t.Errorf("Init registered %d teardowns, want the terminal, input, cursor and renderer ones", snapshot.Teardowns)
	}
	output := captured.String()
	hide, show := strings.Index(output, "\x1b[?25l"), strings.LastIndex(output, "\x1b[?25h")
	if hide < 0 || show < hide {
		t.Errorf("the cursor is not hidden while the mode runs and shown again on Stop (hide at %d, show at %d)", hide, show)
	}
	if !strings.Contains(output, "initial session") {
		t.Errorf("the first paint shows no session name:\n%q", output[:min(len(output), 600)])
	}
	if !strings.Contains(output, "\x1b]0;") {
		t.Error("Stop did not clear the terminal title")
	}
}

func TestInitBuildsTheInteractiveModeChild(t *testing.T) {
	reportPath := os.Getenv(initFullChildEnv)
	if reportPath == "" {
		t.Skip("runs only as the pty child of TestInitBuildsTheInteractiveModeFromTheSettingsAndTheSession")
	}
	report := struct {
		Failure  string
		InitErr  string
		Snapshot icodingagent.InitSnapshot
		Marks    []string
	}{}
	write := func() {
		raw, _ := json.Marshal(report)
		_ = os.WriteFile(reportPath, raw, 0o600)
	}
	defer write()
	fail := func(format string, args ...any) {
		report.Failure = fmt.Sprintf(format, args...)
		t.Fatal(report.Failure)
	}
	home := os.Getenv("PIG_HOME")
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		fail("%v", err)
	}
	settings := `{"editorPaddingX":2,"autocompleteMaxVisible":7,"steeringMode":"all","followUpMode":"all","hideThinkingBlock":true,"compaction":{"enabled":false}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		fail("%v", err)
	}
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(agentDir, "prompts"), 0o755); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "prompts", "review.md"), []byte("Review the change.\n"), 0o600); err != nil {
		fail("%v", err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		fail("%v", err)
	}
	manager, err := coding.NewInMemorySessionManager(cwd)
	if err != nil {
		fail("%v", err)
	}
	if _, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "saved question"}}}}); err != nil {
		fail("%v", err)
	}
	if _, err := manager.AppendSessionInfo("initial session"); err != nil {
		fail("%v", err)
	}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SessionManager: manager})
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = session.Close() }()
	mode := icodingagent.NewInteractiveMode(nil, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session, NoThemes: true, ResumePath: filepath.Join(cwd, "resumed.jsonl"), PromptPaths: []string{filepath.Join(agentDir, "prompts")},
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
		StartupMark: func(label string) { report.Marks = append(report.Marks, label) },
		ResourceSourceInfoProvider: func() map[string]icodingagent.ResourceSourceInfo {
			return map[string]icodingagent.ResourceSourceInfo{"review": {}}
		},
	})
	if err := mode.Init(t.Context()); err != nil {
		report.InitErr = err.Error()
		_ = mode.Stop()
		return
	}
	report.Snapshot = mode.InitSnapshot()
	if err := mode.Stop(); err != nil {
		fail("Stop: %v", err)
	}
}
