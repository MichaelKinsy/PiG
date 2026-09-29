package tui

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

type upstreamKeyboardComponent struct {
	lines   []string
	renders int
}

func (c *upstreamKeyboardComponent) Render(int) []string     { c.renders++; return c.lines }
func (*upstreamKeyboardComponent) Invalidate()               {}
func (c *upstreamKeyboardComponent) HandleInput(data string) { c.lines = []string{data} }

// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:117
func TestUpstreamTUIKeyboardRender(t *testing.T) {
	t.Run("renders keyboard input without waiting for a throttled frame", func(t *testing.T) {
		ui := NewWithOutput(&bytes.Buffer{}, 40, 10)
		component := &upstreamKeyboardComponent{lines: []string{"initial"}}
		ui.Add(component)
		ui.SetFocus(component)
		now := time.Unix(1, 0)
		ui.now = func() time.Time { return now }
		var timer *manualRenderTimer
		ui.afterFunc = func(delay time.Duration, fn func()) stoppableTimer {
			if delay <= 0 {
				t.Fatalf("keyboard render incorrectly uses timer delay %s", delay)
			}
			timer = &manualRenderTimer{fn: fn}
			return timer
		}
		var queue []func()
		ui.SetRenderDispatcher(func(fn func()) { queue = append(queue, fn) })
		ui.Render()
		before := component.renders
		component.lines = []string{"pending"}
		ui.RequestRender()
		for _, key := range []string{"first", "second", "typed"} {
			component.HandleInput(key)
			ui.RequestImmediateRender()
		}
		if timer == nil || !timer.stopped {
			t.Fatal("pending throttled frame was not cancelled")
		}
		if len(queue) != 1 {
			t.Fatalf("queued frames=%d, want one coalesced next-turn frame", len(queue))
		}
		queue[0]()
		if component.renders != before+1 {
			t.Fatalf("render count=%d, want %d", component.renders, before+1)
		}
		if !slices.Equal(component.lines, []string{"typed"}) {
			t.Fatalf("lines=%q, want typed", component.lines)
		}
		ui.CancelPendingRender()
	})
}

func TestImmediateRenderCancellationAndReplacement(t *testing.T) {
	ui := NewWithOutput(&bytes.Buffer{}, 40, 10)
	c := &upstreamKeyboardComponent{lines: []string{"first"}}
	ui.Add(c)
	var queue []func()
	ui.SetRenderDispatcher(func(fn func()) { queue = append(queue, fn) })
	ui.RequestImmediateRender()
	ui.CancelPendingRender()
	queue[0]()
	if c.renders != 0 {
		t.Fatal("cancelled immediate frame rendered")
	}
	ui.RequestImmediateRender()
	ui.Render()
	queue[1]()
	if c.renders != 1 {
		t.Fatalf("direct render did not consume queued frame: %d", c.renders)
	}
	ui.RequestImmediateRender()
	queue[2]()
	if c.renders != 2 {
		t.Fatalf("new immediate frame did not run: %d", c.renders)
	}
	ui.RequestImmediateRender()
	ui.Stop()
	queue[3]()
	if c.renders != 2 {
		t.Fatalf("queued frame rendered after stop: %d", c.renders)
	}
}
