// Component behavior on a virtual terminal. The message and tool-card cases are ported from p2's tui-render.test.ts; the overlays,
// selectors and inputs have no test of their own in p2 (its tests drive them through the runtime), so these pin their rendering and keys.
//
// pi: apps/p2/test/p2/tui-render.test.ts
package components

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func assistantMessage(stop ai.StopReason, errText string, content ...ai.AssistantContentBlock) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: content, API: "openai-completions", Provider: "faux", Model: "faux-1", StopReason: stop, ErrorMessage: errText, Timestamp: 1}
}

func TestAssistantMessageComponents(t *testing.T) {
	ctx := newTestContext(t)
	message := assistantMessage(ai.StopReasonStop, "",
		ai.ThinkingContent{Thinking: "private reasoning"},
		ai.TextContent{Text: "**Answer** with `code`\x1b]52;c;evil\x07"})
	hidden := NewAssistantMessageComponent(ctx, message, true, "")
	out := plain(hidden.Render(60))
	contains(t, out, "Answer with code")
	contains(t, out, "Thinking...")
	lacks(t, out, "private reasoning")
	lacks(t, strings.Join(hidden.Render(60), ""), "52;c")
	contains(t, plain(NewAssistantMessageComponent(ctx, message, false, "").Render(60)), "private reasoning")
	failed := *message
	failed.StopReason, failed.ErrorMessage = ai.StopReasonError, "fixture error"
	contains(t, plain(NewAssistantMessageComponent(ctx, &failed, false, "").Render(60)), "Error: fixture error")
	aborted := *message
	aborted.Content, aborted.StopReason = nil, ai.StopReasonAborted
	contains(t, plain(NewAssistantMessageComponent(ctx, &aborted, false, "").Render(60)), "Operation aborted")
	truncated := *message
	truncated.StopReason = ai.StopReasonLength
	contains(t, plain(NewAssistantMessageComponent(ctx, &truncated, false, "").Render(60)), "Response was truncated before completion.")
	if got := render.SafeText("safe\x1b]52;c;evil\x07\x1b[2J\x00 text"); got != "safe text" {
		t.Fatalf("SafeText = %q", got)
	}
}

func TestNoticeAndCompactionSummary(t *testing.T) {
	ctx := newTestContext(t)
	for _, width := range []int{20, 60} {
		notice := NewNoticeComponent("The model could not answer.", "error", ctx).Render(width)
		assertWithin(t, notice, width)
		contains(t, plain(notice), "The model")
	}
	summary := NewCompactionSummaryComponent("Summary of earlier work", false, ctx, CompactionSummaryOptions{})
	collapsed := plain(summary.Render(60))
	lacks(t, collapsed, "Summary of earlier work")
	summary.SetExpanded(true)
	contains(t, plain(summary.Render(60)), "Summary of earlier work")
}

func TestUserMessageComponent(t *testing.T) {
	ctx := newTestContext(t)
	c := NewUserMessageComponent(transcript.UserItem{Text: "hello **there**"}, ctx)
	out := c.Render(40)
	assertWithin(t, out, 40)
	contains(t, plain(out), "hello there")
}

func TestToolCardGenericFallbackAndThemeSwap(t *testing.T) {
	ctx := newTestContext(t)
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = "output-" + string(rune('a'+i))
	}
	tool := transcript.ToolView{ID: "call", Name: "mcp_thing", Status: "done", Output: strings.Join(lines, "\n")}
	card := NewToolExecutionComponent(tool, nil, ctx, true)
	out := plain(card.Render(80))
	contains(t, out, "mcp_thing")
	contains(t, out, "5 more lines, ctrl+o to expand")
	lacks(t, out, "output-o")
	assertWithin(t, card.Render(80), 80)
	card.SetExpanded(true)
	contains(t, plain(card.Render(80)), "output-o")
	before := strings.Join(card.Render(80), "\n")
	light, _ := lightTheme(t)
	ctx.Theme().Set(light)
	if strings.Join(card.Render(80), "\n") == before {
		t.Fatal("theme swap did not change the card (baked colors)")
	}
}

func TestBashExecution(t *testing.T) {
	ctx := newTestContext(t)
	screen, _ := newScreen(t, 80, 24)
	c := NewBashExecutionComponent("echo hi", ctx, BashExecutionOptions{TUI: screen})
	defer c.Dispose()
	running := plain(c.Render(60))
	contains(t, running, "$ echo hi")
	contains(t, running, "Running...")
	var out []string
	for i := range 30 {
		out = append(out, "line-"+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	c.SetOutput(strings.Join(out, "\n"), false)
	zero := 0.0
	c.SetComplete(&zero, false, BashCompletion{})
	done := plain(c.Render(60))
	lacks(t, done, "Running...")
	contains(t, done, "more lines")
	c.SetExpanded(true)
	contains(t, plain(c.Render(60)), "line-A0")
	assertWithin(t, c.Render(60), 60)
	code := 2.0
	failed := NewBashExecutionComponent("false", ctx, BashExecutionOptions{})
	failed.SetComplete(&code, false, BashCompletion{})
	contains(t, plain(failed.Render(60)), "(exit 2)")
	cancelled := NewBashExecutionComponent("sleep 9", ctx, BashExecutionOptions{})
	cancelled.SetComplete(nil, true, BashCompletion{})
	contains(t, plain(cancelled.Render(60)), "(cancelled)")
}

func TestSkillInvocation(t *testing.T) {
	ctx := newTestContext(t)
	block := ParseSkillBlock("<skill name=\"deploy\" location=\"/s/deploy.md\">\nStep one\nStep two\n</skill>\n\nplease go")
	if block == nil || block.Name != "deploy" || block.UserMessage != "please go" {
		t.Fatalf("parse = %+v", block)
	}
	if ParseSkillBlock("not a skill") != nil {
		t.Fatal("parsed non-skill")
	}
	c := NewSkillInvocationComponent(*block, ctx, 1)
	collapsed := plain(c.Render(60))
	contains(t, collapsed, "deploy")
	lacks(t, collapsed, "Step two")
	c.SetExpanded(true)
	contains(t, plain(c.Render(60)), "Step two")
	assertWithin(t, c.Render(30), 30)
}

func TestSecretInput(t *testing.T) {
	ctx := newTestContext(t)
	in := NewSecretInput(ctx)
	var got string
	in.OnSubmit = func(v string) { got = v }
	for _, k := range []string{"a", "b", "c"} {
		in.HandleInput(k)
	}
	if in.Length() != 3 {
		t.Fatalf("length = %d", in.Length())
	}
	lacks(t, plain(in.Render(40)), "abc")
	in.HandleInput("\x1b[200~x\ny\x1b[201~")
	if in.Length() != 5 {
		t.Fatalf("paste length = %d (line breaks must be dropped)", in.Length())
	}
	in.HandleInput(keyEnter)
	if got != "abcxy" || in.Length() != 0 {
		t.Fatalf("submitted %q, remaining %d", got, in.Length())
	}
}

func listOverlay(t *testing.T, opts ListOverlayOptions) *ListOverlay {
	t.Helper()
	return NewListOverlay(newTestContext(t), opts)
}

func TestListOverlayFilterSelectAndActions(t *testing.T) {
	rows := []ListRow{{Key: "a", Label: "alpha"}, {Key: "b", Label: "beta", Detail: "d"}, {Key: "c", Label: "gamma"}}
	var selected, acted string
	closed := false
	o := listOverlay(t, ListOverlayOptions{
		Title: func() string { return "Pick" }, Rows: func() []ListRow { return rows },
		OnSelect: func(r ListRow) { selected = r.Key }, OnClose: func() { closed = true },
		Actions: []ListAction{{Key: "ctrl+t", Label: "tag", Run: func(r *ListRow) { acted = r.Key }}},
	})
	out := plain(o.Render(50))
	contains(t, out, "Pick")
	contains(t, out, "alpha")
	assertWithin(t, o.Render(50), 50)
	o.HandleInput(keyDown)
	o.HandleInput(keyEnter)
	if selected != "b" {
		t.Fatalf("selected %q", selected)
	}
	o.HandleInput("\x14")
	if acted != "b" {
		t.Fatalf("action ran on %q", acted)
	}
	for _, ch := range "gam" {
		o.HandleInput(string(ch))
	}
	filtered := plain(o.Render(50))
	contains(t, filtered, "gamma")
	lacks(t, filtered, "alpha")
	o.HandleInput(keyEsc)
	if !closed {
		t.Fatal("Escape did not close")
	}
}

func TestConfirmOverlayAndConfirm(t *testing.T) {
	ctx := newTestContext(t)
	var answers []bool
	o := NewConfirmOverlay(ctx, []string{"Delete it?", "cannot be undone"}, func(yes bool) { answers = append(answers, yes) })
	out := plain(o.Render(50))
	contains(t, out, "Delete it?")
	contains(t, out, "confirm")
	o.HandleInput("y")
	o.HandleInput(keyEnter)
	o.HandleInput("n")
	o.HandleInput(keyEsc)
	o.HandleInput("x")
	want := []bool{true, true, false, false}
	if len(answers) != len(want) {
		t.Fatalf("answers = %v", answers)
	}
	for i := range want {
		if answers[i] != want[i] {
			t.Fatalf("answers = %v, want %v", answers, want)
		}
	}
}

func TestTextViewerScrolls(t *testing.T) {
	ctx := newTestContext(t)
	var lines []string
	for i := range 50 {
		lines = append(lines, "row "+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	closed := false
	v := NewTextViewer(ctx, TextViewerOptions{Title: "File", Lines: func(int) []string { return lines }, OnClose: func() { closed = true }, Height: 5})
	first := plain(v.Render(40))
	contains(t, first, "File")
	contains(t, first, "row Aa")
	lacks(t, first, "row Fa")
	for range 3 {
		v.HandleInput("\x1b[6~")
	}
	if plain(v.Render(40)) == first {
		t.Fatal("page down did not scroll")
	}
	v.HandleInput(keyEsc)
	if !closed {
		t.Fatal("Escape did not close")
	}
}

var models = []SelectorModel{{"anthropic", "sonnet", "Sonnet"}, {"openai", "gpt", "GPT"}, {"faux", "faux-1", "Faux"}}

func TestModelSelector(t *testing.T) {
	ctx := newTestContext(t)
	var chosen, saved SelectorModel
	cancelled := false
	c := NewModelSelector(ctx, ModelSelectorOptions{
		Models: models, Current: &ModelRef{"openai", "gpt"}, Title: "Model",
		OnSelect: func(m SelectorModel) { chosen = m }, OnSelectAsDefault: func(m SelectorModel) { saved = m }, OnCancel: func() { cancelled = true },
	})
	out := plain(c.Render(80))
	contains(t, out, "sonnet")
	contains(t, out, "gpt")
	assertWithin(t, c.Render(80), 80)
	c.HandleInput(keyEnter)
	if chosen.ModelID != "gpt" {
		t.Fatalf("Enter chose %+v (the current model starts selected)", chosen)
	}
	c.HandleInput(keyDown)
	c.HandleInput("\x13")
	if saved.ModelID != "sonnet" {
		t.Fatalf("save chose %+v", saved)
	}
	c.HandleInput(keyEsc)
	if !cancelled {
		t.Fatal("not cancelled")
	}
}

func TestThinkingSelector(t *testing.T) {
	ctx := newTestContext(t)
	var level string
	c := NewThinkingSelector(ctx, ThinkingSelectorOptions{Current: "off", Levels: []string{"off", "low", "high"}, OnSelect: func(l string) { level = l }, OnCancel: func() {}})
	contains(t, plain(c.Render(60)), "high")
	c.HandleInput(keyDown)
	c.HandleInput(keyEnter)
	if level != "low" {
		t.Fatalf("level = %q", level)
	}
}

func TestScopedModelsSelector(t *testing.T) {
	ctx := newTestContext(t)
	var saved []string
	c := NewScopedModelsSelector(ctx, ScopedModelsSelectorOptions{Models: models, Enabled: []ModelRef{{"faux", "faux-1"}}, OnSave: func(e []string) { saved = e }, OnCancel: func() {}})
	contains(t, plain(c.Render(80)), "sonnet")
	c.HandleInput(keyEnter) // toggles the first model on
	c.HandleInput("\x13")
	if len(saved) != 2 {
		t.Fatalf("saved = %v", saved)
	}
}

func TestTreeSelector(t *testing.T) {
	ctx := newTestContext(t)
	root := 1
	two := 2
	nodes := []TreeSelectorNode{
		{Entry: 1, Conversation: 1, Role: "user", Text: "first question"},
		{Entry: 2, Parent: &root, Conversation: 1, Role: "assistant", Text: "first answer"},
		{Entry: 3, Parent: &two, Conversation: 1, Role: "user", Text: "branch a"},
		{Entry: 4, Parent: &two, Conversation: 2, Role: "user", Text: "branch b"},
	}
	var picked int
	c := NewTreeSelector(ctx, nodes, &two, nil, func(e int) { picked = e }, func() {}, func(int, string) {})
	out := plain(c.Render(70))
	contains(t, out, "first question")
	contains(t, out, "branch a")
	contains(t, out, "branch b")
	assertWithin(t, c.Render(70), 70)
	c.HandleInput(keyUp)
	c.HandleInput(keyEnter)
	if picked == 0 {
		t.Fatal("Enter selected nothing")
	}
}

func TestUserMessageSelector(t *testing.T) {
	ctx := newTestContext(t)
	var id int
	cancelled := false
	c := NewUserMessageSelector(ctx, []UserMessageItem{{1, "one"}, {2, "two"}, {3, "three"}}, func(e int) { id = e }, func() { cancelled = true }, nil)
	contains(t, plain(c.Render(60)), "three")
	c.HandleInput(keyUp)
	c.HandleInput(keyEnter)
	if id != 2 {
		t.Fatalf("selected %d (the newest starts selected)", id)
	}
	c.HandleInput(keyEsc)
	if !cancelled {
		t.Fatal("not cancelled")
	}
}

var _ pitui.Component = (*ListOverlay)(nil)

type fakeHandle struct{ closed bool }

func (h *fakeHandle) Close() { h.closed = true }

type fakeHost struct {
	*testContext
	shown   pitui.Component
	handle  *fakeHandle
	overlay string
}

func (h *fakeHost) Overlay(id string, c pitui.Component, _ pitui.OverlaySpec) OverlayHandle {
	h.overlay, h.shown, h.handle = id, c, &fakeHandle{}
	return h.handle
}

func TestConfirmClosesItsOverlayThenAnswers(t *testing.T) {
	host := &fakeHost{testContext: newTestContext(t)}
	var answers []bool
	Confirm(host, []string{"Sure?"}, func(yes bool) {
		if !host.handle.closed {
			t.Error("answered before the overlay closed")
		}
		answers = append(answers, yes)
	})
	if host.overlay != "confirm" {
		t.Fatalf("overlay id %q", host.overlay)
	}
	host.shown.(*ConfirmOverlay).HandleInput("y")
	if len(answers) != 1 || !answers[0] {
		t.Fatalf("answers = %v", answers)
	}
}

func FuzzParseSkillBlock(f *testing.F) {
	f.Add("<skill name=\"a\" location=\"/b\">\nbody\n</skill>\n\nuser")
	f.Add("<skill name=\"\" location=\"\">\n\n</skill>")
	f.Add("<skill name=\"a\" location=\"/b\">\n</skill>\n\n")
	f.Fuzz(func(t *testing.T, text string) {
		block := ParseSkillBlock(text)
		if block == nil {
			return
		}
		if block.Name == "" || block.Location == "" || !strings.HasPrefix(text, "<skill ") {
			t.Fatalf("invalid block %+v from %q", block, text)
		}
		// Rendering never panics and stays within the width.
		c := NewSkillInvocationComponent(*block, newTestContext(t), 1)
		c.SetExpanded(true)
		assertWithin(t, c.Render(40), 40)
	})
}
