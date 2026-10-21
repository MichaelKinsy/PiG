package codingagent

import (
	"context"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// extensionDialogReservedLines is the terminal budget kept for chrome around an
// open extension dialog: the status container, the footer, and a blank
// separator.
//
// Approximate on purpose. The exact figure would be the rendered height of
// every layout sibling, but reading it means rendering them on each dialog open
// and each resize, on the UI loop, and exposing the container's children. That
// cost buys at most a row or two of transcript.
//
// The approximation is the mechanism, not this number: upstream proportions the
// viewport natively and never computes a line budget, so SetMaxLines is a
// line-renderer stand-in for a layout pass pig does not have. A proportional
// layout would delete this constant rather than refine it.
const extensionDialogReservedLines = 3

// minExtensionDialogChatLines is the floor for transcript lines kept visible
// while an extension dialog is open. On a terminal too short for both, the
// dialog wins, but the transcript never renders empty and leaves the user
// answering with no context at all.
const minExtensionDialogChatLines = 3

// setExtensionDialogViewMode caps (or restores) the chat container so an open
// extension dialog has room without hiding the reasoning that motivated it
// (#667).
func (m *InteractiveMode) setExtensionDialogViewMode(on bool, dialogLines int) {
	if !on {
		m.chatContainer.SetMaxLines(0)
		m.tuiInst.Render()
		return
	}
	available := m.tuiInst.Height() - dialogLines - extensionDialogReservedLines
	m.chatContainer.SetMaxLines(max(available, minExtensionDialogChatLines))
	m.tuiInst.Render()
}

// onTerminalHeightChange keeps height-dependent state current across a resize.
// The extension-dialog chat cap is derived from terminal height, so without
// this a resize while a dialog is open leaves the previous height's cap in
// place.
func (m *InteractiveMode) onTerminalHeightChange(height int) {
	if m.opts.SubprocessHost != nil {
		m.opts.SubprocessHost.NotifyHeight(height)
	}
	if dialog := m.shownExtensionDialog(); dialog != nil {
		m.setExtensionDialogViewMode(true, m.extensionDialogLines(dialog.component))
	}
}

// extensionDialogLines measures a dialog component at the current width so the
// chat cap reflects the dialog actually on screen rather than a fixed guess.
func (m *InteractiveMode) extensionDialogLines(component tui.Component) int {
	width := m.tuiInst.Width()
	if width <= 0 || component == nil {
		return 0
	}
	return len(component.Render(width))
}

func (m *InteractiveMode) setStatusContainerLabel(label string) {
	if m.activeStatusIndicator != nil && m.activeStatusIndicator.Kind == "retry" {
		m.activeStatusIndicator.SetMessage(label)
		return
	}
	m.showStatusIndicator(&tui.StatusIndicator{Kind: "retry", Loader: tui.NewLoader(m.tuiInst, tui.ThemeFg("warning"), tui.ThemeFg("muted"), label, nil)})
}

func (m *InteractiveMode) startWorkingLoader() {
	if !m.workingVisible {
		return
	}
	message := "Working"
	if m.workingMessage != nil {
		message = *m.workingMessage
	}
	indicator := &tui.StatusIndicator{Kind: "working", Loader: tui.NewLoader(m.tuiInst, tui.ThemeFg("accent"), tui.ThemeFg("muted"), message, nil)}
	m.applyWorkingIndicatorOptions(indicator)
	m.showStatusIndicator(indicator)
	m.tuiInst.RequestRender()
}

func (m *InteractiveMode) stopWorkingLoader() { m.clearStatusIndicator("working") }

// tickSpinner drives status-indicator frames. Like Pi's Loader, whose
// setInterval exists only while a loader does, the ticker parks once a tick
// finds the session idle with no indicator, so an idle session takes no timer
// wakes. Owner-loop posts, scheduled renders and indicator changes wake it.
//
// A run's end (or a busy tick) arms one idle collection. Once the session has
// stayed idle for idleCollectAfter, the heap is returned to the OS once if it
// grew since the last collection; a wake restarts the wait, and while the wait
// runs every input event wakes it (Pi-Bolt idle-gc.ts).
// pig additive (D172): the ticker parks while idle and returns heap once after a run.
func (m *InteractiveMode) tickSpinner(ctx context.Context) {
	interval := 80 * time.Millisecond // upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
	//perf:ok one ticker per spinner goroutine; parking stops it and a wake resets it
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	tickC := ticker.C
	var collectC <-chan time.Time
	var collectTimer *time.Timer
	stopCollect := func() {
		if collectTimer != nil {
			collectTimer.Stop()
			collectTimer, collectC = nil, nil
			m.idleCollectPending.Store(false)
		}
	}
	defer stopCollect()
	for {
		select {
		case <-ctx.Done():
			return
		case next := <-m.spinnerIntervalCh:
			interval = next
			ticker.Reset(interval)
			tickC = ticker.C
			stopCollect()
		case <-m.spinnerWakeCh:
			if tickC == nil {
				ticker.Reset(interval)
				tickC = ticker.C
			}
			stopCollect()
		case park := <-m.spinnerParkCh:
			if tickC == nil || park.gen != m.spinnerWakeGen.Load() {
				continue // a wake arrived after the idle tick was posted
			}
			ticker.Stop()
			tickC = nil
			if park.afterBusy {
				stopCollect()
				//perf:ok once per idle park while armed (D172 idle heap return), not per tick
				collectTimer = time.NewTimer(idleCollectAfter)
				collectC = collectTimer.C
				m.idleCollectPending.Store(true)
			}
		case <-collectC:
			collectTimer, collectC = nil, nil
			m.idleCollectPending.Store(false)
			m.idleCollectArmed.Store(false)
			m.idleCollector.collect()
		case <-tickC:
			// Like a Node setInterval on a blocked event loop, ticks that come
			// due while one is still queued collapse into it; a stalled loop
			// must not wake to a backlog of frames or a task queue full of
			// ticks that crowds out other posts.
			if !m.spinnerTickQueued.CompareAndSwap(false, true) {
				continue
			}
			gen := m.spinnerWakeGen.Load()
			if !m.postUITaskQuiet(func() {
				m.spinnerTickQueued.Store(false)
				if ctx.Err() != nil {
					return
				}
				if m.isIdle && m.activeStatusIndicator == nil {
					select {
					case m.spinnerParkCh <- spinnerPark{gen: gen, afterBusy: m.idleCollectArmed.Load()}:
					default:
					}
					return
				}
				m.idleCollectArmed.Store(true)
				m.tickStatusIndicators(time.Now())
			}) {
				m.spinnerTickQueued.Store(false)
			}
		}
	}
}

// idleCollectAfter is how long the session stays idle after a run before the
// idle collection. Mirrors Pi-Bolt's IDLE_GC_DELAY_MS (packages/coding-agent/src/core/idle-gc.ts).
const idleCollectAfter = 500 * time.Millisecond

type spinnerPark struct {
	gen       uint64
	afterBusy bool
}

// wakeSpinner resumes a parked spinner ticker; its next tick re-checks idleness.
func (m *InteractiveMode) wakeSpinner() {
	if m.spinnerWakeCh == nil {
		return
	}
	m.spinnerWakeGen.Add(1)
	select {
	case m.spinnerWakeCh <- struct{}{}:
	default:
	}
}

// pig additive (D172): idle heap return after a run, mirroring Pi-Bolt 4ee61e8a.
// idleHeapCollector returns freed heap to the OS once per idle period, only
// when the live heap grew since its last collection.
type idleHeapCollector struct {
	lastHeap uint64
	collects atomic.Int32
}

func (c *idleHeapCollector) collect() {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 || sample[0].Value.Uint64() <= c.lastHeap {
		return
	}
	// pig additive (D112, D172): the request caches hold the encodings of the conversation the run sent; an idle session lets them go, and the
	// next prompt encodes its request as its first did.
	ai.ReleaseRequestCaches()
	// One forced cycle moves sync.Pool contents to their victim caches and leaves them live; the second, in
	// FreeOSMemory, frees them and returns the pages (about 290 KB at the tmux prompt, TestTmuxIdleMemoryGate).
	runtime.GC()
	debug.FreeOSMemory()
	metrics.Read(sample)
	c.lastHeap = sample[0].Value.Uint64()
	c.collects.Add(1)
}

func (m *InteractiveMode) tickStatusIndicators(now time.Time) {
	if m.isIdle && m.activeStatusIndicator == nil {
		return
	}
	if m.statusLine != nil {
		m.statusLine.Invalidate()
	}
	if indicator := m.activeStatusIndicator; indicator != nil && now.Sub(m.statusLastFrame) >= m.statusFrameInterval() {
		if len(indicator.Frames) > 1 {
			indicator.Tick()
			if m.remoteEditor != nil && m.activeWorkingIndicatorEmbedded {
				m.reconfigureRemoteEditor()
			}
		}
		m.statusLastFrame = now
		// Node's setInterval rearms from callback entry, not the previous deadline. Rearm only a due callback so dispatch jitter cannot skip a frame or postpone a replacement indicator's first frame.
		m.resetSpinnerInterval()
	}
	for _, block := range m.bashOrder {
		if loader := block.Loader(); loader != nil {
			loader.Tick()
		}
	}
	m.tuiInst.Render()
}

// openExternalEditor hands the terminal to $VISUAL || $EDITOR with the
// current editor buffer content, then loads the result back.
//
// Wraps OpenExternalEditor (the pure helper) with the TUI lifecycle:
//  1. show cursor (editor needs it visible);
//  2. restore cooked-mode terminal (rawRestore);
//  3. run editor;
//  4. re-enter raw mode + hide cursor;
//  5. SetText if successful, flash status either way;
//  6. RepaintAll: the editor may have left arbitrary ANSI state.
