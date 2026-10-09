package codingagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type startupQueueProvider struct {
	seen      chan capturedStreamRequest
	failFirst bool
	calls     atomic.Int32
}

func (*startupQueueProvider) ID() string   { return "startup-queue" }
func (*startupQueueProvider) Close() error { return nil }
func (p *startupQueueProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	if p.calls.Add(1) == 1 && p.failFirst {
		p.seen <- capturedStreamRequest{StreamOptions: options, Messages: transcript.Messages()}
		return nil, errors.New("startup queue provider failure")
	}
	return (captureStreamOptionsProvider{seen: p.seen}).Stream(ctx, transcript, options)
}

// Pi interactive-mode.ts:1185-1192 consumes queued input in order and continues after prompt rejection; initial prompts finish before getUserInput is consumed.
func TestStartupQueuedPromptsReachTheInputLoopInOrder(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failFirst    bool
		inputs, want []string
	}{
		{"ordinary", false, []string{" first queued ", "second queued"}, []string{"first queued", "second queued"}},
		{"first prompt rejects", true, []string{" first queued ", "second queued"}, []string{"first queued", "second queued"}},
		{"bare bash prefixes and ECMAScript whitespace", false, []string{"!", "!!", "\ufefftrimmed\ufeff", "\u0085retained\u0085"}, []string{"!", "!!", "trimmed", "\u0085retained\u0085"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := newPendingDisplayHarness(t)
				m.opts.AgentDir = t.TempDir()
				provider := &startupQueueProvider{seen: make(chan capturedStreamRequest, len(tc.inputs)), failFirst: tc.failFirst}
				model := &ai.Model{ID: "queued", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 800000}}
				m.opts.Model = model
				m.agent = mustNewAgent(agent.AgentOptions{Model: model})
				m.chatContainer, m.statusContainer = tui.NewContainer(), tui.NewContainer()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.runCtx, m.abortCtx = ctx, ctx
				m.inputReadCh, m.inputErrCh = make(chan inputChunk, 1), make(chan error, 1)
				m.setupEditorSubmitHandler(ctx)
				m.installRenderDispatcher()
				unsubscribe := m.agent.Subscribe(func(eventCtx context.Context, event agent.AgentEvent) error {
					return m.postToMain(eventCtx, func() { m.handleAgentEvent(event) })
				})
				defer unsubscribe()
				for _, text := range tc.inputs {
					m.editor.OnSubmit(text)
				}
				// The early inputs precede the initial prompt. The production dispatcher must wait for that captured run before opening the normal input consumer.
				m.turnActive.Store(true)
				m.turnSettled = make(chan struct{})
				initialDone := make(chan struct{})
				m.initialMessagesDone = initialDone
				done := make(chan error, 1)
				go func() { done <- m.inputLoop(ctx, strings.NewReader("")) }()
				synctest.Wait()
				if provider.calls.Load() != 0 || m.onInputCallback != nil {
					t.Error("startup queue dispatched before initial prompts finished")
				}
				m.queueMu.Lock()
				m.turnActive.Store(false)
				close(m.turnSettled)
				m.turnSettled = nil
				m.queueMu.Unlock()
				close(initialDone)
				synctest.Wait()
				if got := int(provider.calls.Load()); got != len(tc.want) {
					t.Fatalf("provider calls=%d, want %d queued prompts", got, len(tc.want))
				}
				for _, want := range tc.want {
					got := <-provider.seen
					if text := lastUserMessageText(t, got.Messages); text != want {
						t.Errorf("prompt=%q want=%q", text, want)
					}
				}
				if tc.failFirst && !strings.Contains(strings.Join(m.chatContainer.Render(120), "\n"), "startup queue provider failure") {
					t.Error("queued prompt rejection was not surfaced")
				}
				cancel()
				m.backgroundTasks.Wait()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				if m.onInputCallback != nil || len(m.pendingUserInputs) != 0 {
					t.Fatal("stopped input owner retained its callback or queued prompts")
				}
			})
		})
	}
}

func TestInitialMessageDispatcherCancellationJoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := newPendingDisplayHarness(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.turnActive.Store(true)
		m.turnSettled = make(chan struct{})
		m.backgroundTasks.Go(func() { m.submitInitialMessages(ctx, []string{"initial prompt"}) })
		synctest.Wait()
		cancel()
		m.backgroundTasks.Wait()
		if len(m.uiTaskCh) != 0 {
			t.Fatal("cancelled positional-prompt dispatcher posted UI work")
		}
	})
}

func TestInteractiveSubmitPreservesECMAScriptWhitespace(t *testing.T) {
	for _, submit := range []string{"\r", hostFollowUpInput()} {
		for _, tc := range []struct{ text, want string }{{"\ufefftrimmed\ufeff", "trimmed"}, {"\u0085retained\u0085", "\u0085retained\u0085"}} {
			m, ctx := newCustomEditorDispatchMode(t)
			t.Cleanup(m.abortFn)
			m.isIdle = true
			m.setupEditorSubmitHandler(ctx)
			m.editor.SetText(tc.text)
			if err := m.dispatchKey(ctx, submit); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(m.pendingUserInputs, []string{tc.want}) {
				t.Fatalf("submit=%q text=%q queued=%q want=%q", submit, tc.text, m.pendingUserInputs, tc.want)
			}
		}
	}
}

func TestRemoteSubmitPreservesECMAScriptWhitespace(t *testing.T) {
	for _, tc := range []struct{ text, want string }{{"\ufefftrimmed\ufeff", "trimmed"}, {"\u0085retained\u0085", "\u0085retained\u0085"}} {
		synctest.Test(t, func(t *testing.T) {
			m, _ := newTickRenderProbe(t, "regular")
			m.setupEditorSubmitHandler(t.Context())
			m.setRemoteEditor(&fakeRemoteEditor{})
			done := make(chan struct{})
			m.remoteEditor.EditorSubmit(tc.text, func() { close(done) })
			synctest.Wait()
			m.drainMainLoopOnce()
			select {
			case <-done:
			default:
				t.Fatal("remote submit did not settle")
			}
			if !slices.Equal(m.pendingUserInputs, []string{tc.want}) {
				t.Fatalf("remote text=%q queued=%q want=%q", tc.text, m.pendingUserInputs, tc.want)
			}
		})
	}
}

func TestRestoreQueuedInputPreservesECMAScriptWhitespace(t *testing.T) {
	for _, tc := range []struct{ draft, want string }{{"\ufeff", "queued"}, {"\u0085", "queued\n\n\u0085"}} {
		m := statusBorderMode(t, false)
		m.compactionQueue = []compactionQueuedMessage{{text: "queued", mode: compactionQueueSteer}}
		m.editor.SetText(tc.draft)
		m.restoreQueuedMessagesToEditor(false)
		if got := m.editor.Text(); got != tc.want {
			t.Fatalf("draft=%q restored=%q want=%q", tc.draft, got, tc.want)
		}
	}
}

func TestStartupQueueDoesNotDispatchAfterOwnerCancellation(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer, m.statusContainer = tui.NewContainer(), tui.NewContainer()
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx, m.abortCtx = ctx, ctx
	m.inputReadCh, m.inputErrCh = make(chan inputChunk, 1), make(chan error, 1)
	m.pendingUserInputs = []string{"cancelled queued prompt"}
	cancel()
	if err := m.inputLoop(ctx, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if m.runGen != 0 {
		t.Fatalf("cancelled startup queue admitted %d turns", m.runGen)
	}
}
