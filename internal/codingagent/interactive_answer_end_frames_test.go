package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// writeLog records each terminal write the renderer makes, so a test can assert the exact byte stream.
type writeLog struct {
	mu     sync.Mutex
	writes []string
}

func (w *writeLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes = append(w.writes, string(p))
	w.mu.Unlock()
	return len(p), nil
}

func (w *writeLog) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.writes...)
}

type answerProvider struct{}

func (answerProvider) ID() string   { return "faux" }
func (answerProvider) Close() error { return nil }

func (answerProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ANSWER-END"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: message})
	_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	return stream, nil
}

// The frame that draws the end of a reply ends the byte stream. Upstream's agent_end handler requests one frame and agent_settled requests none; the
// pinned Pi 1.5.2, driven over a pty with the same fake-model prompts, writes no frame after the one that carries the end of the reply (the last frame
// ends the capture 400 ms later). PiG drew three more no-change frames that only moved and hid the cursor: two direct renders at the end of the turn
// (updatePendingMessagesDisplay and the turn's final flush, each after a render the turn had just made) and one requested by agent_settled 16 ms later.
func TestInteractiveAnswerEndEmitsNoTrailingFrame(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, cwd)
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: answerProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	out := &writeLog{}
	h := icodingagent.NewTestHarnessWithOutput(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}, nil, out)
	before := len(out.snapshot())
	h.Do(func() { h.Enter("hello") })
	h.WaitIdle(t, 10*time.Second)
	// A frame that is still pending is at most one 16 ms throttle away; wait several of them for a late one.
	time.Sleep(200 * time.Millisecond)
	writes := out.snapshot()[before:]
	last := -1
	for i, w := range writes {
		if strings.Contains(w, "ANSWER-END") {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("the answer was never drawn; writes: %q", writes)
	}
	want := []string{"\x1b[3A\x1b[1G", "\x1b[?25l"}
	if got := writes[last+1:]; !slices.Equal(got, want) {
		t.Fatalf("writes after the write that draws the answer = %q, want only that frame's cursor tail %q", got, want)
	}
}
