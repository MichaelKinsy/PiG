package codingagent

import (
	"slices"
	"testing"
	"time"
)

// ownedFramesAfterTheFirst is a harness whose loop owns the frames (installRenderDispatcher), started, with its first frame run: the next frame
// it is asked for is throttled, so its deadline is ahead.
func ownedFramesAfterTheFirst(t *testing.T) *InteractiveMode {
	t.Helper()
	m := newPendingDisplayHarness(t)
	m.installRenderDispatcher()
	m.tuiInst.Start()
	t.Cleanup(func() { m.tuiInst.Stop() })
	m.tuiInst.RequestRender()
	select {
	case <-m.renderWakeCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick for the first frame")
	}
	m.tuiInst.RunDueRender()
	return m
}

func noFrameTick(t *testing.T, m *InteractiveMode, when string) {
	t.Helper()
	select {
	case at := <-m.renderWakeCh:
		t.Fatalf("%s the frame timer ticked at %v", when, at)
	case <-time.After(40 * time.Millisecond):
	}
}

// The deadline the turn end reads (frameDue) and the frame timer are one pair. The owner's stop replaces the timer (the stop hook of
// installRenderDispatcher): the stopped renderer keeps neither a deadline nor an armed timer, and a frame asked of it afterwards arms neither.
func TestAStoppedRendererKeepsNoFrameDeadlineWhenItsTimerIsReplaced(t *testing.T) {
	m := ownedFramesAfterTheFirst(t)
	m.tuiInst.RequestRender()
	if m.frameDue.Load() == 0 {
		t.Fatal("a throttled frame stored no deadline")
	}
	m.tuiInst.Stop()
	m.useFrameTimer(newRuntimeFrameTimer())
	if due := m.frameDue.Load(); due != 0 {
		t.Fatalf("the stopped renderer kept the frame deadline %d", due)
	}
	if m.frameRequested() {
		t.Fatal("the stopped renderer still has a requested frame")
	}
	noFrameTick(t, m, "after the stop")
	m.tuiInst.RequestRender()
	if due := m.frameDue.Load(); due != 0 {
		t.Fatalf("a frame asked of the stopped renderer stored the deadline %d", due)
	}
	noFrameTick(t, m, "after a request to the stopped renderer")
}

// A frame asked for before its timer is replaced is not lost with the replaced timer's tick: the turn end still knows of it and paints it
// (paintTurnEnd). Nothing else gives the turn end a reason to paint here: no status indicator is shown and its rows do not change.
func TestAFrameAskedForBeforeItsTimerIsReplacedIsStillRequested(t *testing.T) {
	m := ownedFramesAfterTheFirst(t)
	content := &renderRecorder{text: "asked for before the timer was replaced"}
	m.tuiInst.Add(content)
	m.tuiInst.RequestRender()
	m.useFrameTimer(newRuntimeFrameTimer())
	if !m.frameRequested() {
		t.Fatal("the replacement of the frame timer forgot the requested frame")
	}
	if len(content.renders) != 0 {
		t.Fatalf("%d frames were painted before the turn end", len(content.renders))
	}
	before := m.beginTurnEnd()
	if before.indicator {
		t.Fatal("a status indicator is shown: the turn end would paint without the requested frame")
	}
	m.paintTurnEnd(before)
	if !slices.Equal(content.renders, []string{content.text}) {
		t.Fatalf("the turn end painted %q, want the one frame that was asked for", content.renders)
	}
}
