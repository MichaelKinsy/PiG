package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// proxy.ts toolcall_delta sets `arguments: args.append(delta)`: tool arguments are an object, so a streamed value of any other kind (null, 0, false, "", [], 5,
// "s") shows as the empty object. toolcall_end's Object.assign keeps the streamed arguments when the wire call omits them.
func TestProxyNonObjectToolArgumentsFollowPi(t *testing.T) {
	for _, tc := range []struct{ delta, want string }{{`null`, `{}`}, {`0`, `{}`}, {`false`, `{}`}, {`""`, `{}`}, {`[]`, `{}`}, {`5`, `{}`}, {`"s"`, `{}`}} {
		converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolArguments: map[proxyArgumentsKey]*ai.ToolCallArgumentsStream{}}
		for _, event := range []ProxyAssistantMessageEvent{{Type: "start"}, {Type: "toolcall_start", ContentIndex: 0, ID: "call", ToolName: "lookup"}, {Type: "toolcall_delta", ContentIndex: 0, Delta: tc.delta}} {
			if _, err := converter.process(event); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := converter.partial.Content[0].(ai.ToolCall).ArgumentsJSON(); string(got) != tc.want {
			t.Errorf("delta %s: arguments %s, want %s", tc.delta, got, tc.want)
		}
		var end ProxyAssistantMessageEvent
		if err := json.Unmarshal([]byte(`{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"call","name":"lookup"}}`), &end); err != nil {
			t.Fatal(err)
		}
		converted, err := converter.process(end)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := converted.(ai.ToolCallEndEvent).ToolCall.ArgumentsJSON(); string(got) != tc.want {
			t.Errorf("delta %s: toolcall_end without arguments gives %s, want %s", tc.delta, got, tc.want)
		}
	}
}
