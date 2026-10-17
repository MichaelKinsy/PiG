// Ports packages/durable/test/system-order-cache-e2e.test.ts.

package harness

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// committedOrder puts the leading system message back after the user messages before it: the request order before
// #10542 (system-order-cache-e2e.test.ts:57-63).
func committedOrder(messages []ai.Message) []ai.Message {
	if len(messages) == 0 {
		return append([]ai.Message{}, messages...)
	}
	if _, ok := messages[0].(ai.SystemMessage); !ok {
		return append([]ai.Message{}, messages...)
	}
	rest := messages[1:]
	at := len(rest)
	for index, message := range rest {
		if _, ok := message.(ai.UserMessage); !ok {
			at = index
			break
		}
	}
	ordered := append([]ai.Message{}, rest[:at]...)
	ordered = append(ordered, messages[0])
	return append(ordered, rest[at:]...)
}

// The live case sets the legacy request order with committedOrder to measure the cache without #10542's fix; this checks
// the helper itself, offline: the leading system message moves behind the user messages that precede the first
// non-user message, and a request without a leading system message is unchanged.
func TestCommittedOrderPutsTheLeadingSystemMessageBehindTheLeadingUsers(t *testing.T) {
	user := func(text string) ai.Message { return ai.UserMessage{Content: ai.UserText(text)} }
	system := ai.Message(ai.SystemMessage{Content: ai.SystemText("rules")})
	assistant := ai.Message(ai.AssistantMessage{StopReason: ai.StopReasonStop})
	for name, c := range map[string]struct{ in, want []ai.Message }{
		"users then assistant": {[]ai.Message{system, user("a"), user("b"), assistant}, []ai.Message{user("a"), user("b"), system, assistant}},
		"only users":           {[]ai.Message{system, user("a")}, []ai.Message{user("a"), system}},
		"system alone":         {[]ai.Message{system}, []ai.Message{system}},
		"no leading system":    {[]ai.Message{user("a"), system}, []ai.Message{user("a"), system}},
		"empty":                {[]ai.Message{}, []ai.Message{}},
	} {
		if got := committedOrder(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: committedOrder = %v, want %v", name, got, c.want)
		}
	}
}
