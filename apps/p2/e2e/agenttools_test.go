// Helpers of the agent-tools cases (subagents, agent, code mode): conversation views and the fake provider's flows.

package e2e_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	// The plugins of the agent tools: the coding tools, code mode, the one-shot agent and PowerShell.
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/agent"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell"
)

// messages are the model messages of a conversation view's entries, in order.
func messages(view delta.JsonValue) []*delta.JsonObject {
	var out []*delta.JsonObject
	for _, entry := range list(object(view).Value("entries")) {
		for _, message := range list(object(entry).Value("model")) {
			out = append(out, object(message))
		}
	}
	return out
}

// textsOf are the text parts of every message of view (a string content counts as one).
func textsOf(view delta.JsonValue) []string {
	var out []string
	for _, message := range messages(view) {
		switch content := message.Value("content").(type) {
		case string:
			out = append(out, content)
		case []any:
			for _, part := range content {
				if object(part).Value("type") == "text" {
					text, _ := object(part).Value("text").(string)
					out = append(out, text)
				}
			}
		}
	}
	return out
}

func hasText(view delta.JsonValue, wanted func(string) bool) bool {
	for _, text := range textsOf(view) {
		if wanted(text) {
			return true
		}
	}
	return false
}

// agentResultText is a tool result's text.
func agentResultText(result *delta.JsonObject) string {
	if result == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range list(result.Value("content")) {
		if object(part).Value("type") == "text" {
			s, _ := object(part).Value("text").(string)
			text.WriteString(s)
		}
	}
	return text.String()
}

// subagentResult is the newest result of the subagent tool's action.
func subagentResult(view delta.JsonValue, action string) *delta.JsonObject {
	var found *delta.JsonObject
	for _, result := range toolResults(view, "subagent") {
		if object(result.Value("details")).Value("action") == action {
			found = result
		}
	}
	return found
}

func idle(view delta.JsonValue) bool { return view != nil && !running(view) }

// agentFixture is a fixture with an owner's session and its chat of conversation 1.
type agentFixture struct {
	t       *testing.T
	Fixture *p2test.Fixture
	Owner   host.Host
	Session string
	Chat    *host.Reference
	sent    int
}

func newAgentFixture(t *testing.T, options hosts.ServerOptions) *agentFixture {
	t.Helper()
	p2test.NoLeakedGoroutines(t)
	f := &agentFixture{t: t, Fixture: p2test.StartFixture(t, options)}
	f.Owner = f.Fixture.Browser()
	sessions, err := p2test.HoldIn(f.Owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", f.Fixture.Workspace), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	f.Session = object(created).Value("id").(string)
	f.Chat = f.hold(1)
	return f
}

func (f *agentFixture) hold(conversation float64) *host.Reference {
	f.t.Helper()
	ref, err := p2test.HoldIn(f.Owner, chatcontract.Chat, delta.JsonObjectOf("session", f.Session, "conversation", conversation), "participant")
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

// send sends text to the main conversation under a fresh request ID.
func (f *agentFixture) send(text string) {
	f.t.Helper()
	f.sent++
	if _, err := f.Chat.Call("send", delta.JsonObjectOf("text", text, "requestId", "r"+itoa(f.sent)), p2test.Background); err != nil {
		f.t.Fatal(err)
	}
}

func (f *agentFixture) until(source p2test.JSONSource, predicate func(delta.JsonValue) bool, timeout ...time.Duration) delta.JsonValue {
	f.t.Helper()
	limit := time.Duration(0)
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	value, err := p2test.Until(source, predicate, limit)
	if err != nil {
		f.t.Fatal(err)
	}
	return value
}

func jsonOf(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return value
}

func marshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func decode(t *testing.T, text string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), into); err != nil {
		t.Fatalf("%q: %v", text, err)
	}
}
