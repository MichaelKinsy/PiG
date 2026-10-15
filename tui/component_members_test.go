package tui

import (
	"strings"
	"testing"
)

// PiG-only: the upstream component tests render these components but never call Box.clear/removeChild/invalidate/
// setBgFn, Text.setCustomBgFn/invalidate or Container.children on their own.
type countingChild struct {
	label       string
	invalidates int
}

func (c *countingChild) Render(int) []string { return []string{c.label} }
func (c *countingChild) Invalidate()         { c.invalidates++ }

// Pi: packages/tui/src/components/box.ts:35 (Box.removeChild); packages/tui/src/components/box.ts:43 (Box.clear); packages/tui/src/components/box.ts:48 (Box.setBgFn).
func TestBoxRemoveChildClearAndSetBgFnChangeWhatRenders(t *testing.T) {
	a, b := &countingChild{label: "alpha"}, &countingChild{label: "beta"}
	box := NewPaddedBox(0, 0, nil)
	box.AddChild(a)
	box.AddChild(b)
	if got := strings.Join(box.Render(20), "|"); !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Fatalf("render = %q", got)
	}
	box.RemoveChild(a)
	if got := strings.Join(box.Render(20), "|"); strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Fatalf("after RemoveChild render = %q", got)
	}
	box.RemoveChild(a) // not a child: no effect
	box.SetBgFn(func(s string) string { return "<bg>" + s + "</bg>" })
	if got := strings.Join(box.Render(20), "|"); !strings.Contains(got, "<bg>") {
		t.Fatalf("SetBgFn was not applied: %q", got)
	}
	box.SetBgFn(nil)
	if got := strings.Join(box.Render(20), "|"); strings.Contains(got, "<bg>") {
		t.Fatalf("SetBgFn(nil) kept the background: %q", got)
	}
	box.Clear()
	if lines := box.Render(20); len(lines) != 0 {
		t.Fatalf("Clear left %q", lines)
	}
}

func TestBoxInvalidateReachesEveryChildAndDropsTheRenderCache(t *testing.T) {
	a, b := &countingChild{label: "a"}, &countingChild{label: "b"}
	box := NewPaddedBox(0, 0, nil)
	box.AddChild(a)
	box.AddChild(b)
	before := a.invalidates
	box.Invalidate()
	if a.invalidates != before+1 || b.invalidates == 0 {
		t.Fatalf("children invalidated %d and %d times", a.invalidates-before, b.invalidates)
	}
	if box.cache != nil {
		t.Fatal("Invalidate kept the render cache")
	}
}

func TestTextSetCustomBgFnAndInvalidateRerender(t *testing.T) {
	calls := 0
	text := NewText("hello")
	text.SetCustomBgFn(func(s string) string { calls++; return "[" + s + "]" })
	first := strings.Join(text.Render(20), "|")
	if !strings.Contains(first, "[") || calls == 0 {
		t.Fatalf("custom background not applied: %q (%d calls)", first, calls)
	}
	text.Render(20)
	cached := calls
	text.Render(20)
	if calls != cached {
		t.Fatal("an unchanged render must come from the cache")
	}
	text.Invalidate()
	text.Render(20)
	if calls == cached {
		t.Fatal("Invalidate did not force a re-render")
	}
	text.SetCustomBgFn(nil)
	if got := strings.Join(text.Render(20), "|"); strings.Contains(got, "[") {
		t.Fatalf("SetCustomBgFn(nil) kept the background: %q", got)
	}
}

func TestContainerChildrenIsAnInsertionOrderedSnapshot(t *testing.T) {
	a, b := &countingChild{label: "a"}, &countingChild{label: "b"}
	container := NewContainer(a)
	container.Add(b)
	snapshot := container.Children()
	if len(snapshot) != 2 || snapshot[0] != Component(a) || snapshot[1] != Component(b) {
		t.Fatalf("children = %v", snapshot)
	}
	container.Remove(a)
	if len(snapshot) != 2 || len(container.Children()) != 1 {
		t.Fatal("the snapshot must not follow later changes")
	}
}

// PiG-only: markdown tests render but never call Invalidate to force the transform and parser to run again on unchanged
// content and width.
func TestMarkdownInvalidateForcesTheTransformAndParserToRunAgain(t *testing.T) {
	calls := 0
	md := NewMarkdown("# title")
	md.Transform = func(source string, _ int) string { calls++; return source }
	md.Render(40)
	md.Render(40)
	if calls != 1 {
		t.Fatalf("an unchanged render must come from the cache, transform ran %d times", calls)
	}
	md.Invalidate()
	md.Render(40)
	if calls != 2 {
		t.Fatalf("Invalidate must force a fresh transform and parse, transform ran %d times", calls)
	}
}
