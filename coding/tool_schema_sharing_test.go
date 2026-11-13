package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// schemaScaleProvider answers every request with one text reply.
type schemaScaleProvider struct{}

func (schemaScaleProvider) ID() string { return "fake" }
func (schemaScaleProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := sessionTestMessage("fake", "ok", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}
func (schemaScaleProvider) Close() error { return nil }

// schemaScaleParameters is a JSON schema of the given number of string properties, each with a description as long as a real tool's.
func schemaScaleParameters(properties int) []byte {
	members := make([]string, 0, properties)
	for i := range properties {
		members = append(members, fmt.Sprintf(`"field%d":{"type":"string","description":"argument %d of the extension tool, with a description of realistic length for a schema"}`, i, i))
	}
	return []byte(`{"type":"object","properties":{` + strings.Join(members, ",") + `},"required":["field0"]}`)
}

// lastRequestProvider answers every request with one text reply and keeps the last request's transcript as JSON, with the tool
// declarations the transcript holds.
type lastRequestProvider struct {
	mu   sync.Mutex
	last string
}

func (p *lastRequestProvider) ID() string { return "fake" }
func (p *lastRequestProvider) Stream(ctx context.Context, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	raw, _ := json.Marshal(request.Messages())
	p.mu.Lock()
	p.last = string(raw)
	p.mu.Unlock()
	return schemaScaleProvider{}.Stream(ctx, request, options)
}
func (p *lastRequestProvider) Close() error { return nil }
func (p *lastRequestProvider) lastRequest() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// variantSchema is a schema whose one description names the variant, so a request shows which registration it declared.
func variantSchema(variant int) []byte {
	return []byte(fmt.Sprintf(`{"type":"object","properties":{"field0":{"type":"string","description":"variant %d"},"extra%d":{"type":"integer"}},"required":["field0"]}`, variant, variant))
}

// TestToolSchemaReRegistrationDuringPromptsIsRaceFreeAndVisible: an extension re-registers a tool with a changed schema 200 times
// while prompts, and compactions of what they said, run on the same session. Sharing a tool's parameters between the transcript, the
// loadout comparison, the compaction estimate and the preparation must leave every one of those readers with a schema nobody writes
// (this test is meaningful under -race), and the last registration must reach the next request through its re-declaration, as Pi's
// loop re-compares the live tools before every request (agent-loop.ts:342).
func TestToolSchemaReRegistrationDuringPromptsIsRaceFreeAndVisible(t *testing.T) {
	services := newTestServicesSmallKeep(t)
	definition := func(variant int) extension.RegisteredTool {
		def := registryTool("ext_a", "ext_a", "extension tool", "Run ext_a")
		def.Parameters = variantSchema(variant)
		return extension.RegisteredTool{Definition: def}
	}
	ext := extension.Extension{Path: "<inline:race>", Tools: map[string]extension.RegisteredTool{"ext_a": definition(0)}, ToolOrder: []string{"ext_a"}}
	ext.InitializeToolRegistry()
	provider := &lastRequestProvider{}
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider), NoSession: true, Runner: inproc.NewRunner([]extension.Extension{ext}, services.CWD())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	const registrations = 200
	// Each registration waits for a prompt to finish, so the refresh that follows runs while the next prompt does.
	done, step := make(chan error, 1), make(chan struct{})
	go func() {
		for variant := 1; variant <= registrations; variant++ {
			<-step
			ext.SetRegisteredTool(definition(variant))
			if err := session.RefreshTools(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; ; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Send(t.Context(), "after the last registration"); err != nil {
				t.Fatal(err)
			}
			if request := provider.lastRequest(); !strings.Contains(request, fmt.Sprintf("variant %d", registrations)) || !strings.Contains(request, fmt.Sprintf("extra%d", registrations)) {
				t.Fatalf("the request after the last registration does not declare it: %.600s", request)
			}
			return
		default:
		}
		if _, err := session.Send(t.Context(), fmt.Sprintf("prompt %d", i)); err != nil {
			t.Fatal(err)
		}
		select {
		case step <- struct{}{}:
		case err := <-done:
			done <- err
		}
		if i%4 == 3 {
			// Nothing to compact is an answer, not a failure.
			if _, err := session.Compact(t.Context(), ""); err != nil && !strings.Contains(err.Error(), "Already compacted") && !strings.Contains(err.Error(), "Nothing to compact") {
				t.Fatal(err)
			}
		}
	}
}

// TestBeforeCompactHookEditsDoNotReachTheSession: the preparation a session_before_compact handler receives is the handler's own;
// whatever it edits (a user message's text, an assistant message's blocks) leaves the session's entries and the messages of the next
// request as they were. The preparation reads the session's own messages and copies only the ones it hands out, so this is the
// guard for that copy.
func TestBeforeCompactHookEditsDoNotReachTheSession(t *testing.T) {
	provider := &scriptedProvider{}
	for range 12 {
		provider.responses = append(provider.responses, fauxReply("a reply to keep", ai.StopReasonStop, 0))
	}
	model := fakeModelWithProvider(provider)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 200000
	model.Capabilities.MaxOutputTokens = 8192
	session, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SystemPrompt: "You are a helpful assistant."})
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Events()
	t.Cleanup(func() { _ = session.Close() })
	edited := 0
	handler := func(args ...any) (any, error) {
		prep := args[0].(extension.SessionBeforeCompactEvent).Preparation
		for _, list := range [][]agent.AgentMessage{prep.MessagesToSummarize, prep.TurnPrefixMessages} {
			for _, message := range list {
				switch {
				case message.User != nil:
					if blocks, ok := message.User.Content.(ai.UserContentBlocks); ok && len(blocks) > 0 {
						blocks[0] = ai.TextContent{Text: "edited by the handler"}
						edited++
					}
				case message.Assistant != nil && len(message.Assistant.Content) > 0:
					message.Assistant.Content[0] = ai.TextContent{Text: "edited by the handler"}
					edited++
				}
			}
		}
		return nil, nil
	}
	session.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "hook", Handlers: map[string][]extension.HandlerFn{"session_before_compact": {handler}}}}, t.TempDir()))
	for _, prompt := range []string{"first question", "second question", "third question"} {
		if _, err := session.Send(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
		drainEvents(t, session)
	}
	entriesJSON := func() []string {
		var out []string
		for _, entry := range session.inner.Load().GetEntries() {
			out = append(out, string(entry.Raw()))
		}
		return out
	}
	before := entriesJSON()
	if _, err := session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if edited == 0 {
		t.Fatal("the handler edited nothing: the preparation held no message to summarize")
	}
	after := entriesJSON()
	if len(after) != len(before)+1 {
		t.Fatalf("%d entries before the compaction, %d after: want one more", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("the handler's edits reached entry %d:\n before %s\n after  %s", i, before[i], after[i])
		}
	}
	for _, entry := range after {
		if strings.Contains(entry, "edited by the handler") && !strings.Contains(entry, `"type":"compaction"`) {
			t.Fatalf("an entry holds the handler's edit: %s", entry)
		}
	}
}
