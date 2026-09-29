package codingagent

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// Pi installs the ordinary submit handler after managed-tool setup and continues servicing editor input while later startup awaits complete (interactive-mode.ts:1027 and startup-input.test.ts:69-88). The pending callback must not freeze visible typeahead in the mounted editor.
func TestStartupAwaitServicesVisibleTypeahead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newCustomEditorDispatchMode(t)
		m.tuiInst.Add(m.editor)
		t.Cleanup(m.abortFn)
		m.runCtx = ctx
		m.isIdle = true
		m.inputReadCh = make(chan inputChunk, 1)
		m.inputErrCh = make(chan error, 1)
		m.beginStartupSubmitWindow()
		m.endStartupSubmitWindow()
		m.inputReadCh <- inputChunk{data: []byte("early prompt")}
		until := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- m.inputLoopUntil(ctx, strings.NewReader(""), until) }()
		synctest.Wait()
		if got := m.editor.Text(); got != "early prompt" {
			t.Errorf("startup await froze visible typeahead: editor=%q, want early prompt", got)
		}
		close(until)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestStartupAwaitQueuesSubmittedInput(t *testing.T) {
	for _, submit := range []string{"\r", hostFollowUpInput()} {
		t.Run(submit, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, ctx := newCustomEditorDispatchMode(t)
				t.Cleanup(m.abortFn)
				m.runCtx = ctx
				m.isIdle = true
				m.tuiInst.Add(m.editor)
				m.endStartupSubmitWindow()
				m.inputReadCh = make(chan inputChunk, 3)
				m.inputErrCh = make(chan error, 1)
				for _, data := range []string{" early prompt ", submit, "draft"} {
					m.inputReadCh <- inputChunk{data: []byte(data)}
				}
				until := make(chan struct{})
				done := make(chan error, 1)
				go func() { done <- m.inputLoopUntil(ctx, strings.NewReader(""), until) }()
				synctest.Wait()
				if !slices.Equal(m.pendingUserInputs, []string{"early prompt"}) || m.editor.Text() != "draft" || m.onInputCallback != nil {
					t.Errorf("awaited startup: queued=%q draft=%q callback=%v", m.pendingUserInputs, m.editor.Text(), m.onInputCallback != nil)
				}
				close(until)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if got := <-m.getUserInput(); got != "early prompt" || m.editor.Text() != "draft" {
					t.Fatalf("resumed input=%q draft=%q", got, m.editor.Text())
				}
			})
		})
	}
}

func TestGetUserInputCallbackIsOneShot(t *testing.T) {
	m, _ := newCustomEditorDispatchMode(t)
	t.Cleanup(m.abortFn)
	m.setupEditorSubmitHandler(t.Context())
	first := m.getUserInput()
	m.editor.OnSubmit("first")
	m.editor.OnSubmit("second")
	if got := <-first; got != "first" || m.onInputCallback != nil {
		t.Fatalf("first=%q callback retained=%v", got, m.onInputCallback != nil)
	}
	if got := <-m.getUserInput(); got != "second" || len(m.pendingUserInputs) != 0 || m.onInputCallback != nil {
		t.Fatalf("second=%q queue=%q callback=%v", got, m.pendingUserInputs, m.onInputCallback != nil)
	}
}

func TestStartupInputConsumerWaitsForInitialMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, ctx := newCustomEditorDispatchMode(t)
		t.Cleanup(m.abortFn)
		m.runCtx = ctx
		m.inputReadCh = make(chan inputChunk)
		m.inputErrCh = make(chan error, 1)
		initialDone := make(chan struct{})
		m.initialMessagesDone = initialDone
		done := make(chan error, 1)
		go func() { done <- m.inputLoop(ctx, strings.NewReader("")) }()
		synctest.Wait()
		if m.onInputCallback != nil {
			t.Error("user input consumer overtook the positional prompts")
		}
		close(initialDone)
		synctest.Wait()
		if m.onInputCallback == nil {
			t.Error("settled positional prompts did not release user input")
		}
		m.inputErrCh <- io.EOF
		if err := <-done; !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if m.onInputCallback != nil {
			t.Fatal("input loop exit retained a callback")
		}
	})
}

func BenchmarkStartupInputHandoff(b *testing.B) {
	m, terminal := newTickRenderProbe(b, "regular")
	m.isIdle = true
	m.setupEditorSubmitHandler(b.Context())
	b.ReportAllocs()
	for b.Loop() {
		input := m.getUserInput()
		m.editor.SetText("early prompt")
		if err := m.dispatchKey(b.Context(), "\r"); err != nil {
			b.Fatal(err)
		}
		if got := <-input; got != "early prompt" {
			b.Fatal(got)
		}
		m.drainMainLoopOnce()
		terminal.take()
	}
}

func TestRemoteEditorQueuesStartupSubmit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newTickRenderProbe(t, "regular")
		m.setupEditorSubmitHandler(t.Context())
		m.setRemoteEditor(&fakeRemoteEditor{})
		done := make(chan struct{})
		m.remoteEditor.EditorSubmit(" early prompt ", func() { close(done) })
		synctest.Wait()
		m.drainMainLoopOnce()
		select {
		case <-done:
		default:
			t.Fatal("remote submit did not settle")
		}
		if !slices.Equal(m.pendingUserInputs, []string{"early prompt"}) {
			t.Fatalf("remote startup submit bypassed queue: %q", m.pendingUserInputs)
		}
	})
}
