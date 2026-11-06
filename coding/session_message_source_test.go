package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// A message_end replacement replaces the message's members with the replacement's, in its order (agent-session.ts _replaceMessageInPlace deletes every key, then assigns the replacement's); null content becomes [] in its place ({ ...replacement, content: [] }).
func TestMessageEndReplacementKeepsItsMemberOrder(t *testing.T) {
	const replacement = `{"stopReason":"stop","content":null,"role":"assistant","api":"faux","provider":"faux","model":"faux-1","usage":{"totalTokens":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5,"note":"kept"}`
	// node -e 'const r=JSON.parse(process.argv[1]); console.log(JSON.stringify({...r, content: []}))'
	const want = `{"stopReason":"stop","content":[],"role":"assistant","api":"faux","provider":"faux","model":"faux-1","usage":{"totalTokens":0,"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"timestamp":5,"note":"kept"}`
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"message_end": {func(args ...any) (any, error) {
		if args[0].(extension.MessageEndEvent).Message.Assistant == nil {
			return nil, nil
		}
		var value extension.AgentMessage
		if err := json.Unmarshal([]byte(replacement), &value); err != nil {
			return nil, err
		}
		return &extension.MessageEndEventResult{Message: &value}, nil
	}}}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("hello", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	messages := laxMessagesWithRole(h.session, agent.RoleAssistant)
	if len(messages) != 1 {
		t.Fatalf("assistant messages=%d want 1", len(messages))
	}
	encoded, err := json.Marshal(messages[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonstringify.Canonicalize(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("replaced message\n got %s\nwant %s", got, want)
	}
}

// A message_end replacement of a custom message that has no content gets content after its members, as {...replacement, content: []} writes it
// (agent-session.ts _emitExtensionEvent), in the message the session holds and in the event the listeners and a JSON consumer read; the same
// replacement keeps its own member order whatever the original's was (_replaceMessageInPlace assigns the replacement's keys to the emptied object).
func TestMessageEndCustomReplacementWithoutContentPutsContentLast(t *testing.T) {
	for _, tc := range []struct {
		name, replacement, want string
	}{
		// node -e 'const r=JSON.parse(process.argv[1]); console.log(JSON.stringify({...r, content: []}))'
		{"canonical members", `{"role":"custom","customType":"role-context","display":false,"timestamp":7}`, `{"role":"custom","customType":"role-context","display":false,"timestamp":7,"content":[]}`},
		{"other member order", `{"timestamp":7,"display":false,"customType":"role-context","role":"custom"}`, `{"timestamp":7,"display":false,"customType":"role-context","role":"custom","content":[]}`},
		{"null content keeps its place", `{"role":"custom","content":null,"customType":"role-context","display":false,"timestamp":7}`, `{"role":"custom","content":[],"customType":"role-context","display":false,"timestamp":7}`},
		{"content given keeps the replacement order", `{"timestamp":7,"content":"x","role":"custom","customType":"role-context","display":false}`, `{"timestamp":7,"content":"x","role":"custom","customType":"role-context","display":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ended []agent.AgentMessage
			ext := extension.Extension{Path: "/ext/custom", Handlers: map[string][]extension.HandlerFn{
				"before_agent_start": {func(...any) (any, error) {
					return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "role-context", Content: "INJECTED-CONTEXT"}}, nil
				}},
				"message_end": {func(args ...any) (any, error) {
					if args[0].(extension.MessageEndEvent).Message.Custom == nil {
						return nil, nil
					}
					var value extension.AgentMessage
					if err := json.Unmarshal([]byte(tc.replacement), &value); err != nil {
						return nil, err
					}
					return &extension.MessageEndEventResult{Message: &value}, nil
				}},
			}}
			h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("ok", ai.StopReasonStop, 0))
			if _, err := h.session.Send(t.Context(), "go"); err != nil {
				t.Fatal(err)
			}
			for _, event := range h.settle(t) {
				if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Custom != nil {
					ended = append(ended, end.Message)
				}
			}
			held := laxMessagesWithRole(h.session, agent.RoleCustom)
			if len(held) != 1 || len(ended) != 1 {
				t.Fatalf("custom messages held=%d, in message_end events=%d, want 1 each", len(held), len(ended))
			}
			for label, message := range map[string]agent.AgentMessage{"the session's message": held[0], "the message_end event": ended[0]} {
				encoded, err := json.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				got, err := jsonstringify.Canonicalize(encoded)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Errorf("%s\n got %s\nwant %s", label, got, tc.want)
				}
			}
		})
	}
}
