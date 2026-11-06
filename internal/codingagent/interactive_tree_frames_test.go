package codingagent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// frameLog is the terminal output of a renderer, readable from the worker goroutine while the owner loop writes it.
type frameLog struct {
	mu   sync.Mutex
	data strings.Builder
}

func (l *frameLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.Write(p)
}

// text is everything written since the last call, without escape sequences.
func (l *frameLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	written := l.data.String()
	l.data.Reset()
	return widthx.StripAnsi(written)
}

// treeFramesMode is a mode whose renderer runs frames on its owner loop, as the interactive mode does, over a transcript that shows oldLeaf.
func treeFramesMode(t *testing.T, oldLeaf string) (*InteractiveMode, *frameLog, *lifecycleNavigationHandle) {
	t.Helper()
	m := statusBorderMode(t, false)
	log := &frameLog{}
	m.tuiInst = tui.NewWithOutput(log, 80, 24)
	m.installRenderDispatcher()
	m.chatContainer.AddChild(tui.NewText(oldLeaf))
	m.editorContainer = tui.NewContainer(m.editor)
	m.tuiInst.AddChild(m.chatContainer)
	m.tuiInst.AddChild(m.statusContainer)
	m.tuiInst.AddChild(m.editorContainer)
	m.tuiInst.Render()
	log.text()
	h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 8)}
	m.opts.SessionHandle = h
	m.eventCh = h.events
	return m, log, h
}

// PiG holds the frame the prompt's close requested until the navigation returns (D270; Pi paints the old transcript with the prompt gone in
// that window, interactive-mode.ts:5750-5768). A frame that ran meanwhile would show the old transcript with the prompt gone.
func TestTreeNavigationPaintsNoStaleFrameBeforeTheTranscriptSwap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, log, h := treeFramesMode(t, "OLD LEAF")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		m.runCtx = ctx
		var during string
		h.navigate = func(context.Context) (NavigateTreeResult, error) {
			// The close of the selector requested this frame before the navigation began.
			time.Sleep(200 * time.Millisecond)
			during = log.text()
			return NavigateTreeResult{}, nil
		}
		// The summary prompt is on screen, and its close requests the frame.
		m.editorContainer.SetChildren(tui.NewText("SUMMARY PROMPT"))
		m.tuiInst.Render()
		log.text()
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
		if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
			t.Fatal(err)
		}
		if during != "" {
			t.Fatalf("a frame ran while the navigation was in progress: %q", during)
		}
		// The repaint replaced the transcript, and the frame that follows shows it.
		<-m.renderWakeCh
		m.runScheduledRender()
		after := log.text()
		if strings.Contains(after, "OLD LEAF") || strings.Contains(after, "SUMMARY PROMPT") || !strings.Contains(after, "─") {
			t.Fatalf("the first frame after the navigation is not the swapped transcript with the editor: %q", after)
		}
	})
}

// A summary makes the status visible, as Pi's requestRender after showStatusIndicator does, so its frames are not held.
func TestTreeSummaryNavigationKeepsPaintingFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, log, h := treeFramesMode(t, "OLD LEAF")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		m.runCtx = ctx
		var during string
		h.navigate = func(context.Context) (NavigateTreeResult, error) {
			time.Sleep(200 * time.Millisecond)
			during = log.text()
			return NavigateTreeResult{}, nil
		}
		if _, err := m.navigateTree(ctx, "target", true, ""); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(during, "Summarizing branch...") {
			t.Fatalf("the summary status was not painted during the navigation: %q", during)
		}
	})
}

// A dialog an extension opens while the navigation waits reaches the screen at once: its keys have nothing else to answer.
func TestTreeNavigationPaintsAnExtensionDialog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, log, h := treeFramesMode(t, "OLD LEAF")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		m.runCtx = ctx
		var during string
		h.navigate = func(context.Context) (NavigateTreeResult, error) {
			if err := m.postToMain(ctx, func() { m.pushExtensionDialog(&extensionDialog{component: tui.NewText("EXTENSION DIALOG")}) }); err != nil {
				return NavigateTreeResult{}, err
			}
			time.Sleep(200 * time.Millisecond)
			during = log.text()
			return NavigateTreeResult{}, nil
		}
		if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(during, "EXTENSION DIALOG") {
			t.Fatalf("the extension dialog was not painted during the navigation: %q", during)
		}
	})
}

// An overlay an extension opens while the navigation waits takes the keys, so it reaches the screen at once as well.
func TestTreeNavigationPaintsAnExtensionOverlay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, log, h := treeFramesMode(t, "OLD LEAF")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		m.runCtx = ctx
		var during string
		h.navigate = func(context.Context) (NavigateTreeResult, error) {
			if err := m.postToMain(ctx, func() { m.tuiInst.ShowOverlay(tui.NewText("EXTENSION OVERLAY"), tui.OverlayOptions{}) }); err != nil {
				return NavigateTreeResult{}, err
			}
			time.Sleep(200 * time.Millisecond)
			during = log.text()
			return NavigateTreeResult{}, nil
		}
		if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(during, "EXTENSION OVERLAY") {
			t.Fatalf("the extension overlay was not painted during the navigation: %q", during)
		}
	})
}

// The selection key that closes the summary prompt paints right after it, before the navigation it starts has run. A choice of the prompt that
// starts the navigation holds that frame, so the prompt stays on screen until the fork point replaces it; a choice that shows another dialog,
// and Esc, paint at once. The arrangement belongs to the prompt's own dialog: another dialog stacked over it, or one a handler opens during the
// navigation, never starts the hold.
func TestTreeSummaryPromptChoiceHoldsTheFramesOfItsClose(t *testing.T) {
	holdFor := func(choice string) bool { return choice != "Summarize with custom prompt" }
	options := []string{"No summary", "Summarize", "Summarize with custom prompt"}
	for _, tc := range []struct {
		name string
		keys []string
		hold bool
	}{
		{"no summary", []string{"\r"}, true},
		{"summarize", []string{"\x1b[B", "\r"}, true},
		{"custom prompt", []string{"\x1b[B", "\x1b[B", "\r"}, false},
		{"escape", []string{"\x1b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newCustomRig(t)
			prompt := &ExtUIContext{m: r.m, commit: holdCommit(r.m, holdFor)}
			result := r.callDialog(t, func() (string, error) {
				return prompt.Select(t.Context(), "Summarize branch?", options, extension.ExtensionUIDialogOptions{})
			})
			dialog := r.m.shownExtensionDialog()
			for _, key := range tc.keys {
				dialog.handle(key)
			}
			if r.m.frameHold != tc.hold {
				t.Fatalf("frames held = %v, want %v", r.m.frameHold, tc.hold)
			}
			<-result
		})
	}
}

func TestTreeDialogsOfOtherContextsNeverStartTheHold(t *testing.T) {
	r := newCustomRig(t)
	prompt := &ExtUIContext{m: r.m, commit: holdCommit(r.m, func(string) bool { return true })}
	promptResult := r.callDialog(t, func() (string, error) {
		return prompt.Select(t.Context(), "Summarize branch?", []string{"No summary"}, extension.ExtensionUIDialogOptions{})
	})
	// An extension's dialog stacked over the prompt, answered while the prompt is still open.
	stacked := r.callDialog(t, r.selectCall(t.Context(), "Proceed?", "Yes", "No"))
	r.m.shownExtensionDialog().handle("\r")
	<-stacked
	if r.m.frameHold {
		t.Fatal("an extension dialog stacked over the summary prompt started the hold")
	}
	if shown := r.m.shownExtensionDialog(); shown == nil {
		t.Fatal("the summary prompt is not shown again once the stacked dialog closed")
	} else {
		shown.handle("\r")
	}
	<-promptResult
	if !r.m.frameHold {
		t.Fatal("the prompt's own choice did not start the hold")
	}
}
