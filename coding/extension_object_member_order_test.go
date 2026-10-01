package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

const extensionObjectValue = `{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`

// A session_before_tree handler's summary `details` is a value the extension wrote; the branch summary entry and the events that report it hold it in that order (types.ts SessionBeforeTreeResult).
func TestExtensionTreeSummaryKeepsDetailsMemberOrder(t *testing.T) {
	var result sessionBeforeTreeResult
	if err := json.Unmarshal([]byte(`{"summary":{"summary":"s","details":`+extensionObjectValue+`}}`), &result); err != nil {
		t.Fatal(err)
	}
	summary, err := extensionTreeSummary(&result)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, err := json.Marshal(summary.Details); err != nil || string(encoded) != extensionObjectValue {
		t.Fatalf("details = %s, %v, want %s", encoded, err, extensionObjectValue)
	}
}

// A before_agent_start handler's message becomes a custom message of the turn (agent-session.ts prompt()); its `details` keeps the order the handler wrote, and its members are written as createCustomMessage builds them (messages.ts:123-137).
func TestQueuedAgentStartMessageKeepsDetailsMemberOrder(t *testing.T) {
	session := &Session{}
	var ref extension.CustomMessageRef
	if err := json.Unmarshal([]byte(`{"customType":"probe","content":"note","display":true,"details":`+extensionObjectValue+`}`), &ref); err != nil {
		t.Fatal(err)
	}
	session.QueueAgentStartMessages([]extension.CustomMessageRef{ref})
	messages := session.takeAgentStartMessages()
	if len(messages) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	encoded, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `{"role":"custom","customType":"probe","content":"note","display":true,"details":` + extensionObjectValue + `,"timestamp":`
	if got := string(encoded); len(got) < len(prefix) || got[:len(prefix)] != prefix {
		t.Fatalf("message = %s, want prefix %s", got, prefix)
	}
}

// A session_before_compact handler's compaction `details` is the value the extension wrote (types.ts SessionBeforeCompactResult); the compaction entry in the session holds it in that order.
func TestExtensionCompactionDetailsKeepMemberOrder(t *testing.T) {
	p := &scriptedProvider{}
	for range 8 {
		p.responses = append(p.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
	}
	model := fakeModelWithProvider(p)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 200000
	model.Capabilities.MaxOutputTokens = 8192
	s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SystemPrompt: "You are a helpful assistant. Be concise."})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	s.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "order", Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			prep := args[0].(extension.SessionBeforeCompactEvent).Preparation.(*compaction.CompactionPreparation)
			return json.RawMessage(`{"compaction":{"summary":"Custom","firstKeptEntryId":"` + prep.FirstKeptEntryID + `","tokensBefore":1,"details":` + extensionObjectValue + `}}`), nil
		}},
	}}}, t.TempDir()))
	for _, prompt := range []string{"What is 2+2? Reply with just the number.", "What is 3+3? Reply with just the number."} {
		if _, err := s.Send(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
		drainEvents(t, s)
	}
	if _, err := s.CompactResult(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	for _, entry := range s.inner.Entries() {
		if entry.Base.Type == "compaction" {
			var compacted struct {
				Details json.RawMessage `json:"details"`
			}
			if err := json.Unmarshal(entry.Raw(), &compacted); err != nil {
				t.Fatal(err)
			}
			if string(compacted.Details) != extensionObjectValue {
				t.Fatalf("compaction details = %s, want %s", compacted.Details, extensionObjectValue)
			}
			return
		}
	}
	t.Fatal("no compaction entry")
}

// A registered tool's `parameters` is the schema object its extension wrote (types.ts ToolDefinition.parameters); the tool declarations a model request and the transcript carry keep its member order, as JSON.stringify of the TypeBox object does.
func TestBridgeToolSchemaKeepsParameterMemberOrder(t *testing.T) {
	const parameters = `{"type":"object","properties":{"text":{"type":"string","description":"t"},"alpha":{"type":"number"}},"required":["text"]}`
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: "probe", Description: "d", Parameters: json.RawMessage(parameters)}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(tool.Schema())
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"probe","description":"d","parameters":` + parameters + `}`; string(encoded) != want {
		t.Fatalf("schema =\n%s\nwant\n%s", encoded, want)
	}
}
