package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// resumedBashCase is one persisted bashExecution message and the block Pi's addMessageToChat case "bashExecution"
// (interactive-mode.ts:3847-3859) builds for it: the constructor with the command and excludeFromContext, appendOutput
// only for a non-empty output, then setComplete(exitCode, cancelled, truncated ? {truncated} : undefined, fullOutputPath).
type resumedBashCase struct {
	name    string
	message BashExecutionMessage
	want    []string // plain rows the block must show
}

func resumedBashCases() []resumedBashCase {
	return []resumedBashCase{
		{"success", BashExecutionMessage{Command: "echo ok", Output: "ok\n", ExitCode: new(0)}, []string{"$ echo ok", "ok"}},
		{"non-zero exit", BashExecutionMessage{Command: "echo out; exit 3", Output: "out\n", ExitCode: new(3)}, []string{"$ echo out; exit 3", "out", "(exit 3)"}},
		{"cancelled", BashExecutionMessage{Command: "sleep 30", Cancelled: true}, []string{"$ sleep 30", "(cancelled)"}},
		{"truncated", BashExecutionMessage{Command: "seq 3", Output: "1\n2\n3\n", ExitCode: new(0), Truncated: true, FullOutputPath: "/tmp/pi-bash-0123456789abcdef.log"}, []string{"$ seq 3", "Output truncated. Full output: /tmp/pi-bash-0123456789abcdef.log"}},
		{"excluded", BashExecutionMessage{Command: "echo hidden", Output: "hidden\n", ExitCode: new(0), ExcludeFromContext: true}, []string{"$ echo hidden", "hidden"}},
		{"excluded non-zero", BashExecutionMessage{Command: "cat missing", Output: "cat: missing: No such file or directory\n", ExitCode: new(1), ExcludeFromContext: true}, []string{"$ cat missing", "(exit 1)"}},
	}
}

// piResumedBashBlock is the block Pi's addMessageToChat builds for a persisted message.
func piResumedBashBlock(message BashExecutionMessage) *tui.BashExecutionBlock {
	block := tui.NewBashExecutionBlock(message.Command, message.ExcludeFromContext)
	if message.Output != "" {
		block.AppendOutput(message.Output)
	}
	block.SetCompleteFullOutput(message.ExitCode, message.Cancelled, message.Truncated, message.FullOutputPath)
	return block
}

// resumeBashMode persists messages through the production writer, loads the Session file again, and returns an
// interactive mode bound to it.
func resumeBashMode(t *testing.T, messages ...BashExecutionMessage) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	sess, err := NewSessionManagerWithDir(dir, dir).Create("resume-bash", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		message.Timestamp = time.Now().UnixMilli()
		if _, err := sess.AppendBashExecution(message); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sess.AppendMessage(userMsg("resume-end")); err != nil {
		t.Fatal(err)
	}
	return resumeBashModeFromFile(t, dir, sess.Path())
}

func resumeBashModeFromFile(t *testing.T, dir, path string) *InteractiveMode {
	t.Helper()
	loaded, err := NewSessionManagerWithDir(dir, dir).Load(path)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	var terminal bytes.Buffer
	m := &InteractiveMode{
		opts:          InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: loaded}},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&terminal, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		outputPad:     1,
	}
	m.tuiInst.Add(m.chatContainer)
	return m
}

func chatBashBlocks(m *InteractiveMode) []*tui.BashExecutionBlock {
	var blocks []*tui.BashExecutionBlock
	for _, child := range m.chatContainer.Children() {
		if block, ok := child.(*tui.BashExecutionBlock); ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func assertResumedBashBlocks(t *testing.T, m *InteractiveMode, cases []resumedBashCase) {
	t.Helper()
	blocks := chatBashBlocks(m)
	if len(blocks) != len(cases) {
		t.Fatalf("resumed chat has %d bash blocks, want %d:\n%s", len(blocks), len(cases), renderedChat(m))
	}
	for i, tc := range cases {
		got, want := blocks[i].Render(80), piResumedBashBlock(tc.message).Render(80)
		if !slices.Equal(got, want) {
			t.Errorf("%s: block rows\n got %q\nwant %q", tc.name, got, want)
		}
		plain := stripANSITest(strings.Join(got, "\n"))
		for _, text := range tc.want {
			if !strings.Contains(plain, text) {
				t.Errorf("%s: block %q lacks %q", tc.name, plain, text)
			}
		}
		if strings.Contains(plain, "Running...") {
			t.Errorf("%s: resumed block still shows the loader: %q", tc.name, plain)
		}
	}
	chat := renderedChat(m)
	if last := strings.LastIndex(chat, "$ cat missing"); last < 0 || last > strings.Index(chat, "resume-end") {
		t.Errorf("bash blocks are not in session order before the user message:\n%s", chat)
	}
}

// Pi renderSessionEntries (interactive-mode.ts:4053) feeds every bashExecution message, including a `!!` one, to
// addMessageToChat, whose case "bashExecution" (:3847-3859) draws a completed BashExecutionComponent. Every Pig path
// that rebuilds the transcript from Session history shares renderSessionEntryList, so each must draw the blocks.
func TestResumedSessionRendersPersistedBashExecutionBlocks(t *testing.T) {
	cases := resumedBashCases()
	messages := make([]BashExecutionMessage, len(cases))
	for i, tc := range cases {
		messages[i] = tc.message
	}
	for _, path := range []struct {
		name   string
		render func(*InteractiveMode)
	}{
		{"initial resume (--session, --continue)", (*InteractiveMode).renderSessionEntries},
		{"rebuild (/tree, cache-miss and display settings, extension navigation)", (*InteractiveMode).rebuildChatFromSession},
		{"session replacement (/resume, /new, /fork, /clone, reload)", (*InteractiveMode).renderCurrentSessionState},
		{"compaction rebuild", func(m *InteractiveMode) { m.renderSessionEntryList(m.currentSession().GetBranch(), false) }},
	} {
		t.Run(path.name, func(t *testing.T) {
			m := resumeBashMode(t, messages...)
			path.render(m)
			assertResumedBashBlocks(t, m, cases)
		})
	}
}

// Pi addMessageToChat never calls setExpanded on a bashExecution component, so a rebuilt block starts collapsed
// even while tool output is expanded; Ctrl+O afterwards expands it with every other expandable child.
func TestResumedBashExecutionBlockStartsCollapsed(t *testing.T) {
	output := strings.Repeat("line\n", 24) + "last"
	m := resumeBashMode(t, BashExecutionMessage{Command: "seq 25", Output: output, ExitCode: new(0)})
	m.toolsExpanded = true
	m.rebuildChatFromSession()
	blocks := chatBashBlocks(m)
	if len(blocks) != 1 {
		t.Fatalf("bash blocks = %d, want 1", len(blocks))
	}
	if plain := stripANSITest(strings.Join(blocks[0].Render(80), "\n")); !strings.Contains(plain, "... 5 more lines") {
		t.Fatalf("rebuilt block is not collapsed: %q", plain)
	}
	m.setAllToolsExpanded(false)
	m.setAllToolsExpanded(true)
	if plain := stripANSITest(strings.Join(blocks[0].Render(80), "\n")); strings.Contains(plain, "more lines") || !strings.Contains(plain, "to collapse") {
		t.Fatalf("Ctrl+O did not expand the resumed block: %q", plain)
	}
}

// With no Session, Pig renders the agent's messages through the same message renderer.
func TestAgentMessageFallbackRendersBashExecutionBlock(t *testing.T) {
	m := &InteractiveMode{
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&bytes.Buffer{}, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		agent:         agent.NewAgent(agent.AgentOptions{}),
	}
	m.tuiInst.Add(m.chatContainer)
	m.agent.SetMessages([]agent.AgentMessage{{Custom: map[string]any{
		"role": agent.RoleBashExecution, "command": "echo ok", "output": "ok\n", "exitCode": float64(2), "excludeFromContext": true,
	}}})
	m.renderSessionEntries()
	blocks := chatBashBlocks(m)
	want := piResumedBashBlock(BashExecutionMessage{Command: "echo ok", Output: "ok\n", ExitCode: new(2), ExcludeFromContext: true})
	if len(blocks) != 1 || !slices.Equal(blocks[0].Render(80), want.Render(80)) {
		t.Fatalf("fallback chat = %q", renderedChat(m))
	}
}

// writeRawBashSession writes a Session file whose bashExecution messages hold exactly the given members, as an older
// or foreign writer may leave them.
func writeRawBashSession(t *testing.T, messages ...map[string]any) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "poisoned.jsonl")
	var file bytes.Buffer
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "poisoned-bash", "timestamp": "2026-10-07T00:00:00.000Z", "cwd": dir})
	file.Write(append(header, '\n'))
	var parent any
	for i, message := range messages {
		id := "p" + string(rune('a'+i))
		line, _ := json.Marshal(map[string]any{"type": "message", "id": id, "parentId": parent, "timestamp": "2026-10-07T00:00:01.000Z", "message": message})
		file.Write(append(line, '\n'))
		parent = id
	}
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// Poisoned bashExecution messages render as Pi's addMessageToChat renders them: a missing or empty output appends
// nothing, a missing or null exit code completes without a status, a non-zero exit shows it, cancellation wins over
// the exit code, truncation without a full-output path shows no notice, and ANSI escapes and carriage returns are
// cleaned. Pi 1.0.3 crashes at startup when command or output is missing (compaction.ts:338 estimateTokens reads
// their length for the footer), so for those two Pig only must not panic and draws the remaining fields.
func TestResumedPoisonedBashExecutionMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message map[string]any
		want    BashExecutionMessage
		rows    []string // trimmed plain rows between the borders
	}{
		{"missing exit code and flags", map[string]any{"command": "true", "output": ""}, BashExecutionMessage{Command: "true"}, []string{"$ true"}},
		{"null exit code", map[string]any{"command": "printf ''", "output": "", "exitCode": nil, "cancelled": false, "truncated": false}, BashExecutionMessage{Command: "printf ''"}, []string{"$ printf ''"}},
		{"empty output non-zero", map[string]any{"command": "false", "output": "", "exitCode": 2}, BashExecutionMessage{Command: "false", ExitCode: new(2)}, []string{"$ false", "", "(exit 2)"}},
		{"cancelled non-zero", map[string]any{"command": "sleep 9", "output": "partial\n", "exitCode": 130, "cancelled": true}, BashExecutionMessage{Command: "sleep 9", Output: "partial\n", ExitCode: new(130), Cancelled: true}, []string{"$ sleep 9", "", "partial", "", "", "(cancelled)"}},
		{"truncated without path", map[string]any{"command": "seq 2", "output": "1\n2\n", "exitCode": 0, "truncated": true}, BashExecutionMessage{Command: "seq 2", Output: "1\n2\n", ExitCode: new(0), Truncated: true}, []string{"$ seq 2", "", "1", "2", ""}},
		{"ansi and carriage returns", map[string]any{"command": "printf colors", "output": "\x1b[31mred\x1b[0m\r\nnext\rover\n", "exitCode": 0}, BashExecutionMessage{Command: "printf colors", Output: "red\nnext\nover\n", ExitCode: new(0)}, []string{"$ printf colors", "", "red", "next", "over", ""}},
		{"missing output", map[string]any{"command": "true", "exitCode": 0}, BashExecutionMessage{Command: "true", ExitCode: new(0)}, []string{"$ true"}},
		{"missing command", map[string]any{"output": "no command field\n", "exitCode": 0}, BashExecutionMessage{Output: "no command field\n", ExitCode: new(0)}, []string{"$", "", "no command field", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.message["role"] = agent.RoleBashExecution
			dir, path := writeRawBashSession(t, tc.message)
			m := resumeBashModeFromFile(t, dir, path)
			m.renderSessionEntries()
			blocks := chatBashBlocks(m)
			if len(blocks) != 1 {
				t.Fatalf("bash blocks = %d, want 1:\n%s", len(blocks), renderedChat(m))
			}
			got := blocks[0].Render(80)
			if want := piResumedBashBlock(tc.want).Render(80); !slices.Equal(got, want) {
				t.Errorf("block rows\n got %q\nwant %q", got, want)
			}
			var rows []string
			for _, line := range got[3 : len(got)-1] {
				rows = append(rows, strings.TrimSpace(stripANSITest(line)))
			}
			rows = append([]string{strings.TrimSpace(stripANSITest(got[2]))}, rows...)
			if !slices.Equal(rows, tc.rows) {
				t.Errorf("plain rows = %q, want %q", rows, tc.rows)
			}
		})
	}
}
