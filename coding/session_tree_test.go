package coding

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Tests for Session.NavigateTree against upstream agent-session.ts navigateTree
// and its tests (agent-session-tree-navigation.test.ts,
// branch-summary-extensions.test.ts, regressions 3688 and 9178).

func newTreeTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func appendTreeUser(t *testing.T, sess *Session, text string) string {
	t.Helper()
	id, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role:    "user",
		Content: []ai.UserContentBlock{ai.TextContent{Text: text}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func appendTreeAssistant(t *testing.T, sess *Session, text string) string {
	t.Helper()
	id, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: text}},
		StopReason: ai.StopReasonStop,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newTreeTestSessionWithSettings writes settings.json before building the
// session services.
func newTreeTestSessionWithSettings(t *testing.T, settings string) *Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// promptCapturingCompleter records each summarization prompt.
type promptCapturingCompleter struct {
	summary string
	prompts []string
}

func (c *promptCapturingCompleter) CompleteSimple(_ context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
	var prompt strings.Builder
	for _, message := range messages {
		if message.User == nil {
			continue
		}
		for _, block := range message.User.Content {
			if text, ok := block.(ai.TextContent); ok {
				prompt.WriteString(text.Text)
			}
		}
	}
	c.prompts = append(c.prompts, prompt.String())
	return c.summary, nil, nil
}

func treeLeaf(sess *Session) string {
	if leaf := sess.inner.LeafID(); leaf != nil {
		return *leaf
	}
	return "<nil>"
}

// Upstream "should handle navigation to same position (no-op)": selecting the
// current leaf changes nothing, even when that leaf is a user message.
func TestNavigateTreeToCurrentLeafIsNoOp(t *testing.T) {
	sess := newTreeTestSession(t)
	appendTreeUser(t, sess, "hello")
	appendTreeAssistant(t, sess, "hi")
	leaf := appendTreeUser(t, sess, "unanswered")
	entriesBefore := len(sess.inner.Entries())

	res, err := sess.NavigateTree(context.Background(), leaf, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.Aborted || res.EditorText != "" {
		t.Fatalf("result = %+v, want zero no-op result", res)
	}
	if got := treeLeaf(sess); got != leaf {
		t.Fatalf("leaf = %s, want unchanged %s", got, leaf)
	}
	if got := len(sess.inner.Entries()); got != entriesBefore {
		t.Fatalf("entries = %d, want %d", got, entriesBefore)
	}
}

// Upstream throws `Entry <id> not found` before touching the session.
func TestNavigateTreeRejectsMissingEntry(t *testing.T) {
	sess := newTreeTestSession(t)
	appendTreeUser(t, sess, "hello")
	leaf := appendTreeAssistant(t, sess, "hi")

	_, err := sess.NavigateTree(context.Background(), "missing-entry", NavigateTreeOptions{})
	if err == nil || err.Error() != "Entry missing-entry not found" {
		t.Fatalf("error = %v, want Entry missing-entry not found", err)
	}
	if got := treeLeaf(sess); got != leaf {
		t.Fatalf("leaf = %s, want unchanged %s", got, leaf)
	}
	if sess.IsCompacting() {
		t.Fatal("IsCompacting stayed true after the rejected navigation")
	}
}

// Upstream throws "No model available for summarization" when summarize is
// requested without a model.
func TestNavigateTreeSummarizeRequiresModel(t *testing.T) {
	sess := newTreeTestSession(t)
	target := appendTreeUser(t, sess, "hello")
	appendTreeAssistant(t, sess, "hi")
	leaf := appendTreeUser(t, sess, "again")
	sess.model.Store(nil)

	_, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
	if err == nil || err.Error() != "No model available for summarization" {
		t.Fatalf("error = %v, want No model available for summarization", err)
	}
	if got := treeLeaf(sess); got != leaf {
		t.Fatalf("leaf = %s, want unchanged %s", got, leaf)
	}
}

// Upstream treats a custom_message target like a user message: the leaf moves
// to its parent and contentText(content, "") goes to the editor.
func TestNavigateTreeCustomMessageTarget(t *testing.T) {
	cases := []struct {
		name    string
		content any
		want    string
	}{
		{name: "string content", content: "custom note", want: "custom note"},
		{name: "block content", content: []map[string]any{
			{"type": "text", "text": "first "},
			{"type": "image", "data": "aGk=", "mimeType": "image/png"},
			{"type": "text", "text": "second"},
		}, want: "first second"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sess := newTreeTestSession(t)
			appendTreeUser(t, sess, "hello")
			parent := appendTreeAssistant(t, sess, "hi")
			target, err := sess.inner.AppendCustomMessage("note", tc.content, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			appendTreeUser(t, sess, "later")

			res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
			if err != nil {
				t.Fatalf("NavigateTree: %v", err)
			}
			if res.EditorText != tc.want {
				t.Fatalf("EditorText = %q, want %q", res.EditorText, tc.want)
			}
			if got := treeLeaf(sess); got != parent {
				t.Fatalf("leaf = %s, want custom message parent %s", got, parent)
			}
		})
	}
}

// Upstream editorText is contentText(message.content, ""): every text block of
// the user message, joined without a separator.
func TestNavigateTreeUserTargetJoinsTextBlocks(t *testing.T) {
	sess := newTreeTestSession(t)
	target, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
		Role: "user",
		Content: []ai.UserContentBlock{
			ai.TextContent{Text: "look at "},
			ai.ImageContent{Data: "aGk=", MimeType: "image/png"},
			ai.TextContent{Text: "this"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	appendTreeAssistant(t, sess, "ok")

	res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.EditorText != "look at this" {
		t.Fatalf("EditorText = %q, want %q", res.EditorText, "look at this")
	}
}

// Upstream passes settings branchSummary.reserveTokens to generateBranchSummary,
// which bounds the abandoned-branch messages in the prompt to
// contextWindow - reserveTokens.
func TestNavigateTreeSummaryUsesBranchSummaryReserveTokens(t *testing.T) {
	// fakeModel has an 8000-token window, so the budget is 10 tokens.
	sess := newTreeTestSessionWithSettings(t, `{"branchSummary":{"reserveTokens":7990}}`)
	completer := &promptCapturingCompleter{summary: "summary"}
	sess.completer = completer
	target := appendTreeUser(t, sess, "start")
	appendTreeAssistant(t, sess, "ready")
	appendTreeUser(t, sess, "OLDEST-ABANDONED "+strings.Repeat("filler ", 60))
	appendTreeAssistant(t, sess, "z")

	if _, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true}); err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if len(completer.prompts) != 1 {
		t.Fatalf("summarizer calls = %d, want 1", len(completer.prompts))
	}
	if strings.Contains(completer.prompts[0], "OLDEST-ABANDONED") {
		t.Fatal("prompt includes a message beyond the reserveTokens budget")
	}
}

// Upstream forwards replaceInstructions so custom instructions replace the
// default branch summary prompt instead of being appended to it.
func TestNavigateTreeSummaryReplaceInstructions(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := &promptCapturingCompleter{summary: "summary"}
	sess.completer = completer
	target := appendTreeUser(t, sess, "start")
	appendTreeAssistant(t, sess, "ready")
	appendTreeUser(t, sess, "abandoned")

	_, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{
		Summarize:           true,
		CustomInstructions:  "ONLY THESE INSTRUCTIONS",
		ReplaceInstructions: true,
	})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if len(completer.prompts) != 1 {
		t.Fatalf("summarizer calls = %d, want 1", len(completer.prompts))
	}
	prompt := completer.prompts[0]
	if !strings.HasSuffix(prompt, "</conversation>\n\nONLY THESE INSTRUCTIONS") {
		t.Fatalf("prompt does not end with the replacement instructions:\n%s", prompt)
	}
	if strings.Contains(prompt, "Additional focus") {
		t.Fatal("custom instructions were appended instead of replacing the prompt")
	}
}

// treeLabelTargets returns the targetId → label map from label entries.
func treeLabelTargets(t *testing.T, sess *Session) map[string]string {
	t.Helper()
	labels := map[string]string{}
	for _, entry := range sess.inner.Entries() {
		if entry.Base.Type != "label" {
			continue
		}
		var label icodingagent.LabelEntry
		if err := json.Unmarshal(entry.Raw(), &label); err != nil {
			t.Fatal(err)
		}
		if label.Label != nil {
			labels[label.TargetID] = *label.Label
		}
	}
	return labels
}

// Upstream attaches options.label to the target entry when no summary is
// created.
func TestNavigateTreeLabelsTargetWithoutSummary(t *testing.T) {
	sess := newTreeTestSession(t)
	appendTreeUser(t, sess, "hello")
	target := appendTreeAssistant(t, sess, "hi")
	appendTreeUser(t, sess, "abandoned")

	if _, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Label: "checkpoint"}); err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if got := treeLabelTargets(t, sess); got[target] != "checkpoint" || len(got) != 1 {
		t.Fatalf("labels = %v, want %s labelled checkpoint", got, target)
	}
	leaf, ok := sess.inner.EntryByID(treeLeaf(sess))
	if !ok || leaf.Base.Type != "label" || leaf.Base.ParentID == nil || *leaf.Base.ParentID != target {
		t.Fatalf("leaf = %+v, want label entry under %s", leaf.Base, target)
	}
}

// Upstream attaches options.label to the branch summary entry when one is
// created.
func TestNavigateTreeLabelsBranchSummary(t *testing.T) {
	sess := newTreeTestSession(t)
	sess.completer = &fakeCompleter{summary: "summary"}
	appendTreeUser(t, sess, "hello")
	target := appendTreeAssistant(t, sess, "hi")
	appendTreeUser(t, sess, "abandoned")

	if _, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true, Label: "explored"}); err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	var summaryID string
	for _, entry := range sess.inner.Entries() {
		if entry.Base.Type == "branch_summary" {
			summaryID = entry.Base.ID
		}
	}
	if summaryID == "" {
		t.Fatal("branch summary entry missing")
	}
	if got := treeLabelTargets(t, sess); got[summaryID] != "explored" || len(got) != 1 {
		t.Fatalf("labels = %v, want summary %s labelled explored", got, summaryID)
	}
}

// Ports of agent-session-tree-navigation.test.ts (upstream runs them against a
// live model; these use a fake summarizer). PiG sessions start with bootstrap
// model/thinking entries, so the first user message is not a root entry unless
// the leaf is reset before it is appended.

func TestNavigateTreeRootUserMessageResetsLeaf(t *testing.T) {
	sess := newTreeTestSession(t)
	if err := sess.inner.SetLeafID(nil); err != nil {
		t.Fatal(err)
	}
	root := appendTreeUser(t, sess, "First message")
	appendTreeAssistant(t, sess, "a1")
	appendTreeUser(t, sess, "Second message")
	appendTreeAssistant(t, sess, "a2")

	res, err := sess.NavigateTree(context.Background(), root, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.EditorText != "First message" || res.SummaryEntry != nil {
		t.Fatalf("result = %+v", res)
	}
	if leaf := sess.inner.LeafID(); leaf != nil {
		t.Fatalf("leaf = %s, want nil", *leaf)
	}
}

func TestNavigateTreeSummaryAtRootIsRootEntry(t *testing.T) {
	sess := newTreeTestSession(t)
	sess.completer = &fakeCompleter{summary: "summary of 2+2"}
	if err := sess.inner.SetLeafID(nil); err != nil {
		t.Fatal(err)
	}
	root := appendTreeUser(t, sess, "What is 2+2?")
	appendTreeAssistant(t, sess, "4")
	appendTreeUser(t, sess, "What is 3+3?")
	oldLeaf := appendTreeAssistant(t, sess, "6")

	res, err := sess.NavigateTree(context.Background(), root, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.EditorText != "What is 2+2?" {
		t.Fatalf("result = %+v", res)
	}
	summary := res.SummaryEntry
	if summary == nil || summary.Type != "branch_summary" || !strings.Contains(summary.Summary, "summary of 2+2") {
		t.Fatalf("summary entry = %+v", summary)
	}
	if summary.ParentID != nil {
		t.Fatalf("summary parentId = %s, want nil", *summary.ParentID)
	}
	if summary.FromID != oldLeaf {
		t.Fatalf("summary fromId = %s, want %s", summary.FromID, oldLeaf)
	}
	if got := treeLeaf(sess); got != summary.ID {
		t.Fatalf("leaf = %s, want summary %s", got, summary.ID)
	}
}

func TestNavigateTreeSummaryAttachesToNestedUserParent(t *testing.T) {
	sess := newTreeTestSession(t)
	sess.completer = &fakeCompleter{summary: "summary"}
	appendTreeUser(t, sess, "Message one")
	a1 := appendTreeAssistant(t, sess, "a1")
	u2 := appendTreeUser(t, sess, "Message two")
	appendTreeAssistant(t, sess, "a2")
	appendTreeUser(t, sess, "Message three")
	appendTreeAssistant(t, sess, "a3")

	res, err := sess.NavigateTree(context.Background(), u2, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.EditorText != "Message two" || res.SummaryEntry == nil {
		t.Fatalf("result = %+v", res)
	}
	if res.SummaryEntry.ParentID == nil || *res.SummaryEntry.ParentID != a1 {
		t.Fatalf("summary parentId = %v, want %s", res.SummaryEntry.ParentID, a1)
	}
	var childTypes []string
	for _, entry := range sess.inner.Entries() {
		if entry.Base.ParentID != nil && *entry.Base.ParentID == a1 {
			childTypes = append(childTypes, entry.Base.Type)
		}
	}
	if len(childTypes) != 2 || !slices.Contains(childTypes, "branch_summary") || !slices.Contains(childTypes, "message") {
		t.Fatalf("children of a1 = %v, want message and branch_summary", childTypes)
	}
}

func TestNavigateTreeSummaryAttachesToAssistantTarget(t *testing.T) {
	sess := newTreeTestSession(t)
	sess.completer = &fakeCompleter{summary: "summary"}
	appendTreeUser(t, sess, "Hello")
	a1 := appendTreeAssistant(t, sess, "a1")
	appendTreeUser(t, sess, "Goodbye")
	appendTreeAssistant(t, sess, "a2")

	res, err := sess.NavigateTree(context.Background(), a1, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.EditorText != "" || res.SummaryEntry == nil {
		t.Fatalf("result = %+v", res)
	}
	if res.SummaryEntry.ParentID == nil || *res.SummaryEntry.ParentID != a1 {
		t.Fatalf("summary parentId = %v, want %s", res.SummaryEntry.ParentID, a1)
	}
	if got := treeLeaf(sess); got != res.SummaryEntry.ID {
		t.Fatalf("leaf = %s, want summary %s", got, res.SummaryEntry.ID)
	}
}

func TestNavigateTreeWithoutSummarizeCreatesNoEntries(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := &fakeCompleter{summary: "must not run"}
	sess.completer = completer
	first := appendTreeUser(t, sess, "First")
	appendTreeAssistant(t, sess, "a1")
	appendTreeUser(t, sess, "Second")
	appendTreeAssistant(t, sess, "a2")
	before := len(sess.inner.Entries())

	res, err := sess.NavigateTree(context.Background(), first, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.SummaryEntry != nil || completer.called.Load() {
		t.Fatalf("summary created without summarize: %+v", res)
	}
	if got := len(sess.inner.Entries()); got != before {
		t.Fatalf("entries = %d, want %d", got, before)
	}
}

func TestNavigateTreeBetweenBranchesSummarizesLeftBranch(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := &promptCapturingCompleter{summary: "left branch"}
	sess.completer = completer
	appendTreeUser(t, sess, "Main branch start")
	a1 := appendTreeAssistant(t, sess, "a1")
	u2 := appendTreeUser(t, sess, "Main branch continue")
	appendTreeAssistant(t, sess, "a2")
	if err := sess.inner.Fork(a1); err != nil {
		t.Fatal(err)
	}
	appendTreeUser(t, sess, "Branch path")
	appendTreeAssistant(t, sess, "a3")

	res, err := sess.NavigateTree(context.Background(), u2, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.Cancelled || res.EditorText != "Main branch continue" || res.SummaryEntry == nil || res.SummaryEntry.Summary == "" {
		t.Fatalf("result = %+v", res)
	}
	if len(completer.prompts) != 1 || !strings.Contains(completer.prompts[0], "Branch path") || strings.Contains(completer.prompts[0], "Main branch continue") {
		t.Fatalf("summary prompt did not cover exactly the branch being left: %q", completer.prompts)
	}
}

// Upstream "should handle abort during summarization": aborting returns
// { cancelled: true, aborted: true } and leaves the session unchanged.
func TestNavigateTreeAbortDuringSummarization(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := newBlockingCompactionCompleter()
	sess.completer = completer
	target := appendTreeUser(t, sess, "Tell me about something")
	appendTreeAssistant(t, sess, "a1")
	appendTreeUser(t, sess, "Continue")
	leafBefore := appendTreeAssistant(t, sess, "a2")
	entriesBefore := len(sess.inner.Entries())

	type outcome struct {
		res NavigateTreeResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
		done <- outcome{res, err}
	}()
	<-completer.started
	if !sess.IsCompacting() {
		t.Fatal("IsCompacting = false during branch summarization")
	}
	sess.AbortBranchSummary()
	got := <-done

	if got.err != nil {
		t.Fatalf("NavigateTree: %v", got.err)
	}
	if !got.res.Cancelled || !got.res.Aborted || got.res.SummaryEntry != nil {
		t.Fatalf("result = %+v, want cancelled and aborted without summary", got.res)
	}
	if n := len(sess.inner.Entries()); n != entriesBefore {
		t.Fatalf("entries = %d, want %d", n, entriesBefore)
	}
	if leaf := treeLeaf(sess); leaf != leafBefore {
		t.Fatalf("leaf = %s, want %s", leaf, leafBefore)
	}
}

func withTreeHandlers(sess *Session, t *testing.T, handlers map[string][]extension.HandlerFn) {
	t.Helper()
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Handlers: handlers}}, t.TempDir()))
}

// Upstream emits session_before_tree from navigateTree with the full
// TreePreparation and the branch summary abort signal.
func TestNavigateTreeEmitsSessionBeforeTreeWithPreparation(t *testing.T) {
	sess := newTreeTestSession(t)
	sess.completer = &fakeCompleter{summary: "summary"}
	appendTreeUser(t, sess, "hello")
	common := appendTreeAssistant(t, sess, "hi")
	target := appendTreeUser(t, sess, "target")
	if err := sess.inner.Fork(common); err != nil {
		t.Fatal(err)
	}
	abandonedUser := appendTreeUser(t, sess, "abandoned")
	oldLeaf := appendTreeAssistant(t, sess, "abandoned reply")

	var events []extension.SessionBeforeTreeEvent
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(args ...any) (any, error) {
			events = append(events, args[0].(extension.SessionBeforeTreeEvent))
			return nil, nil
		}},
	})

	if _, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true, CustomInstructions: "focus", Label: "tag"}); err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("session_before_tree emitted %d times, want 1", len(events))
	}
	event := events[0]
	if event.Type != "session_before_tree" || event.Signal == nil {
		t.Fatalf("event = %+v", event)
	}
	preparation, ok := event.Preparation.(*TreePreparation)
	if !ok {
		t.Fatalf("preparation = %T, want *TreePreparation", event.Preparation)
	}
	var summarized []string
	for _, entry := range preparation.EntriesToSummarize {
		summarized = append(summarized, entry.Base.ID)
	}
	if preparation.TargetID != target || preparation.OldLeafID == nil || *preparation.OldLeafID != oldLeaf ||
		preparation.CommonAncestorID == nil || *preparation.CommonAncestorID != common ||
		!slices.Equal(summarized, []string{abandonedUser, oldLeaf}) || !preparation.UserWantsSummary ||
		preparation.CustomInstructions != "focus" || preparation.ReplaceInstructions || preparation.Label != "tag" {
		t.Fatalf("preparation = %+v (entries %v)", preparation, summarized)
	}
	encoded, err := json.Marshal(preparation)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"targetId", "oldLeafId", "commonAncestorId", "entriesToSummarize", "userWantsSummary", "customInstructions", "label"} {
		if _, ok := wire[key]; !ok {
			t.Errorf("preparation JSON lacks %q: %s", key, encoded)
		}
	}
}

// Port of regression 3688: a cancelling session_before_tree handler returns
// { cancelled: true } and clears the branch summary state.
func TestNavigateTreeSessionBeforeTreeCancel(t *testing.T) {
	sess := newTreeTestSession(t)
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(...any) (any, error) {
			return extension.SessionBeforeTreeResult{Cancel: true}, nil
		}},
	})
	target := appendTreeUser(t, sess, "first")
	appendTreeAssistant(t, sess, "reply")
	current := appendTreeUser(t, sess, "second")

	res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res != (NavigateTreeResult{Cancelled: true}) {
		t.Fatalf("result = %+v, want cancelled", res)
	}
	if sess.IsCompacting() {
		t.Fatal("IsCompacting = true after cancelled navigation")
	}
	if got := treeLeaf(sess); got != current {
		t.Fatalf("leaf = %s, want %s", got, current)
	}
}

// Port of branch-summary-extensions.test.ts: an extension-provided summary
// replaces the summarizer, is marked fromHook, and its usage counts in the
// session totals.
func TestNavigateTreeExtensionSummary(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := &fakeCompleter{summary: "must not run"}
	sess.completer = completer
	usage := map[string]any{
		"input": 10, "output": 20, "cacheRead": 30, "cacheWrite": 40, "totalTokens": 100,
		"cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1},
	}
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(...any) (any, error) {
			return extension.SessionBeforeTreeResult{Summary: &extension.SessionBeforeTreeResultSummary{
				Summary: "Summary provided by extension",
				Details: map[string]any{"source": "extension"},
				Usage:   usage,
			}}, nil
		}},
	})
	if err := sess.inner.SetLeafID(nil); err != nil {
		t.Fatal(err)
	}
	target := appendTreeUser(t, sess, "first branch")
	appendTreeAssistant(t, sess, "first reply")
	appendTreeUser(t, sess, "abandoned branch work")
	source := appendTreeAssistant(t, sess, "abandoned reply")

	res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	summary := res.SummaryEntry
	if summary == nil || summary.Type != "branch_summary" || summary.ParentID != nil || summary.FromID != source ||
		!summary.FromHook || summary.Summary != "Summary provided by extension" {
		t.Fatalf("summary entry = %+v", summary)
	}
	wantUsage := ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1}}
	if summary.Usage == nil || *summary.Usage != wantUsage {
		t.Fatalf("summary usage = %+v, want %+v", summary.Usage, wantUsage)
	}
	if details, ok := summary.Details.(map[string]any); !ok || details["source"] != "extension" {
		t.Fatalf("summary details = %#v", summary.Details)
	}
	if completer.called.Load() {
		t.Fatal("extension summary still ran the summarizer")
	}
	stats := sess.GetSessionStats()
	if stats.Tokens != (SessionStatsTokens{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, Total: 100}) || stats.Cost != 1 {
		t.Fatalf("stats tokens = %+v cost = %v", stats.Tokens, stats.Cost)
	}
}

// Upstream ignores an extension summary when the user did not ask to
// summarize.
func TestNavigateTreeExtensionSummaryIgnoredWithoutSummarize(t *testing.T) {
	sess := newTreeTestSession(t)
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(...any) (any, error) {
			return extension.SessionBeforeTreeResult{Summary: &extension.SessionBeforeTreeResultSummary{Summary: "unused"}}, nil
		}},
	})
	appendTreeUser(t, sess, "hello")
	target := appendTreeAssistant(t, sess, "hi")
	appendTreeUser(t, sess, "abandoned")

	res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.SummaryEntry != nil || treeLeaf(sess) != target {
		t.Fatalf("result = %+v leaf = %s, want plain navigation to %s", res, treeLeaf(sess), target)
	}
}

// Upstream lets session_before_tree override customInstructions,
// replaceInstructions and label. A subprocess extension's result arrives as
// raw JSON.
func TestNavigateTreeSessionBeforeTreeOverrides(t *testing.T) {
	sess := newTreeTestSession(t)
	completer := &promptCapturingCompleter{summary: "summary"}
	sess.completer = completer
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(...any) (any, error) {
			return json.RawMessage(`{"customInstructions":"EXTENSION ONLY","replaceInstructions":true,"label":"from-extension"}`), nil
		}},
	})
	appendTreeUser(t, sess, "hello")
	target := appendTreeAssistant(t, sess, "hi")
	appendTreeUser(t, sess, "abandoned")

	res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true, CustomInstructions: "user focus", Label: "user-label"})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if len(completer.prompts) != 1 || !strings.HasSuffix(completer.prompts[0], "</conversation>\n\nEXTENSION ONLY") {
		t.Fatalf("summary prompt = %q, want extension replacement instructions", completer.prompts)
	}
	if res.SummaryEntry == nil {
		t.Fatal("summary entry missing")
	}
	if got := treeLabelTargets(t, sess); got[res.SummaryEntry.ID] != "from-extension" || len(got) != 1 {
		t.Fatalf("labels = %v, want summary labelled from-extension", got)
	}
}

// Upstream emits session_tree after every completed navigation with the new
// and old leaf, and the summary entry plus fromExtension when a summary was
// created.
func TestNavigateTreeEmitsSessionTree(t *testing.T) {
	type treeEvent struct {
		newLeaf, oldLeaf string
		summaryID        string
		fromExtension    bool
	}
	leafString := func(id *string) string {
		if id == nil {
			return "<nil>"
		}
		return *id
	}
	record := func(events *[]treeEvent) extension.HandlerFn {
		return func(args ...any) (any, error) {
			event := args[0].(extension.SessionTreeEvent)
			got := treeEvent{newLeaf: leafString(event.NewLeafID), oldLeaf: leafString(event.OldLeafID), fromExtension: event.FromExtension}
			if event.SummaryEntry != nil {
				entry, ok := event.SummaryEntry.(icodingagent.SessionEntry)
				if !ok || entry.Base.Type != "branch_summary" {
					t.Errorf("summaryEntry = %#v", event.SummaryEntry)
				}
				got.summaryID = entry.Base.ID
			}
			*events = append(*events, got)
			return nil, nil
		}
	}

	t.Run("plain navigation", func(t *testing.T) {
		sess := newTreeTestSession(t)
		var events []treeEvent
		// The second handler re-enters the session, as upstream handlers may.
		withTreeHandlers(sess, t, map[string][]extension.HandlerFn{"session_tree": {record(&events), func(...any) (any, error) {
			sess.RefreshContext()
			return nil, nil
		}}})
		appendTreeUser(t, sess, "hello")
		target := appendTreeAssistant(t, sess, "hi")
		oldLeaf := appendTreeUser(t, sess, "abandoned")

		done := make(chan error, 1)
		go func() {
			_, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{})
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("NavigateTree: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("NavigateTree blocked while a session_tree handler re-entered the session")
		}
		if want := []treeEvent{{newLeaf: target, oldLeaf: oldLeaf}}; !slices.Equal(events, want) {
			t.Fatalf("session_tree events = %+v, want %+v", events, want)
		}
	})

	t.Run("extension summary", func(t *testing.T) {
		sess := newTreeTestSession(t)
		var events []treeEvent
		withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
			"session_before_tree": {func(...any) (any, error) {
				return extension.SessionBeforeTreeResult{Summary: &extension.SessionBeforeTreeResultSummary{Summary: "from extension"}}, nil
			}},
			"session_tree": {record(&events)},
		})
		appendTreeUser(t, sess, "hello")
		target := appendTreeAssistant(t, sess, "hi")
		oldLeaf := appendTreeUser(t, sess, "abandoned")

		res, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
		if err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if res.SummaryEntry == nil {
			t.Fatal("summary entry missing")
		}
		want := []treeEvent{{newLeaf: res.SummaryEntry.ID, oldLeaf: oldLeaf, summaryID: res.SummaryEntry.ID, fromExtension: true}}
		if !slices.Equal(events, want) {
			t.Fatalf("session_tree events = %+v, want %+v", events, want)
		}
	})

	t.Run("no-op and cancel do not emit", func(t *testing.T) {
		sess := newTreeTestSession(t)
		var events []treeEvent
		withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
			"session_before_tree": {func(...any) (any, error) { return extension.SessionBeforeTreeResult{Cancel: true}, nil }},
			"session_tree":        {record(&events)},
		})
		target := appendTreeUser(t, sess, "hello")
		leaf := appendTreeAssistant(t, sess, "hi")
		for _, id := range []string{leaf, target} {
			if _, err := sess.NavigateTree(context.Background(), id, NavigateTreeOptions{}); err != nil {
				t.Fatalf("NavigateTree(%s): %v", id, err)
			}
		}
		if len(events) != 0 {
			t.Fatalf("session_tree events = %+v, want none", events)
		}
	})
}

// Port of regression 9178 "rejects a second navigation while the first is
// waiting": the first navigation is held inside its session_before_tree
// handler, and a second navigation is rejected without moving the leaf.
func TestNavigateTreeRejectsSecondNavigationDuringSessionBeforeTree(t *testing.T) {
	sess := newTreeTestSession(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_tree": {func(...any) (any, error) {
			once.Do(func() { close(started) })
			<-release
			return nil, nil
		}},
	})
	secondTarget := appendTreeUser(t, sess, "first user")
	firstTarget := appendTreeAssistant(t, sess, "first assistant")
	appendTreeUser(t, sess, "second user")
	originalLeaf := appendTreeAssistant(t, sess, "second assistant")

	firstDone := make(chan error, 1)
	go func() {
		_, err := sess.NavigateTree(context.Background(), firstTarget, NavigateTreeOptions{})
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("session_before_tree was not emitted")
	}

	_, secondErr := sess.NavigateTree(context.Background(), secondTarget, NavigateTreeOptions{})
	leafWhileWaiting := treeLeaf(sess)
	close(release)
	if secondErr == nil || secondErr.Error() != treeNavigationInProgressError {
		t.Fatalf("second NavigateTree error = %v", secondErr)
	}
	if leafWhileWaiting != originalLeaf {
		t.Fatalf("leaf = %s while the first navigation waits, want %s", leafWhileWaiting, originalLeaf)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first NavigateTree: %v", err)
	}
	if got := treeLeaf(sess); got != firstTarget {
		t.Fatalf("leaf = %s, want %s", got, firstTarget)
	}
}
