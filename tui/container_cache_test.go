package tui

import "testing"

type cacheProbeComponent struct {
	invalidatable
	lines       []string
	renderCount int
	alwaysDirty bool
}

func (c *cacheProbeComponent) Render(int) []string {
	c.renderCount++
	return c.lines
}

func (c *cacheProbeComponent) IsDirty() bool {
	return c.alwaysDirty || c.invalidatable.IsDirty()
}

// A clean child is rendered once and reused; a dirty child re-renders and
// then reuses again after it settles.
func TestContainerCachesCleanChildAndRerendersOnDirty(t *testing.T) {
	child := &cacheProbeComponent{lines: []string{"one"}}
	container := NewContainer(child)

	if got := container.Render(80); child.renderCount != 1 || len(got) != 1 || got[0] != "one" {
		t.Fatalf("first render = %v, renderCount=%d", got, child.renderCount)
	}
	if got := container.Render(80); child.renderCount != 1 || got[0] != "one" {
		t.Fatalf("second render must reuse cache: renderCount=%d", child.renderCount)
	}

	child.lines = []string{"two"}
	child.Invalidate()
	if got := container.Render(80); child.renderCount != 2 || got[0] != "two" {
		t.Fatalf("dirty render = %v, renderCount=%d", got, child.renderCount)
	}
	if got := container.Render(80); child.renderCount != 2 || got[0] != "two" {
		t.Fatalf("render after settle must reuse cache: renderCount=%d", child.renderCount)
	}
}

// A live component (running bash elapsed / animated loader) reports dirty
// every frame and must never be served from cache.
func TestContainerNeverCachesLiveComponent(t *testing.T) {
	live := &cacheProbeComponent{lines: []string{"Elapsed 0.1s"}, alwaysDirty: true}
	container := NewContainer(live)
	for range 5 {
		container.Render(80)
	}
	if live.renderCount != 5 {
		t.Fatalf("live component must render every frame; renderCount=%d, want 5", live.renderCount)
	}
}

// Appending a child during streaming must not invalidate the cache of the
// settled history children: only the new child renders.
func TestContainerAppendKeepsHistoryCached(t *testing.T) {
	history := &cacheProbeComponent{lines: []string{"old"}}
	container := NewContainer(history)
	container.Render(80)
	if history.renderCount != 1 {
		t.Fatalf("history first render count=%d", history.renderCount)
	}

	tail := &cacheProbeComponent{lines: []string{"new"}}
	container.Add(tail)
	got := container.Render(80)
	if history.renderCount != 1 {
		t.Fatalf("appending a child must not re-render settled history; count=%d", history.renderCount)
	}
	if tail.renderCount != 1 || len(got) != 2 || got[0] != "old" || got[1] != "new" {
		t.Fatalf("append render = %v, tailCount=%d", got, tail.renderCount)
	}
}

// A width change busts the cache so wrapped children re-render.
func TestContainerWidthChangeBustsCache(t *testing.T) {
	child := &cacheProbeComponent{lines: []string{"x"}}
	container := NewContainer(child)
	container.Render(80)
	container.Render(80)
	if child.renderCount != 1 {
		t.Fatalf("same width should reuse; count=%d", child.renderCount)
	}
	container.Render(100)
	if child.renderCount != 2 {
		t.Fatalf("width change must re-render; count=%d", child.renderCount)
	}
}

// A component that does not implement the dirty contract is always rendered,
// so it can never be served stale.
type noDirtyProbeComponent struct{ renderCount int }

func (c *noDirtyProbeComponent) Render(int) []string {
	c.renderCount++
	return []string{"x"}
}

func (c *noDirtyProbeComponent) Invalidate() {}

func TestContainerAlwaysRendersNonDirtyComponent(t *testing.T) {
	child := &noDirtyProbeComponent{}
	container := NewContainer(child)
	container.Render(80)
	container.Render(80)
	if child.renderCount != 2 {
		t.Fatalf("non-dirty component must render every frame; count=%d", child.renderCount)
	}
}
