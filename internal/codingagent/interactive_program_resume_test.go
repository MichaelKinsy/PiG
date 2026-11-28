// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type resumeTestSessionHandle struct {
	InteractiveSessionHandle
	inner *Session
}

func (h *resumeTestSessionHandle) Inner() *Session            { return h.inner }
func (h *resumeTestSessionHandle) ReplaceInner(next *Session) { h.inner = next }

func decodeOSC7577Sequence(t *testing.T, seq, token string) (kind string, argv []string, cwd string) {
	t.Helper()
	m := regexp.MustCompile(`^\x1b\]7577;(.*):mac=([0-9a-f]{64})\x1b\\$`).FindStringSubmatch(seq)
	if m == nil {
		t.Fatalf("sequence %q does not match OSC 7577", seq)
	}
	body, mac := m[1], m[2]
	sum := sha256.Sum256([]byte(body + ":" + token))
	if got := hex.EncodeToString(sum[:]); got != mac {
		t.Fatalf("mac = %s, want %s", mac, got)
	}
	pairs := map[string]string{}
	for pair := range strings.SplitSeq(body, ":") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			t.Fatalf("bad pair %q", pair)
		}
		pairs[k] = v
	}
	if pairs["pid"] != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid = %q, want %d", pairs["pid"], os.Getpid())
	}
	if pairs["clear"] == "1" {
		return "clear", nil, ""
	}
	for part := range strings.SplitSeq(pairs["argv"], ",") {
		b, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			t.Fatalf("decode argv %q: %v", part, err)
		}
		argv = append(argv, string(b))
	}
	if rawCwd, ok := pairs["cwd"]; ok {
		b, err := base64.StdEncoding.DecodeString(rawCwd)
		if err != nil {
			t.Fatalf("decode cwd %q: %v", rawCwd, err)
		}
		cwd = string(b)
	}
	return "resume", argv, cwd
}

func extractOSC7577Sequences(raw string) []string {
	return regexp.MustCompile(`\x1b\]7577;[^\x1b]*\x1b\\`).FindAllString(raw, -1)
}

type lockedResumeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedResumeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedResumeBuffer) WriteString(s string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.WriteString(s)
}

func (b *lockedResumeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type captureWriteTerminal struct {
	tui.Terminal
	buf lockedResumeBuffer
}

func (c *captureWriteTerminal) Start(func(string), func()) error              { return nil }
func (c *captureWriteTerminal) Stop()                                         {}
func (c *captureWriteTerminal) DrainInput(time.Duration, time.Duration) error { return nil }
func (c *captureWriteTerminal) Write(data string)                             { _, _ = c.buf.WriteString(data) }
func (c *captureWriteTerminal) Columns() int                                  { return 80 }
func (c *captureWriteTerminal) Rows() int                                     { return 24 }
func (c *captureWriteTerminal) KittyProtocolActive() bool                     { return false }
func (c *captureWriteTerminal) MoveBy(int)                                    {}
func (c *captureWriteTerminal) HideCursor()                                   {}
func (c *captureWriteTerminal) ShowCursor()                                   {}
func (c *captureWriteTerminal) ClearLine()                                    {}
func (c *captureWriteTerminal) ClearFromCursor()                              {}
func (c *captureWriteTerminal) ClearScreen()                                  {}
func (c *captureWriteTerminal) SetTitle(string)                               {}
func (c *captureWriteTerminal) SetProgress(bool)                              {}
func (c *captureWriteTerminal) SetProgramStatus(tui.ProgramStatus)            {}

func newInteractiveModeForResumeTest(t *testing.T, cwd, sessionDir, token, programName string, persisted bool) (*InteractiveMode, *captureWriteTerminal, *resumeTestSessionHandle) {
	t.Helper()
	term := &captureWriteTerminal{}
	sm := NewSessionManagerWithDir(cwd, sessionDir)
	var sess *Session
	var err error
	if persisted {
		sess, err = sm.Create("", "")
		if err != nil {
			t.Fatal(err)
		}
	} else {
		sess = NewSession("", cwd)
	}
	handle := &resumeTestSessionHandle{inner: sess}
	m := NewInteractiveMode(nil, InteractiveModeOptions{
		CWD:                cwd,
		AgentDir:           t.TempDir(),
		SessionDir:         sessionDir,
		SessionHandle:      handle,
		Terminal:           term,
		ProgramResumeToken: token,
		ResumeProgramName:  programName,
	})
	m.tuiInst = tui.NewWithOutput(&term.buf, 80, 24)
	m.chatContainer = tui.NewContainer()
	t.Cleanup(func() { m.stopInteractiveTui() })
	return m, term, handle
}

func TestInteractiveProgramResumeLifecycleAndPigletBinaryName(t *testing.T) {
	t.Setenv(ENV_AGENT_DIR, "")
	const token = "interactive-resume-token-1234"
	cwd := t.TempDir()
	sessionDir := t.TempDir()

	// 1. Piglet Binary name flows into both exit hint and OSC 7577 argv[0], and cwd is session.CWD().
	m, term, handle := newInteractiveModeForResumeTest(t, cwd, sessionDir, token, "pig-standard", true)

	// New session with no file yet: report writes nothing.
	m.programResume.Report()
	if got := extractOSC7577Sequences(term.buf.String()); len(got) != 0 {
		t.Fatalf("new session without file emitted %q, want none", got)
	}

	// First message writes the session file; message_start / agent_settled triggers ReportIfNone().
	if _, err := handle.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}, Timestamp: time.Now().UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	m.applyAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{}}})
	seqs := extractOSC7577Sequences(term.buf.String())
	if len(seqs) != 1 {
		t.Fatalf("after first message got %d OSC 7577 reports, want 1: %q", len(seqs), seqs)
	}
	kind, argv, gotCwd := decodeOSC7577Sequence(t, seqs[0], token)
	wantArgv := []string{"pig-standard", "--session-dir", sessionDir, "--session", handle.Inner().ID()}
	if kind != "resume" || !reflect.DeepEqual(argv, wantArgv) || gotCwd != cwd {
		t.Fatalf("report = (%s, %q, %q), want (resume, %q, %q)", kind, argv, gotCwd, wantArgv, cwd)
	}
	// Exit hint matches the exact same command (quoted if needed).
	prevTTY := stdoutIsTTY
	stdoutIsTTY = func() bool { return true }
	t.Cleanup(func() { stdoutIsTTY = prevTTY })
	hint := m.resumeHintCommand()
	if !strings.HasPrefix(hint, "pig-standard --session-dir ") || !strings.HasSuffix(hint, " --session "+handle.Inner().ID()) {
		t.Fatalf("resumeHintCommand = %q, want pig-standard with session-dir and id", hint)
	}

	// 2. Suspend / resume re-reports even when unchanged.
	m.programResume.ReportAgain()
	seqs = extractOSC7577Sequences(term.buf.String())
	if len(seqs) != 2 {
		t.Fatalf("after suspend/resume got %d reports, want 2", len(seqs))
	}
	kind2, argv2, cwd2 := decodeOSC7577Sequence(t, seqs[1], token)
	if kind2 != "resume" || !reflect.DeepEqual(argv2, wantArgv) || cwd2 != cwd {
		t.Fatalf("re-report = (%s, %q, %q), want (resume, %q, %q)", kind2, argv2, cwd2, wantArgv, cwd)
	}

	// 3. Switching to a session with no file (/new) clears.
	sm := NewSessionManagerWithDir(cwd, sessionDir)
	handle.ReplaceInner(NewSession("", cwd))
	m.programResume.Report()
	seqs = extractOSC7577Sequences(term.buf.String())
	if len(seqs) != 3 {
		t.Fatalf("after switch to no-file session got %d reports, want 3", len(seqs))
	}
	if kind3, _, _ := decodeOSC7577Sequence(t, seqs[2], token); kind3 != "clear" {
		t.Fatalf("switch to no-file session emitted %s, want clear", kind3)
	}

	// 4. Signal shutdown (SIGHUP/SIGTERM) keeps the reported command; user quit (quitByKeys) clears it.
	persisted2, err := sm.Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := persisted2.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}, Timestamp: time.Now().UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	handle.ReplaceInner(persisted2)
	m.programResume.Report()
	beforeSignal := len(extractOSC7577Sequences(term.buf.String()))
	// Signal path sets signalShutdownDone=true, quitByKeys=false, then stopInteractiveTui().
	m.signalShutdownDone.Store(true)
	m.quitByKeys = false
	m.stopInteractiveTui()
	afterSignal := len(extractOSC7577Sequences(term.buf.String()))
	if afterSignal != beforeSignal {
		t.Fatalf("signal shutdown wrote %d extra OSC 7577 sequences, want 0 (must keep command on SIGHUP/SIGTERM)", afterSignal-beforeSignal)
	}

	// User quit path: quitByKeys=true calls Clear() inside stopInteractiveTui().
	m2, term2, handle2 := newInteractiveModeForResumeTest(t, cwd, sessionDir, token, "", true)
	if _, err := handle2.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}, Timestamp: time.Now().UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	m2.programResume.Report()
	m2.quitByKeys = true
	m2.stopInteractiveTui()
	seqs2 := extractOSC7577Sequences(term2.buf.String())
	if len(seqs2) != 2 {
		t.Fatalf("user quit got %d OSC 7577 sequences, want 2 (report then clear): %q", len(seqs2), seqs2)
	}
	if k0, argv0, _ := decodeOSC7577Sequence(t, seqs2[0], token); k0 != "resume" || argv0[0] != AppName {
		t.Fatalf("stock report = (%s, %q), want (resume, [%s ...])", k0, argv0, AppName)
	}
	if k1, _, _ := decodeOSC7577Sequence(t, seqs2[1], token); k1 != "clear" {
		t.Fatalf("user quit final report = %s, want clear", k1)
	}
}

func TestInteractiveProgramResumeAbsentTokenWritesZeroBytes(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	m, term, handle := newInteractiveModeForResumeTest(t, cwd, sessionDir, "", "", true)
	if m.programResume != nil {
		t.Fatal("expected nil programResume reporter when ProgramResumeToken is empty")
	}
	if _, err := handle.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}, Timestamp: time.Now().UnixMilli()}}); err != nil {
		t.Fatal(err)
	}
	m.programResume.Report()
	m.applyAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{}}})
	m.applyAgentEvent(agent.AgentSettledEvent{})
	m.programResume.ReportAgain()
	m.quitByKeys = true
	m.stopInteractiveTui()
	if got := extractOSC7577Sequences(term.buf.String()); len(got) != 0 {
		t.Fatalf("absent token wrote OSC 7577 sequences: %q", got)
	}
	if strings.Contains(term.buf.String(), "\x1b]7577;") {
		t.Fatalf("absent token leaked 7577 escape bytes: %q", term.buf.String())
	}
}

func TestExternalEditorChildDoesNotReceiveOSC7577Token(t *testing.T) {
	const token = "editor-secret-osc7577-token"
	t.Setenv(tui.ProgramResumeTokenEnv, token)
	if got := tui.TakeProgramResumeToken(); got != token {
		t.Fatalf("TakeProgramResumeToken() = %q, want %q", got, token)
	}
	outFile := filepath.Join(t.TempDir(), "editor-env.txt")
	// Reuse the cross-platform TestEditorHelperProcess binary via editorCommand so this runs on Windows as well as Unix.
	editorBin := editorHelperName(t)
	t.Setenv(editorHelperEnv, "1")
	cmd := editorCommand(context.Background(), editorBin, []string{"-test.run=^TestEditorHelperProcess$", "--", "dump-osc7577", outFile, filepath.Join(t.TempDir(), "prompt.md")})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("editorCommand failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read editor env dump: %v", err)
	}
	if got := string(data); got != "present=false;value=" {
		t.Fatalf("external editor child saw OSC7577_TOKEN: %q, want present=false;value=", got)
	}
}
