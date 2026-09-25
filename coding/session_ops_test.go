package coding

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// helper: append a user msg directly via inner session, bypassing Send
// so tests don't need a real LLM.
func appendUser(t *testing.T, s *Session, text string) string {
	t.Helper()
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:      "user",
			Content:   []ai.UserContentBlock{ai.TextContent{Text: text}},
			Timestamp: 1,
		},
	}
	id, err := s.inner.AppendMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func appendAsst(t *testing.T, s *Session, text string) string {
	t.Helper()
	msg := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:      "assistant",
			Content:   []ai.AssistantContentBlock{ai.TextContent{Text: text}},
			Timestamp: 2,
		},
	}
	id, err := s.inner.AppendMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// ─── Fork ────────────────────────────────────────────────────────────────────

func TestSessionForkRebuildsAgentMessages(t *testing.T) {
	// Public-API mirror of internal e2e test
	// TestE2E_ForkRewindsAgentHistoryAndExcludesAbandonedTail.
	// Proves the fork-and-rebuild bug we caught in 3.1c stays fixed
	// at the SDK layer.
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()

	uid1 := appendUser(t, sess, "first question")
	aid1 := appendAsst(t, sess, "first answer")
	appendUser(t, sess, "DEAD-END question")
	appendAsst(t, sess, "DEAD-END answer")

	// Rebuild agent's in-memory state to match disk so we can fork.
	sess.agent.SetMessages(sess.inner.BuildContext(nil))
	if got := sess.Messages(); len(got) != 4 || got[0].User == nil {
		t.Fatalf("pre-fork: want 4 directly appended conversation messages, got %v", got)
	}

	// Fork at the assistant reply to the first turn.
	if err := sess.Fork(aid1); err != nil {
		t.Fatal(err)
	}
	got := sess.Messages()
	if len(got) != 2 || got[0].User == nil {
		t.Fatalf("post-fork: want 2 retained conversation messages, got %v", got)
	}
	for _, m := range got {
		txt := messageText(m)
		if strings.Contains(txt, "DEAD-END") {
			t.Errorf("abandoned tail leaked: %q", txt)
		}
	}
	_ = uid1
}

func TestSessionForkUnknownEntryReturnsError(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	if err := sess.Fork("nonexistent-id"); err == nil {
		t.Fatal("expected error for unknown entry id")
	}
}

// ─── Clone ───────────────────────────────────────────────────────────────────

func TestSessionCloneCreatesIndependentFile(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	appendUser(t, sess, "shared turn")
	appendAsst(t, sess, "shared reply")

	cloned, err := sess.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer func() { _ = cloned.Close() }()

	if cloned.Path() == sess.Path() {
		t.Fatal("clone should have a distinct path")
	}
	if got := cloned.Messages(); len(got) != 2 || got[0].User == nil || got[1].Assistant == nil {
		t.Errorf("clone agent should have only the directly persisted user and assistant; got %v", got)
	}
}

func TestSessionCloneBootstrapSessionSucceeds(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	cloned, err := sess.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer func() { _ = cloned.Close() }()
	if cloned.Path() == sess.Path() {
		t.Fatal("clone should have a distinct path")
	}
}

// A clone preserves the source's active loadout rather than reactivating the
// complete registered tool set. Upstream restores the active tools when it
// reconstructs a Session runtime.
func TestSessionClonePreservesActiveTools(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active []string
	}{
		{name: "narrowed", active: []string{"second"}},
		{name: "empty", active: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svcs := newTestServices(t)
			sess, err := NewSession(svcs, SessionOptions{
				Model:            fakeModel(),
				SkipBuiltinTools: true,
				Tools: []agent.AgentTool{
					&fakeTool{name: "first"},
					&fakeTool{name: "second"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			sess.SetActiveToolsByName(tc.active)

			cloned, err := sess.Clone()
			if err != nil {
				t.Fatalf("Clone: %v", err)
			}
			defer func() { _ = cloned.Close() }()
			if got := cloned.ActiveToolNames(); !slices.Equal(got, tc.active) {
				t.Fatalf("clone active tools = %v, want %v", got, tc.active)
			}
		})
	}
}

// ─── Tree, LeafID ────────────────────────────────────────────────────────────

func TestSessionTreeReflectsBranches(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()

	uid1 := appendUser(t, sess, "first")
	_ = sess.Fork(uid1)
	appendUser(t, sess, "branch-A")
	_ = sess.Fork(uid1)
	appendUser(t, sess, "branch-B")

	tree := sess.Tree()
	if tree == nil {
		t.Fatal("nil tree")
	}
	// Just verify the tree builder produced *something* with the
	// branched structure.
	rendered := strings.Join([]string{
		"branch-A", "branch-B", "first",
	}, "")
	asci := icodingagentRenderTreeASCII(tree)
	for _, want := range []string{"first", "branch-A", "branch-B"} {
		if !strings.Contains(asci, want) {
			t.Errorf("tree rendering missing %q\nrendered=%q\nwanted-some-of=%q", want, asci, rendered)
		}
	}
}

func TestSessionLeafIDFollowsAppends(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	if leaf := sess.LeafID(); leaf == nil {
		t.Fatal("fresh session should contain bootstrap audit entries")
	}
	id := appendUser(t, sess, "x")
	if leaf := sess.LeafID(); leaf == nil || *leaf != id {
		t.Errorf("leaf after append = %v, want %s", leaf, id)
	}
}

// ─── SetName ─────────────────────────────────────────────────────────────────

func TestSessionSetNamePersists(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	if err := sess.SetName("my-test-session"); err != nil {
		t.Fatal(err)
	}
	flushSess(t, sess)
	infos, err := sess.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "my-test-session" {
		t.Errorf("name not persisted: %#v", infos)
	}
}

func TestSessionSetNameRejectsEmpty(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	if err := sess.SetName("  "); err == nil {
		t.Error("expected error for empty name")
	}
}

// ─── DispatchSlash ───────────────────────────────────────────────────────────

func TestDispatchSlashSessionShowsInfo(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	out, err := sess.DispatchSlash("/session")
	if err != nil {
		t.Fatalf("DispatchSlash: %v", err)
	}
	joined := strings.Join(out, "\n")
	// /session prints session info; at minimum should not be empty.
	if len(joined) == 0 {
		t.Error("/session produced empty output")
	}
}

func TestDispatchSlashUnknownReturnsError(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	_, err := sess.DispatchSlash("/this-command-does-not-exist")
	if err == nil {
		t.Fatal("expected error for unknown slash")
	}
}

func TestDispatchSlashTreeFallsBackToTextWhenNoPicker(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	appendUser(t, sess, "hello")

	out, err := sess.DispatchSlash("/tree")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "hello") {
		t.Errorf("/tree should fall back to text and include message text in headless mode\nfull:\n%s", joined)
	}
}

func TestDispatchSlashForkWithExplicitID(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()

	appendUser(t, sess, "first")
	appendAsst(t, sess, "reply")
	deadEnd := appendUser(t, sess, "DEAD-END")
	sess.agent.SetMessages(sess.inner.BuildContext(nil))
	srcPath := sess.Path()

	out, err := sess.DispatchSlash("/fork " + deadEnd)
	if err != nil {
		t.Fatalf("/fork: %v", err)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "Forked to new session") {
		t.Errorf("expected confirmation; got %q", joined)
	}
	// /fork branches into a NEW file recording parentSession, not in place.
	if sess.Path() == srcPath {
		t.Errorf("/fork must switch to a new session file; still on %s", srcPath)
	}
	if sess.inner.ParentSession() != srcPath {
		t.Errorf("forked session parentSession=%q want %q", sess.inner.ParentSession(), srcPath)
	}
	// Selected message and its tail are excluded; earlier context is kept.
	for _, m := range sess.Messages() {
		if strings.Contains(messageText(m), "DEAD-END") {
			t.Error("DEAD-END leaked after /fork")
		}
	}
}

func TestDispatchSlashForkBareNoPickerShowsUsage(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	out, err := sess.DispatchSlash("/fork")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "Usage:") {
		t.Errorf("bare /fork should print usage in headless mode\nfull:\n%s", joined)
	}
}

func TestDispatchSlashResumeFallsBackToListing(t *testing.T) {
	svcs := newTestServices(t)
	sess, _ := NewSession(svcs, SessionOptions{Model: fakeModel()})
	defer func() { _ = sess.Close() }()
	flushSess(t, sess)
	out, err := sess.DispatchSlash("/resume")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out, "\n")
	// At least one session exists (the one we just created), so it
	// should appear in the listing.
	if !strings.Contains(joined, "Recent sessions") {
		t.Errorf("expected listing fallback; got %q", joined)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

// flushSess persists a session by appending an assistant message,
// upstream's disk-flush trigger (SessionManager._persist hasAssistant
// gate). Tests asserting on-disk state must flush first; a fresh session
// is not written until the model replies.
func flushSess(t *testing.T, sess *Session) {
	t.Helper()
	if _, err := sess.Inner().AppendMessage(agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:    "assistant",
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}},
		},
	}); err != nil {
		t.Fatalf("flush coding session: %v", err)
	}
}

func messageText(m agent.AgentMessage) string {
	switch {
	case m.User != nil:
		var b strings.Builder
		for _, c := range m.User.Content {
			if t, ok := c.(ai.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	case m.Assistant != nil:
		var b strings.Builder
		for _, c := range m.Assistant.Content {
			if t, ok := c.(ai.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	}
	return ""
}

// thin wrapper so tests can call icodingagent.RenderTreeASCII via a
// short name without polluting test imports.
func icodingagentRenderTreeASCII(root *SessionTreeNode) string {
	return icodingagent.RenderTreeASCII(root)
}

// envProbeTool records the tool environment the agent attaches to its call.
type envProbeTool struct{ got *agent.ToolEnvironment }

func (envProbeTool) Name() string  { return "env_probe" }
func (envProbeTool) Label() string { return "" }
func (envProbeTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "env_probe", Parameters: map[string]any{"type": "object"}}
}
func (envProbeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (p envProbeTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if env, ok := agent.ToolEnvironmentFrom(ctx); ok {
		*p.got = env
	}
	return agent.AgentToolResult{Content: "ok"}, nil
}

// toolCallProvider calls env_probe once, then stops.
type toolCallProvider struct{ calls int }

func (p *toolCallProvider) ID() string   { return "tool-call" }
func (p *toolCallProvider) Close() error { return nil }
func (p *toolCallProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.calls++
	if p.calls == 1 {
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "env_probe", Arguments: ai.JsonObject{}}}, Provider: p.ID(), Model: "fake-1", StopReason: ai.StopReasonToolUse}
		return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: message}), nil
	}
	message := sessionTestMessage(p.ID(), "done", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

// Pi exports PI_SESSION_ID and PI_SESSION_FILE to the bash tool from the
// active session; a clone must report its own file and id, not nothing.
func TestSessionCloneExposesItsOwnSessionToTools(t *testing.T) {
	svcs := newTestServices(t)
	var got agent.ToolEnvironment
	provider := &toolCallProvider{}
	model := &ai.Model{ID: "fake-1", DisplayName: "fake-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	sess, err := NewSession(svcs, SessionOptions{Model: model, Tools: []agent.AgentTool{envProbeTool{got: &got}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	appendUser(t, sess, "shared turn")
	appendAsst(t, sess, "shared reply")

	cloned, err := sess.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer func() { _ = cloned.Close() }()
	if _, err := cloned.Send(context.Background(), "probe"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.SessionFile == "" || got.SessionFile != cloned.Path() || got.SessionFile == sess.Path() {
		t.Fatalf("tool SessionFile = %q, want the clone's %q (source %q)", got.SessionFile, cloned.Path(), sess.Path())
	}
	if got.SessionID != cloned.inner.ID() || got.SessionID == sess.inner.ID() {
		t.Fatalf("tool SessionID = %q, want the clone's %q", got.SessionID, cloned.inner.ID())
	}
}
