package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/test/interactive-mode-status.test.ts:236 through the production remote custom-UI host, not an in-process extension factory. Renderer focus is the host-side observable counterpart of the upstream component's focused flag.
func TestRemoteOverlayReclaimsInputAfterEditorReplacementUpstream(t *testing.T) {
	m := newSwitchTuiProbe(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	palette := tui.NewText("PALETTE")
	m.tuiInst.Add(palette)
	m.tuiInst.SetFocus(palette)
	u := &ExtUIContext{m: m}
	type result struct {
		value any
		ok    bool
	}
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		workers.Wait()
		m.teardownCurrentTui()
		m.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true})
	})
	mount := func(overlay bool, label string) (extension.RemoteOverlayHandle, <-chan result, <-chan string) {
		t.Helper()
		handles := make(chan extension.RemoteOverlayHandle, 1)
		done := make(chan result, 1)
		inputs := make(chan string, 1)
		workers.Go(func() {
			value, ok := u.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: overlay}, overlayInputSpy{onInput: func(data string) { inputs <- data }}, func(handle extension.RemoteOverlayHandle) {
				handle.UpdateLines([]string{label})
				handles <- handle
			})
			done <- result{value, ok}
		})
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case handle := <-handles:
				return handle, done, inputs
			case fn := <-m.uiTaskCh:
				fn()
			case got := <-done:
				t.Fatalf("custom UI ended before mounting: %+v", got)
			case <-timer.C:
				t.Fatal("custom UI did not mount")
			}
		}
	}
	wait := func(done <-chan result, value string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case got := <-done:
				if !got.ok || got.value != value {
					t.Fatalf("custom UI result=%+v, want %q", got, value)
				}
				return
			case fn := <-m.uiTaskCh:
				fn()
			case <-timer.C:
				t.Fatal("custom UI did not close")
			}
		}
	}
	overlay, overlayDone, overlayInputs := mount(true, "OVERLAY")
	overlayFocused := any(m.tuiInst.GetFocusedComponent()) == overlay
	if !overlayFocused {
		t.Fatal("overlay did not receive focus")
	}
	replacement, replacementDone, replacementInputs := mount(false, "REPLACEMENT")
	replacementFocused := any(m.tuiInst.GetFocusedComponent()) == replacement
	if !replacementFocused {
		t.Error("non-overlay custom UI did not receive focus")
	}
	if err := m.dispatchKey(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-replacementInputs:
		if got != "r" {
			t.Fatalf("replacement input=%q, want r", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not receive input while focused")
	}
	replacement.Close("done")
	wait(replacementDone, "done")
	if err := m.dispatchKey(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	inputTimer := time.NewTimer(5 * time.Second)
	defer inputTimer.Stop()
	gotOverlayInput := false
	for !gotOverlayInput {
		select {
		case got := <-overlayInputs:
			if got != "x" {
				t.Fatalf("overlay inputs=%q, want x", got)
			}
			gotOverlayInput = true
		case fn := <-m.uiTaskCh:
			fn()
		case <-inputTimer.C:
			t.Fatal("overlay did not reclaim input after the replacement closed")
		}
	}
	select {
	case got := <-replacementInputs:
		t.Errorf("closed replacement received input %q", got)
	default:
	}
	restoredFocus := any(m.tuiInst.GetFocusedComponent()) == overlay
	if m.editor.Text() != "" || !restoredFocus {
		t.Fatalf("editor/focus after replacement close: editor=%q focus=%T", m.editor.Text(), m.tuiInst.GetFocusedComponent())
	}
	overlay.Close("closed")
	wait(overlayDone, "closed")
	standalone, standaloneDone, _ := mount(false, "STANDALONE")
	standalone.Close("standalone done")
	wait(standaloneDone, "standalone done")
	editorRestored := m.tuiInst.GetFocusedComponent() == m.editor
	if !editorRestored {
		t.Error("standalone custom UI did not restore editor focus before returning")
	}
	observation := struct {
		OverlayFocused     bool   `json:"overlayFocused"`
		ReplacementFocused bool   `json:"replacementFocused"`
		RestoredFocus      bool   `json:"restoredFocus"`
		EditorRestored     bool   `json:"editorRestored"`
		Editor             string `json:"editor"`
	}{overlayFocused, replacementFocused, restoredFocus, editorRestored, m.editor.Text()}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("custom-focus-observation:%s", encoded)
}

func BenchmarkRemoteCustomFocusLifecycle(b *testing.B) {
	for _, rows := range []int{10, 1000} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			m, terminal := newTickRenderProbe(b, "regular")
			m.runCtx = nil // The benchmark itself is the owner; setup and cleanup execute inline.
			for range rows {
				m.chatContainer.Add(tui.NewText("retained transcript"))
			}
			m.tuiInst.Render()
			terminal.take()
			u := &ExtUIContext{m: m}
			b.ReportAllocs()
			for b.Loop() {
				value, ok := u.RunRemoteOverlay(extension.RemoteOverlayOptions{}, nil, func(handle extension.RemoteOverlayHandle) { handle.Close("done") })
				if !ok || value != "done" || m.tuiInst.GetFocusedComponent() != m.editor {
					b.Fatal("custom UI did not complete with editor focus")
				}
				m.drainMainLoopOnce()
				terminal.take()
			}
		})
	}
}
