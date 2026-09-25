package codingagent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func newPendingDisplayHarness(t *testing.T) *InteractiveMode {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.statusLine = NewStatusLine(model, "", nil)
	m.editor = tui.NewEditor()
	m.keybindings = otherColumnKeys()
	return m
}

func renderPending(m *InteractiveMode) string {
	return strings.Join(m.pendingMessagesContainer.Render(100), "\n")
}

// TestPendingDisplay_ShowsCompactionQueuedSteeringMessage reproduces the /compact +
// queued-message path for regular Enter: a message typed during an in-flight
// compaction must stay visible in the pending container as a dim "Steering:"
// line plus the dequeue hint, mirroring upstream getAllQueuedMessages.
func TestPendingDisplay_ShowsCompactionQueuedSteeringMessage(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.compactionQueue = []compactionQueuedMessage{{text: "also run the integration suite", mode: compactionQueueSteer}}

	m.updatePendingMessagesDisplay()
	out := renderPending(m)

	if !strings.Contains(out, "Steering: also run the integration suite") {
		t.Fatalf("compaction-queued message missing from pending display:\n%q", out)
	}
	if !strings.Contains(out, "to edit all queued messages") {
		t.Fatalf("dequeue hint missing from pending display:\n%q", out)
	}
}

func TestPendingDisplay_ShowsCompactionQueuedFollowUpMessage(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.compactionQueue = []compactionQueuedMessage{{text: "follow-up after compact", mode: compactionQueueFollowUp}}

	m.updatePendingMessagesDisplay()
	out := renderPending(m)

	if !strings.Contains(out, "Follow-up: follow-up after compact") {
		t.Fatalf("compaction follow-up missing from pending display:\n%q", out)
	}
	if strings.Contains(out, "Steering: follow-up after compact") {
		t.Fatalf("compaction follow-up rendered as steering:\n%q", out)
	}
}

type blockingProvider struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	blockOnce   sync.Once
}

func (p *blockingProvider) ID() string   { return "blocking" }
func (p *blockingProvider) Close() error { return nil }
func (p *blockingProvider) Stream(_ context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	partial := &ai.AssistantMessage{Provider: p.ID(), Model: "m", StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Provider: p.ID(), Model: "m", StopReason: ai.StopReasonStop}
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		return nil, err
	}
	p.startedOnce.Do(func() { close(p.started) })
	shouldBlock := false
	p.blockOnce.Do(func() { shouldBlock = true })
	go func() {
		if shouldBlock {
			<-p.release
		}
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
	}()
	return stream, nil
}

func TestPendingDisplay_EnterWhileWorkingQueuesSteering(t *testing.T) {
	m := newPendingDisplayHarness(t)
	provider := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	m.agent = agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "m", Provider: provider}})
	ctx := t.Context()
	result := make(chan error, 1)
	go func() {
		_, err := m.agent.Send(ctx, "initial")
		result <- err
	}()
	<-provider.started
	m.isIdle = false
	m.editor.SetText("check this while you work")

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}

	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 1 {
		t.Fatalf("steering queue length = %d, want 1", len(steering))
	}
	if len(followUps) != 0 {
		t.Fatalf("follow-up queue length = %d, want 0", len(followUps))
	}
	if got := extractAgentMessageText(steering[0]); got != "check this while you work" {
		t.Fatalf("steering text = %q", got)
	}
	if got := strings.TrimSpace(m.editor.Text()); got != "" {
		t.Fatalf("editor text after queue = %q, want empty", got)
	}

	out := renderPending(m)
	if !strings.Contains(out, "Steering: check this while you work") {
		t.Fatalf("working Enter did not render steering queue:\n%q", out)
	}
	close(provider.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPendingDisplay_EnterAfterAgentStoppedStartsNewTurn(t *testing.T) {
	m := newPendingDisplayHarness(t)
	provider := &blockingProvider{started: make(chan struct{}), release: make(chan struct{})}
	close(provider.release)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "m", Provider: provider}})
	m.chatContainer = tui.NewContainer()
	m.abortCtx = context.Background()
	m.runCtx = context.Background()
	m.isIdle = false
	m.editor.SetText("new turn after stale working flag")

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}

	steering, followUps := m.agent.PendingMessages()
	if len(steering)+len(followUps) != 0 {
		t.Fatalf("stale non-idle submit must not orphan queues: steering=%d followUps=%d", len(steering), len(followUps))
	}
}

func TestPendingDisplay_AltEnterDuringCompactionQueuesFollowUp(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.isIdle = true
	m.isCompacting = true
	m.editor.SetText("follow-up after compact")

	if err := m.dispatchKey(context.Background(), "\x1b[13;3u"); err != nil {
		t.Fatal(err)
	}
	out := renderPending(m)

	if !strings.Contains(out, "Follow-up: follow-up after compact") {
		t.Fatalf("Alt+Enter during compaction should render as follow-up, not steering:\n%q", out)
	}
	if strings.Contains(out, "Steering: follow-up after compact") {
		t.Fatalf("Alt+Enter during compaction rendered as steering:\n%q", out)
	}
}

// TestPendingDisplay_TruncatesToOneLine locks upstream's TruncatedText(text,
// 1, 0): a long queued message renders as a single truncated line, never
// wrapped across rows.
func TestPendingDisplay_TruncatesToOneLine(t *testing.T) {
	m := newPendingDisplayHarness(t)
	long := strings.Repeat("wrap ", 60) // ~300 cols, far wider than 100
	m.compactionQueue = []compactionQueuedMessage{{text: strings.TrimSpace(long), mode: compactionQueueSteer}}

	m.updatePendingMessagesDisplay()
	rows := m.pendingMessagesContainer.Render(100)

	// Rows: Spacer(1), the steering line, the hint. The steering line must
	// be a single row and end in the truncation ellipsis.
	steeringRows := 0
	for _, r := range rows {
		if strings.Contains(r, "Steering:") {
			steeringRows++
			if !strings.Contains(r, "...") {
				t.Fatalf("long message not truncated (no ellipsis):\n%q", r)
			}
		}
	}
	if steeringRows != 1 {
		t.Fatalf("steering message spans %d rows, want 1 (should truncate not wrap)", steeringRows)
	}
}

// TestPendingDisplay_ClearsWhenEmpty confirms an empty queue renders nothing
// (no stray spacer/hint), so the display collapses once messages flush.
func TestPendingDisplay_ClearsWhenEmpty(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.compactionQueue = nil

	m.updatePendingMessagesDisplay()
	if out := renderPending(m); strings.TrimSpace(out) != "" {
		t.Fatalf("empty queue should render nothing, got:\n%q", out)
	}
}

// TestPendingDisplay_ClearsSteeringOnInjectedUserMessage verifies that when a
// queued steering message is consumed and injected as a user turn, the pending
// display refreshes on message_start rather than lingering until agent_end.
// Regression for stale "Steering:" lines that stayed visible after injection.
func TestPendingDisplay_ClearsSteeringOnInjectedUserMessage(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()

	steer := agent.AgentMessage{User: &agent.UserMessage{
		Role:    agent.RoleUser,
		Content: []ai.UserContentBlock{ai.TextContent{Text: "steer now"}},
	}}
	m.agent.Steer(steer)
	m.updatePendingMessagesDisplay()
	if out := renderPending(m); !strings.Contains(out, "Steering: steer now") {
		t.Fatalf("queued steering message not shown before injection:\n%q", out)
	}

	// The agent drains the queue as it injects the message; simulate that, then
	// drive the message_start(user) the agent emits for the injected turn.
	m.agent.ClearSteeringQueue()
	m.handleAgentEvent(agent.MessageStartEvent{Message: steer})

	if out := renderPending(m); strings.Contains(out, "Steering:") {
		t.Fatalf("pending steering display lingered after injection:\n%q", out)
	}
}
