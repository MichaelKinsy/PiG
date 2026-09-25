package tui

import (
	"io"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.84 TuiBase.hideOverlay removes overlayStack[overlayStack.length-1],
// while OverlayHandle.focus only increments focusOrder. The append tail and
// visual front are therefore independently observable.
func TestOverlayCompletionRemovesAppendTailNotFocusedFront(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	first := &recordingComponent{lines: []string{"first"}}
	second := &recordingComponent{lines: []string{"second"}}

	firstHandle := tu.OpenOverlay(first, OverlayOptions{})
	tu.OpenOverlay(second, OverlayOptions{})
	firstHandle.focus()

	if got := tu.ActiveOverlay(); got != first {
		t.Fatalf("focused visual front = %T %p, want first %p", got, got, first)
	}

	tu.hideOverlay()

	if got := tu.ActiveOverlay(); got != first {
		t.Fatalf("after append-tail removal active overlay = %T %p, want first %p", got, got, first)
	}
	if !tu.HasOverlay() {
		t.Fatal("append-tail completion removed the focused first overlay")
	}
}

func TestOverlayHiddenStateIsReversibleAndDistinctFromRemoval(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	component := &recordingComponent{lines: []string{"content"}}
	handle := tu.OpenOverlay(component, OverlayOptions{})

	handle.setHidden(true)
	if !handle.isHidden() {
		t.Fatal("setHidden(true) did not update the synchronous hidden query")
	}
	if tu.ActiveOverlay() != nil {
		t.Fatal("hidden overlay remained input eligible")
	}
	if !tu.HasOverlay() {
		t.Fatal("hidden overlay was permanently removed")
	}

	handle.setHidden(false)
	if handle.isHidden() || tu.ActiveOverlay() != component {
		t.Fatal("setHidden(false) did not restore visibility and focus")
	}

	handle.Close()
	handle.setHidden(false)
	handle.focus()
	if tu.HasOverlay() || tu.ActiveOverlay() != nil {
		t.Fatal("post-removal setters resurrected an overlay")
	}
}

func TestOverlayNonCapturingFocusAndUnfocusEligibility(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	first := &recordingComponent{lines: []string{"first"}}
	second := &recordingComponent{lines: []string{"second"}}
	firstHandle := tu.OpenOverlay(first, OverlayOptions{})
	secondHandle := tu.OpenOverlay(second, OverlayOptions{nonCapturing: true})

	if tu.ActiveOverlay() != first {
		t.Fatal("non-capturing mount stole automatic focus")
	}
	secondHandle.focus()
	if !secondHandle.isFocused() || firstHandle.isFocused() || tu.ActiveOverlay() != second {
		t.Fatal("explicit focus did not focus the non-capturing overlay")
	}

	secondHandle.unfocus()
	if tu.ActiveOverlay() != first {
		t.Fatal("unfocus did not fall back to the eligible capturing overlay")
	}
	firstHandle.unfocus(second)
	if tu.ActiveOverlay() != second {
		t.Fatal("explicit unfocus target was not honored")
	}
	secondHandle.setHidden(true)
	if tu.ActiveOverlay() != first {
		t.Fatal("hiding an explicitly focused non-capturing overlay did not restore fallback focus")
	}
}

func TestOverlayNestedSnapshotGenerationAndParentTeardown(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	parent := tu.OpenOverlay(&recordingComponent{lines: []string{"parent"}}, OverlayOptions{})
	childComponent := &recordingComponent{lines: []string{"local child"}}
	child := tu.openNestedOverlay(parent, childComponent, OverlayOptions{})
	if child == nil {
		t.Fatal("nested mount was rejected for a mounted parent")
	}

	geometry := tu.updateOverlayGeometry(100, 30)
	lines := []string{"remote child"}
	if !tu.replaceOverlaySnapshot(child, geometry, 1, lines, true) {
		t.Fatal("current atomic snapshot was rejected")
	}
	lines[0] = "caller mutated"
	if tu.replaceOverlaySnapshot(child, geometry-1, 2, []string{"stale geometry"}, true) {
		t.Fatal("stale geometry snapshot was accepted")
	}
	if tu.replaceOverlaySnapshot(child, geometry, 1, []string{"stale frame"}, true) {
		t.Fatal("duplicate frame snapshot was accepted")
	}

	snapshot := tu.overlaySnapshot()
	childEntry, ok := snapshot.entryByID(child.id)
	if !ok || !childEntry.hasFrame || childEntry.frame.lines[0] != "remote child" {
		t.Fatalf("immutable child frame = %#v, present=%v", childEntry.frame.lines, ok)
	}

	if !strings.Contains(strings.Join(tu.composeOverlayLines(nil, 100, 30), "\n"), "remote child") {
		t.Fatal("accepted immutable snapshot did not reach the production compositor")
	}
	resizedGeometry := tu.updateOverlayGeometry(120, 40)
	if retained := tu.overlaySnapshot().RetainedBytes; retained != 0 {
		t.Fatalf("geometry change retained %d stale snapshot bytes", retained)
	}
	if visible := strings.Join(tu.composeOverlayLines(nil, 120, 40), ""); strings.Contains(visible, "remote child") {
		t.Fatal("stale snapshot remained visible after geometry changed")
	}
	if !tu.replaceOverlaySnapshot(child, resizedGeometry, 2, []string{"resized child"}, true) {
		t.Fatal("current resized snapshot was rejected")
	}
	if !strings.Contains(strings.Join(tu.composeOverlayLines(nil, 120, 40), "\n"), "resized child") {
		t.Fatal("resized snapshot did not restore visibility atomically")
	}
	if tu.ActiveOverlay() != childComponent || !child.isFocused() {
		t.Fatal("current visible snapshot did not atomically restore focus and input eligibility")
	}

	parent.closeTree()
	if tu.HasOverlay() {
		t.Fatal("parent teardown retained nested entries")
	}
	// The zero-length model slice must not retain removed component graphs in
	// its backing array. A logical retained-byte counter cannot detect these
	// stale pointers.
	tu.overlayMu.Lock()
	backing := tu.overlayModel.entries[:cap(tu.overlayModel.entries)]
	for i, entry := range backing {
		if entry != nil {
			tu.overlayMu.Unlock()
			t.Fatalf("parent teardown backing slot %d retained entry %d", i, entry.id)
		}
	}
	tu.overlayMu.Unlock()
	child.setHidden(false)
	child.focus()
	if tu.ActiveOverlay() != nil {
		t.Fatal("stale child handle resurrected after parent teardown")
	}
	if nested := tu.openNestedOverlay(parent, childComponent, OverlayOptions{}); nested != nil {
		t.Fatal("nested mount accepted a removed parent")
	}
}

func TestOverlayGenericComponentOwnsFramingAndBuiltinModalPreservesFrame(t *testing.T) {
	tu := NewWithOutput(io.Discard, 20, 8)
	generic := &recordingComponent{lines: []string{"plain"}}
	tu.OpenOverlay(generic, OverlayOptions{
		width:  overlayCells(10),
		anchor: overlayTopLeft,
	})

	got := tu.composeOverlayLines([]string{"background"}, 20, 8)
	if generic.lastWidth != 10 {
		t.Fatalf("generic render width = %d, want resolved content width 10", generic.lastWidth)
	}
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "┌") || strings.Contains(joined, "│") || strings.Contains(joined, "└") {
		t.Fatalf("generic overlay gained host framing: %q", joined)
	}
	if !strings.Contains(joined, "plain") {
		t.Fatalf("generic component output missing: %q", joined)
	}

	tu.hideOverlay()
	modal := &recordingComponent{lines: []string{"choice"}}
	tu.openModalOverlay(modal, "Select", 0.75, 0.75)
	framedLines := tu.composeOverlayLines(nil, 20, 8)
	framed := strings.Join(framedLines, "\n")
	for _, want := range []string{"┌ Select ", "│choice", "└"} {
		if !strings.Contains(framed, want) {
			t.Fatalf("built-in modal lost %q framing: %q", want, framed)
		}
	}
	visible := make([]string, len(framedLines))
	for i, line := range framedLines {
		visible[i] = stripANSI(line)
	}
	wantVisible := []string{
		"",
		"  ┌ Select ─────┐   ",
		"  │choice       │   ",
		"  │             │   ",
		"  │             │   ",
		"  │             │   ",
		"  └─────────────┘   ",
		"",
	}
	if !slices.Equal(visible, wantVisible) {
		t.Fatalf("built-in modal appearance changed:\n got %#v\nwant %#v", visible, wantVisible)
	}
}

func TestOverlayGeometryRecalculatesAndVisibilityIsReentrant(t *testing.T) {
	tu := NewWithOutput(io.Discard, 100, 40)
	component := &recordingComponent{lines: make([]string, 15)}
	factoryCalls := 0
	var handle *OverlayHandle
	handle = tu.openOverlayWithOptionsFactory(component, func() OverlayOptions {
		factoryCalls++
		return OverlayOptions{
			width:     overlayPercent(50),
			minWidth:  60,
			maxHeight: overlayPercent(25),
			anchor:    overlayBottomRight,
			margin:    overlayMargin{Top: 2, Right: 2, Bottom: 2, Left: 2},
			visible: func(_, _ int) bool {
				return handle == nil || !handle.isHidden()
			},
		}
	})
	if factoryCalls != 1 {
		t.Fatalf("outer options factory calls = %d, want 1", factoryCalls)
	}

	done := make(chan []string, 1)
	go func() { done <- tu.composeOverlayLines(nil, 100, 40) }()
	var rendered []string
	select {
	case rendered = <-done:
	case <-time.After(time.Second):
		t.Fatal("visibility callback deadlocked under overlay lock")
	}
	if component.lastWidth != 60 {
		t.Fatalf("resolved width = %d, want min-width-clamped 60", component.lastWidth)
	}
	if len(rendered) != 40 {
		t.Fatalf("composed rows = %d, want terminal height 40", len(rendered))
	}

	tu.composeOverlayLines(nil, 240, 80)
	if component.lastWidth != 120 {
		t.Fatalf("resize render width = %d, want recalculated 50%% = 120", component.lastWidth)
	}
	if factoryCalls != 1 {
		t.Fatalf("resize reran outer options factory: calls=%d", factoryCalls)
	}

	handle.setHidden(true)
	if visible := strings.Join(tu.composeOverlayLines(nil, 240, 80), ""); visible != "" {
		t.Fatalf("hidden responsive overlay rendered pixels: %q", visible)
	}
}

func TestOverlayCommandsHandleEmptyRepeatedAndStrictIdentity(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	tu.hideOverlay()

	first := &recordingComponent{lines: []string{"same"}}
	second := &recordingComponent{lines: []string{"same"}}
	firstHandle := tu.OpenOverlay(first, OverlayOptions{})
	secondHandle := tu.OpenOverlay(second, OverlayOptions{})
	if firstHandle.isFocused() || !secondHandle.isFocused() {
		t.Fatal("focus query used value equality instead of strict entry identity")
	}

	secondHandle.setHidden(false)
	secondHandle.setHidden(true)
	secondHandle.setHidden(true)
	secondHandle.focus()
	if secondHandle.isFocused() || tu.ActiveOverlay() != first {
		t.Fatal("hidden overlay accepted focus")
	}
	secondHandle.setHidden(false)
	secondHandle.unfocus(nil)
	if tu.ActiveOverlay() != nil {
		t.Fatal("explicit nil unfocus target did not clear overlay focus")
	}
	secondHandle.unfocus()
	if tu.ActiveOverlay() != nil {
		t.Fatal("repeated unfocus changed cleared focus")
	}
	firstHandle.focus()
	firstHandle.Close()
	firstHandle.Close()
	if tu.ActiveOverlay() != second {
		t.Fatal("targeted removal did not restore remaining eligible overlay")
	}
	secondHandle.Close()
	tu.hideOverlay()
	if tu.HasOverlay() {
		t.Fatal("repeated teardown retained an entry")
	}
}

func TestOverlayComponentRenderIsReentrantOutsideStateLock(t *testing.T) {
	tu := NewWithOutput(io.Discard, 40, 10)
	component := &reentrantRenderComponent{}
	handle := tu.OpenOverlay(component, OverlayOptions{width: overlayCells(20)})
	component.onRender = func() { handle.setHidden(true) }

	done := make(chan struct{})
	go func() {
		tu.composeOverlayLines(nil, 40, 10)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("component Render deadlocked under overlay state lock")
	}
	if !handle.isHidden() || tu.ActiveOverlay() != nil {
		t.Fatal("reentrant render mutation was lost")
	}
}

func TestOverlayBlockedReplacementRestoresAfterUnmount(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	editor := &recordingComponent{lines: []string{"editor"}}
	replacement := &recordingComponent{lines: []string{"replacement"}}
	tu.Add(editor)
	tu.Add(replacement)
	tu.SetFocus(editor)
	overlay := &recordingComponent{lines: []string{"overlay"}}
	tu.OpenOverlay(overlay, OverlayOptions{})

	tu.SetFocus(replacement)
	if tu.ActiveOverlay() != nil || tu.FocusedComponent() != replacement {
		t.Fatal("mounted replacement did not retain focus while overlay restoration was blocked")
	}
	tu.Remove(replacement)
	tu.SetFocus(editor)
	if tu.ActiveOverlay() != overlay || tu.FocusedComponent() != overlay {
		t.Fatal("unmounted replacement did not restore the blocked overlay")
	}
}

func TestOverlayBlockedUnfocusTargetDefersUntilReplacementCloses(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	editor := &recordingComponent{lines: []string{"editor"}}
	replacement := &recordingComponent{lines: []string{"replacement"}}
	target := &recordingComponent{lines: []string{"target"}}
	tu.Add(editor)
	tu.Add(replacement)
	tu.Add(target)
	tu.SetFocus(editor)
	handle := tu.OpenOverlay(&recordingComponent{lines: []string{"overlay"}}, OverlayOptions{})

	tu.SetFocus(replacement)
	handle.unfocus(target)
	if tu.FocusedComponent() != replacement {
		t.Fatal("blocked unfocus target displaced the active replacement early")
	}
	tu.Remove(replacement)
	tu.SetFocus(editor)
	if tu.FocusedComponent() != target || tu.ActiveOverlay() != nil {
		t.Fatal("blocked unfocus target was not applied after replacement teardown")
	}
}

func TestOverlayBlockedSetFocusNilResumesOverlay(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	editor := &recordingComponent{lines: []string{"editor"}}
	replacement := &recordingComponent{lines: []string{"replacement"}}
	tu.Add(editor)
	tu.Add(replacement)
	tu.SetFocus(editor)
	overlay := &recordingComponent{lines: []string{"overlay"}}
	tu.OpenOverlay(overlay, OverlayOptions{})

	tu.SetFocus(replacement)
	tu.Remove(replacement)
	tu.SetFocus(nil)
	if tu.ActiveOverlay() != overlay {
		t.Fatal("setFocus(nil) did not resume the visible blocked overlay")
	}
}

func TestOverlayFocusAncestryCycleDoesNotHang(t *testing.T) {
	model := overlayModel{}
	a := &recordingComponent{lines: []string{"a"}}
	b := &recordingComponent{lines: []string{"b"}}
	aID := model.mount(a, OverlayOptions{}, 0, true)
	bID := model.mount(b, OverlayOptions{}, 0, true)
	model.overlayByID(aID).preFocus = b
	model.overlayByID(bID).preFocus = a

	done := make(chan bool, 1)
	go func() { done <- model.isOverlayFocusAncestor(model.overlayByID(aID), &recordingComponent{}) }()
	select {
	case ancestor := <-done:
		if ancestor {
			t.Fatal("unrelated target became an ancestor through a cycle")
		}
	case <-time.After(time.Second):
		t.Fatal("cyclic overlay pre-focus ancestry hung")
	}
}

func TestOverlayVisibilityRefreshesAtMountFocusQueryAndInputBoundaries(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	visible := false
	component := &recordingComponent{lines: []string{"overlay"}}
	handle := tu.OpenOverlay(component, OverlayOptions{visible: func(int, int) bool { return visible }})
	if tu.hasOverlay() || tu.ActiveOverlay() != nil || handle.isFocused() {
		t.Fatal("initially invisible overlay became visible or input eligible before composition")
	}

	visible = true
	if !tu.hasOverlay() || tu.ActiveOverlay() != component || !handle.isFocused() {
		t.Fatal("visibility query did not atomically restore visible focus and input eligibility")
	}

	visible = false
	handle.focus()
	if tu.hasOverlay() || tu.ActiveOverlay() != nil || handle.isFocused() {
		t.Fatal("focus used stale executable visibility without a render")
	}
}

func TestOverlayRemoteCommandsApplyOnlyOnOwnerDispatcher(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	handle := tu.OpenOverlay(&recordingComponent{lines: []string{"overlay"}}, OverlayOptions{})
	queued := make(chan func(), 1)
	tu.SetOverlayCommandDispatcher(func(fn func()) { queued <- fn })

	if !tu.postOverlayCommand(overlayCommand{kind: overlaySetHidden, entryID: handle.id, hidden: true}) {
		t.Fatal("remote-ready owner command was rejected")
	}
	if handle.isHidden() {
		t.Fatal("posted command mutated state before owner-loop execution")
	}
	(<-queued)()
	if !handle.isHidden() {
		t.Fatal("owner-loop execution did not apply the posted command")
	}
}

func TestOverlayRestoresPreOverlayFocusTarget(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	editor := &recordingComponent{lines: []string{"editor"}}
	tu.SetFocus(editor)
	handle := tu.OpenOverlay(&recordingComponent{lines: []string{"overlay"}}, OverlayOptions{})
	if tu.FocusedComponent() == editor {
		t.Fatal("capturing overlay did not take focus")
	}
	handle.Close()
	if tu.FocusedComponent() != editor || tu.ActiveOverlay() != nil {
		t.Fatal("final overlay removal did not restore the pre-overlay focus target")
	}
}

func TestModalOverlayTwoColumnBoundaryDoesNotPanic(t *testing.T) {
	tu := NewWithOutput(io.Discard, 2, 4)
	tu.openModalOverlay(&recordingComponent{lines: []string{"x"}}, "T", 1, 1)
	lines := tu.composeOverlayLines(nil, 2, 4)
	for i, line := range lines {
		if !utf8.ValidString(line) || widthx.VisibleWidth(line) > 2 {
			t.Fatalf("row %d invalid at two-column boundary: %q", i, line)
		}
	}
}

type reentrantRenderComponent struct {
	onRender func()
}

func (c *reentrantRenderComponent) Render(int) []string {
	if c.onRender != nil {
		fn := c.onRender
		c.onRender = nil
		fn()
	}
	return []string{"rendered"}
}

func (*reentrantRenderComponent) Invalidate() {}

type recordingComponent struct {
	lines     []string
	lastWidth int
}

func (c *recordingComponent) Render(width int) []string {
	c.lastWidth = width
	return append([]string(nil), c.lines...)
}

func (*recordingComponent) Invalidate() {}
