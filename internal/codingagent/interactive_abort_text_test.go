package codingagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// abortTextProvider fails its first `overloads` requests with a retryable error, shows "alpha beta " on the next and holds it until the request is
// aborted, which it answers as a provider does: an error event with the partial content, stopReason aborted and its own errorMessage. Every later
// request is answered "done". It keeps the transcript each request received.
type abortTextProvider struct {
	abortText string
	overloads int
	started   chan struct{}
	answered  chan struct{}

	mu          sync.Mutex
	calls       int
	transcripts [][]byte
}

func (p *abortTextProvider) ID() string   { return "faux" }
func (p *abortTextProvider) Close() error { return nil }

func (p *abortTextProvider) Stream(ctx context.Context, request ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	raw, err := json.Marshal(request.Messages())
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.transcripts = append(p.transcripts, raw)
	p.mu.Unlock()
	message := func(text string) *ai.AssistantMessage {
		out := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		if text != "" {
			out.Content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
		}
		return out
	}
	stream := ai.NewAssistantMessageEventStream()
	switch {
	case call <= p.overloads:
		failed := message("")
		failed.StopReason, failed.ErrorMessage = ai.StopReasonError, "503 Service Unavailable: the server is overloaded"
		_ = stream.Push(ai.StartEvent{Partial: failed})
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: failed})
	case call == p.overloads+1:
		_ = stream.Push(ai.StartEvent{Partial: message("")})
		_ = stream.Push(ai.TextStartEvent{ContentIndex: 0, Partial: message("")})
		_ = stream.Push(ai.TextDeltaEvent{ContentIndex: 0, Delta: "alpha beta ", Partial: message("alpha beta ")})
		go func() {
			close(p.started)
			<-ctx.Done()
			aborted := message("alpha beta ")
			aborted.StopReason, aborted.ErrorMessage = ai.StopReasonAborted, p.abortText
			_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
		}()
	default:
		done := message("done")
		_ = stream.Push(ai.StartEvent{Partial: done})
		_ = stream.Push(ai.DoneEvent{Reason: done.StopReason, Message: done})
		close(p.answered)
	}
	return stream, nil
}

// abortedTranscriptText is the errorMessage of the aborted assistant message in the transcript the last request received.
func (p *abortTextProvider) abortedTranscriptText(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	raw := p.transcripts[len(p.transcripts)-1]
	p.mu.Unlock()
	var messages []struct{ Role, StopReason, ErrorMessage string }
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.Role == "assistant" && message.StopReason == "aborted" {
			return message.ErrorMessage
		}
	}
	t.Fatalf("the next request's transcript holds no aborted assistant message: %s", raw)
	return ""
}

// abortTextFixture is a real Session with a session file, abortTextProvider and one in-process extension that records the errorMessage of the
// aborted assistant message as each of its message_end, turn_end and agent_end handlers received it.
type abortTextFixture struct {
	session  *coding.Session
	provider *abortTextProvider
	opts     icodingagent.InteractiveModeOptions

	mu   sync.Mutex
	seen []string
}

func newAbortTextFixture(t *testing.T, abortText string, overloads int) *abortTextFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The automatic retry's first delay is baseDelayMs.
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`), 0o600); err != nil {
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
	f := &abortTextFixture{provider: &abortTextProvider{abortText: abortText, overloads: overloads, started: make(chan struct{}), answered: make(chan struct{})}}
	record := func(event string, pick func(json.RawMessage) []json.RawMessage) extension.HandlerFn {
		return func(args ...any) (any, error) {
			raw, err := json.Marshal(args[0])
			if err != nil {
				return nil, err
			}
			for _, encoded := range pick(raw) {
				var message struct{ Role, StopReason, ErrorMessage string }
				if err := json.Unmarshal(encoded, &message); err != nil {
					return nil, err
				}
				if message.Role == "assistant" && message.StopReason == "aborted" {
					f.mu.Lock()
					f.seen = append(f.seen, event+" "+message.ErrorMessage)
					f.mu.Unlock()
				}
			}
			return nil, nil
		}
	}
	one := func(raw json.RawMessage) []json.RawMessage {
		var event struct{ Message json.RawMessage }
		_ = json.Unmarshal(raw, &event)
		return []json.RawMessage{event.Message}
	}
	all := func(raw json.RawMessage) []json.RawMessage {
		var event struct{ Messages []json.RawMessage }
		_ = json.Unmarshal(raw, &event)
		return event.Messages
	}
	ext := extension.Extension{Name: "abort-text", Handlers: map[string][]extension.HandlerFn{
		"message_end": {record("message_end", one)}, "turn_end": {record("turn_end", one)}, "agent_end": {record("agent_end", all)},
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd)
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: f.provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	f.session, err = coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SessionDir: filepath.Join(home, "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.session.Close() })
	f.opts = icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: f.session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}
	return f
}

func (f *abortTextFixture) await(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%s did not happen", what)
	}
}

// persistedAbortText is the errorMessage of the aborted assistant entry in the session file.
func (f *abortTextFixture) persistedAbortText(t *testing.T) string {
	t.Helper()
	path := f.session.Inner().GetSessionFile()
	if path == nil {
		t.Fatal("the Session has no file")
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		t.Fatal(err)
	}
	found, text := 0, ""
	for line := range strings.Lines(string(data)) {
		var entry struct {
			Type    string
			Message struct{ Role, StopReason, ErrorMessage string }
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("session file line %q: %v", line, err)
		}
		if entry.Type == "message" && entry.Message.Role == "assistant" && entry.Message.StopReason == "aborted" {
			found++
			text = entry.Message.ErrorMessage
		}
	}
	if found != 1 {
		t.Fatalf("the session file holds %d aborted assistant entries, want 1:\n%s", found, data)
	}
	return text
}

func (f *abortTextFixture) extensionSaw() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// Pi's interactive mode replaces the errorMessage of an aborted assistant message inside its message_end listener (interactive-mode.ts:3686-3697),
// on the message object itself. The Session calls that listener after the extension handlers and before it persists the message
// (agent-session.ts:1208-1230), so the session file holds the mode's text, an extension's message_end handler saw the provider's, its turn_end and
// agent_end handlers see the mode's, and so does a provider that reads the transcript of the next request. The text names the automatic retries
// the turn was aborted after. Observed on Pi 1.5.2 for an openai-completions, an anthropic-messages and an extension provider.
func TestInteractiveAbortRewritesTheMessageTheSessionPersists(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		overloads      int
		want           string
	}{
		{"openai shape", "Request was aborted", 0, "Operation aborted"},
		{"anthropic shape", "This operation was aborted", 0, "Operation aborted"},
		{"extension provider's own text", "the author saw the abort", 0, "Operation aborted"},
		{"first automatic retry", "Request was aborted", 1, "Aborted after 1 retry attempt"},
		{"second automatic retry", "Request was aborted", 2, "Aborted after 2 retry attempts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAbortTextFixture(t, tc.provider, tc.overloads)
			h := icodingagent.NewTestHarness(t, f.opts, nil)
			h.Do(func() { h.Enter("HOLD the stream") })
			f.await(t, "the held request", f.provider.started)
			h.Do(func() { h.Key("\x1b") })
			h.WaitIdle(t, testbudget.Wait(t))
			if err := f.session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := f.persistedAbortText(t); got != tc.want {
				t.Errorf("the session file's aborted entry has errorMessage %q, want %q", got, tc.want)
			}
			if got, want := f.extensionSaw(), []string{"message_end " + tc.provider, "turn_end " + tc.want, "agent_end " + tc.want}; !slices.Equal(got, want) {
				t.Errorf("the extension's handlers saw %q, want %q", got, want)
			}
			if chat := h.Chat(); !strings.Contains(chat, tc.want) || strings.Contains(chat, tc.provider) {
				t.Errorf("the transcript should show %q and not %q:\n%s", tc.want, tc.provider, chat)
			}
			h.Do(func() { h.Enter("next please") })
			f.await(t, "the next request", f.provider.answered)
			h.WaitIdle(t, testbudget.Wait(t))
			if got := f.provider.abortedTranscriptText(t); got != tc.want {
				t.Errorf("the next request's transcript carries the aborted message with errorMessage %q, want %q", got, tc.want)
			}
		})
	}
}

// Pi's print, json and rpc modes subscribe no such listener: the session file, every extension handler and the next request keep the provider's
// text (observed on Pi 1.5.2 with SIGTERM, an extension's ctx.abort() and the rpc abort command).
func TestAbortWithoutTheInteractiveModeKeepsTheProvidersText(t *testing.T) {
	for _, provider := range []string{"Request was aborted", "This operation was aborted", "the author saw the abort"} {
		t.Run(provider, func(t *testing.T) {
			f := newAbortTextFixture(t, provider, 0)
			sent := make(chan struct{})
			go func() {
				defer close(sent)
				_, _ = f.session.Send(t.Context(), "HOLD the stream")
			}()
			f.await(t, "the held request", f.provider.started)
			if err := f.session.Abort(t.Context()); err != nil {
				t.Fatal(err)
			}
			f.await(t, "the end of the aborted run", sent)
			if got := f.persistedAbortText(t); got != provider {
				t.Errorf("the session file's aborted entry has errorMessage %q, want the provider's %q", got, provider)
			}
			if got, want := f.extensionSaw(), []string{"message_end " + provider, "turn_end " + provider, "agent_end " + provider}; !slices.Equal(got, want) {
				t.Errorf("the extension's handlers saw %q, want %q", got, want)
			}
		})
	}
}

// Pi's stop() unsubscribes the mode from the Session before it stops the TUI (interactive-mode.ts:7293-7295), and a quit by keys calls stop()
// before it disposes the runtime (:4505-4506): the turn that the dispose aborts is persisted with the provider's text.
func TestInteractiveStopUnsubscribesBeforeTheRuntimeAbortsTheTurn(t *testing.T) {
	f := newAbortTextFixture(t, "Request was aborted", 0)
	h := icodingagent.NewTestHarness(t, f.opts, nil)
	h.Do(func() { h.Enter("HOLD the stream") })
	f.await(t, "the held request", f.provider.started)
	h.Do(h.StopInteractiveTui)
	if err := f.session.Abort(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.persistedAbortText(t); got != "Request was aborted" {
		t.Errorf("the session file's aborted entry has errorMessage %q, want the provider's %q", got, "Request was aborted")
	}
	if got, want := f.extensionSaw(), []string{"message_end Request was aborted", "turn_end Request was aborted", "agent_end Request was aborted"}; !slices.Equal(got, want) {
		t.Errorf("the extension's handlers saw %q, want %q", got, want)
	}
}
