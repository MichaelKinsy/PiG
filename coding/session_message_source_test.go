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
