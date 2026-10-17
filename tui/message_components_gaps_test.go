package tui

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func withExpandKeyText(t *testing.T, text string) {
	t.Helper()
	SetAppKeyTextResolver(func(action string) string {
		if action == "app.tools.expand" {
			return text
		}
		return ""
	})
	t.Cleanup(func() { SetAppKeyTextResolver(nil) })
}

// branch-summary-message.ts / skill-invocation-message.ts: the collapsed line names the expand key the user configured, colors the label, the text and the dim key hint with their theme tokens, closes the bold label with the bold-off code, and both components recolor on a theme change when invalidated.
// Pi: packages/coding-agent/src/modes/interactive/components/branch-summary-message.ts, skill-invocation-message.ts.
func TestSummaryMessageCollapsedLinesUseThemeTokensAndTheConfiguredExpandKey(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")
	withExpandKeyText(t, "ctrl+e")
	theme := ActiveTheme()

	branch := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "s"}, nil, 1)
	lines := branch.Render(80)
	wantLabel := theme.Fg("customMessageLabel", "\x1b[1m[branch]\x1b[22m")
	wantHint := theme.Fg("customMessageText", "Branch summary (") + theme.Fg("dim", "ctrl+e") + theme.Fg("customMessageText", " to expand)")
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, wantLabel) }) {
		t.Errorf("branch label %q not in %q", wantLabel, lines)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, wantHint) }) {
		t.Errorf("branch hint %q not in %q", wantHint, lines)
	}

	skill := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "pdf", Content: "c"}, nil, 1)
	skillLines := skill.Render(80)
	wantSkill := theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") + theme.Fg("customMessageText", "pdf") + theme.Fg("dim", " (ctrl+e to expand)")
	if !slices.ContainsFunc(skillLines, func(l string) bool { return strings.Contains(l, wantSkill) }) {
		t.Errorf("skill line %q not in %q", wantSkill, skillLines)
	}

	// A theme change recolors both after Invalidate.
	_ = branch.Render(80)
	_ = skill.Render(80)
	SetTheme("light")
	branch.Invalidate()
	skill.Invalidate()
	freshBranch := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "s"}, nil, 1).Render(80)
	freshSkill := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "pdf", Content: "c"}, nil, 1).Render(80)
	if got := branch.Render(80); !slices.Equal(got, freshBranch) {
		t.Errorf("branch summary after a theme change\n got %q\nwant %q", got, freshBranch)
	}
	if got := skill.Render(80); !slices.Equal(got, freshSkill) {
		t.Errorf("skill message after a theme change\n got %q\nwant %q", got, freshSkill)
	}
}

type liveGapComponent struct {
	text  string
	dirty bool
}

func (c *liveGapComponent) Render(int) []string { return []string{c.text} }
func (c *liveGapComponent) Invalidate()         {}
func (c *liveGapComponent) IsDirty() bool       { return c.dirty }

type staticGapComponent struct{ text string }

func (c *staticGapComponent) Render(int) []string { return []string{c.text} }
func (c *staticGapComponent) Invalidate()         {}

// custom-message.ts: the default rendering is a spacer, then a box padded by the output pad with the bold label, a spacer and the text; a custom renderer replaces the box, is rebuilt (the previous component removed) on expand/pad/invalidate and falls back to the box when it throws; the message reports itself live and dirty through a custom component that does.
// Pi: packages/coding-agent/src/modes/interactive/components/custom-message.ts.
func TestCustomMessageComponentLayoutRendererLifecycleAndLiveness(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")

	def := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, nil, nil, 3)
	lines := def.Render(20)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = strings.TrimRight(stripANSI(line), " ")
	}
	if want := []string{"", "", "   [note]", "", "   body", ""}; !slices.Equal(plain, want) {
		t.Fatalf("default layout = %q, want %q", plain, want)
	}
	if want := ActiveTheme().Fg("customMessageLabel", "\x1b[1m[note]\x1b[22m"); !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
		t.Errorf("label %q not in %q", want, lines)
	}
	if def.SurfaceLive() {
		t.Error("a message with the default box reports itself live")
	}

	version := 0
	var renderedWith []MessageRenderOptions
	custom := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, func(_ *CustomMessage, options MessageRenderOptions) Component {
		version++
		renderedWith = append(renderedWith, options)
		return &staticGapComponent{text: "custom-v" + string(rune('0'+version))}
	}, nil, 2)
	if got := stripANSILines(custom.Render(20)); !slices.Equal(got, []string{"", "custom-v1"}) {
		t.Fatalf("custom render = %q", got)
	}
	custom.SetExpanded(true)
	custom.SetOutputPad(5)
	custom.Invalidate()
	got := stripANSILines(custom.Render(20))
	if !slices.Equal(got, []string{"", "custom-v4"}) {
		t.Fatalf("after expand, pad and invalidate the previous custom components must be replaced: %q", got)
	}
	if last := renderedWith[len(renderedWith)-1]; !last.Expanded || last.OutputPad != 5 {
		t.Errorf("renderer options = %+v, want expanded with pad 5", last)
	}
	if first := renderedWith[0]; first.Expanded || first.OutputPad != 2 {
		t.Errorf("first renderer options = %+v", first)
	}
	if custom.SurfaceLive() {
		t.Error("a static custom component made the message live")
	}

	live := &liveGapComponent{text: "live"}
	liveMessage := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, func(*CustomMessage, MessageRenderOptions) Component { return live }, nil, 1)
	_ = liveMessage.Render(20)
	liveMessage.NeedsRedraw()
	if !liveMessage.SurfaceLive() {
		t.Error("a dirty-reporting custom component did not make the message live")
	}
	if liveMessage.IsDirty() {
		t.Error("the message is dirty though neither it nor its custom component is")
	}
	live.dirty = true
	if !liveMessage.IsDirty() {
		t.Error("the custom component's dirty state is not reported")
	}
	live.dirty = false
	liveMessage.Invalidate()
	if !liveMessage.IsDirty() {
		t.Error("an invalidated message is not dirty")
	}

	panicking := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, func(*CustomMessage, MessageRenderOptions) Component { panic("renderer failed") }, nil, 1)
	if got := stripANSILines(panicking.Render(20)); !slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, "[note]") }) {
		t.Fatalf("a throwing renderer did not fall back to the default box: %q", got)
	}
}

// user-message.ts: setters take effect on the next render of an already rendered message, a negative output pad is clamped to zero, an async transform's completion invalidates the message and then calls the caller's own callback, images mark the message for redraw and are what a frontend reads, and dispose cancels the in-flight transform work.
// Pi: packages/coding-agent/src/modes/interactive/components/user-message.ts.
func TestUserMessageComponentSettersRerenderClampPadAndDispose(t *testing.T) {
	joined := func(b *UserMessageComponent) string { return strings.Join(stripANSILines(b.Render(40)), "\n") }

	block := NewUserMessageComponent("message", nil, 1, nil)
	_ = joined(block)
	block.SetMarkdownTransform(func(text string, _ int) string { return text + " transformed" })
	if got := joined(block); !strings.Contains(got, "message transformed") {
		t.Fatalf("SetMarkdownTransform did not reach the next render: %q", got)
	}

	suffix := "one"
	block.SetMarkdownTransform(func(text string, _ int) string { return text + " " + suffix })
	block.SetMarkdownTransformState(func() string { return suffix })
	_ = joined(block)
	suffix = "two"
	if got := joined(block); !strings.Contains(got, "message two") {
		t.Fatalf("a changed transform state did not re-render: %q", got)
	}
	_ = joined(block)
	suffix = "three"
	block.NeedsRedraw()
	block.SetMarkdownTransformState(func() string { return "other:" + suffix })
	if !block.IsDirty() {
		t.Error("SetMarkdownTransformState did not mark the message for redraw")
	}
	if got := joined(block); !strings.Contains(got, "message three") {
		t.Fatalf("SetMarkdownTransformState did not invalidate: %q", got)
	}

	// A negative output pad renders like zero, from the constructor and from SetOutputPad.
	zero := joined(NewUserMessageComponent("pad", nil, 0, nil))
	if got := joined(NewUserMessageComponent("pad", nil, -3, nil)); got != zero {
		t.Errorf("constructor with pad -3 = %q, want the pad-0 render %q", got, zero)
	}
	padded := NewUserMessageComponent("pad", nil, 2, nil)
	_ = joined(padded)
	padded.SetOutputPad(-4)
	if got := joined(padded); got != zero {
		t.Errorf("SetOutputPad(-4) = %q, want the pad-0 render %q", got, zero)
	}

	// Async transform: it applies after the first render, completion invalidates the message and calls the caller's callback.
	var workers sync.WaitGroup
	completed := 0
	cancelled, started := make(chan struct{}), make(chan struct{})
	asyncBlock := NewUserMessageComponent("raw", nil, 1, nil)
	_ = joined(asyncBlock)
	asyncBlock.NeedsRedraw()
	asyncBlock.SetAsyncMarkdownTransform(&AsyncMarkdownTransform{
		Context: t.Context(), Start: workers.Go,
		Invalidate: func() { completed++ },
		Prepare: func(string, int) func(context.Context) string {
			return func(context.Context) string { return "async-done" }
		},
	})
	if !asyncBlock.IsDirty() {
		t.Error("SetAsyncMarkdownTransform did not mark the message for redraw")
	}
	_ = joined(asyncBlock)
	workers.Wait()
	if got := joined(asyncBlock); !strings.Contains(got, "async-done") {
		t.Fatalf("an async transform set after the first render was not applied: %q", got)
	}
	if completed == 0 {
		t.Error("the caller's invalidate callback was not called when the async transform completed")
	}
	asyncBlock.SetAsyncMarkdownTransform(nil)

	// Images mark the message dirty and reach the frontend decoding.
	imaged := NewUserMessageComponent("pic", nil, 1, nil)
	_ = joined(imaged)
	imaged.NeedsRedraw()
	imaged.SetImages([]ImageBlock{{Data: "aGk=", MIMEType: "image/png"}})
	if !imaged.IsDirty() {
		t.Error("SetImages did not mark the message for redraw")
	}
	if got := imaged.frontendImages(); len(got) != 1 {
		t.Errorf("frontend images = %d, want 1", len(got))
	}

	// Dispose cancels the in-flight transform.
	parent, stop := context.WithCancel(t.Context())
	defer stop()
	disposing := NewUserMessageComponent("raw", nil, 1, nil)
	disposing.SetAsyncMarkdownTransform(&AsyncMarkdownTransform{
		Context: parent, Start: func(run func()) { go run() },
		Prepare: func(string, int) func(context.Context) string {
			return func(ctx context.Context) string {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return ""
			}
		},
	})
	_ = joined(disposing)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the async transform never started")
	}
	disposing.Dispose()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispose did not cancel the in-flight async transform")
	}
}
