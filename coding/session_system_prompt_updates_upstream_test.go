package coding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func requestSystemMessages(request []ai.Message) []ai.SystemMessage {
	var systems []ai.SystemMessage
	for _, message := range request {
		if system, ok := message.(ai.SystemMessage); ok {
			systems = append(systems, system)
		}
	}
	return systems
}

// system-prompt-updates.test.ts:112-168: the forced turns send one leading system message made of the forced text and the head's tool declarations and timestamp, in the same transcript order as an unforced turn, and the fourth turn passes the recorded head and both plan_mode patches through.
func TestForcedPromptRequestShapeMatchesPi(t *testing.T) {
	turn := 0
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
		turn++
		if turn == 3 {
			options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
			*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
		}
		if turn == 2 || turn == 3 {
			return &extension.BeforeAgentStartEventResult{SystemPrompt: new("Exact prompt.")}, nil
		}
		return nil, nil
	}}}}
	var requests [][]ai.Message
	var responses []scriptedResponse
	for _, text := range []string{"one", "two", "three", "four"} {
		responses = append(responses, func(request []ai.Message) *ai.AssistantMessage {
			requests = append(requests, request)
			return fauxReply(text, ai.StopReasonStop, 0)(request)
		})
	}
	h := newRecoveryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{boundaryTool{name: "noop", label: "Noop", description: "Noop", text: "done"}}}, responses...)
	for _, text := range []string{"one", "two", "three", "four"} {
		if err := h.session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}
	systems := make([][]ai.SystemMessage, len(requests))
	for i, request := range requests {
		systems[i] = requestSystemMessages(request)
	}
	// Forced turns collapse to one leading message; the unforced fourth turn passes the recorded head and both plan_mode patches through.
	counts := make([]int, len(systems))
	for i, messages := range systems {
		counts[i] = len(messages)
	}
	if want := []int{1, 1, 1, 3}; !reflect.DeepEqual(counts, want) {
		t.Fatalf("system messages per request = %v, want %v", counts, want)
	}
	head := systems[0][0]
	if len(head.ToolsAdded) == 0 {
		t.Fatal("the head declares no tools, so the forced message's toolsAdded is not exercised")
	}
	forced := ai.SystemMessage{Content: ai.SystemText("Exact prompt."), ToolsAdded: head.ToolsAdded, Timestamp: head.Timestamp}
	wire := func(message ai.SystemMessage) string {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	if got := systems[1][len(systems[1])-1]; wire(got) != wire(forced) {
		t.Errorf("second request's system message = %s, want %s", wire(got), wire(forced))
	}
	if got := systems[2][len(systems[2])-1]; wire(got) != wire(forced) {
		t.Errorf("third request's system message = %s, want %s", wire(got), wire(forced))
	}
	roles := func(request []ai.Message) []string {
		var out []string
		for _, message := range request {
			switch message.(type) {
			case ai.SystemMessage:
				out = append(out, "system")
			case ai.UserMessage:
				out = append(out, "user")
			case *ai.AssistantMessage, ai.AssistantMessage:
				out = append(out, "assistant")
			default:
				out = append(out, "other")
			}
		}
		return out
	}
	if got := ai.GetCurrentSystemPrompt(requests[2]); got != "Exact prompt." {
		t.Errorf("third request's current system prompt = %q, want the forced text", got)
	}
	if got, want := roles(requests[2]), []string{"system", "user", "assistant", "user", "assistant", "user"}; !reflect.DeepEqual(got, want) {
		t.Errorf("third request roles = %v, want %v", got, want)
	}
	// The transcript records the structured sections only, never the forced text.
	var recorded []ai.OrderedSections
	for _, message := range h.session.Messages() {
		if message.System != nil {
			recorded = append(recorded, message.System.Sections)
		}
	}
	wantRecorded := []ai.OrderedSections{
		head.Sections,
		{{Name: "plan_mode", Value: new("<plan_mode>\nPlan only.\n</plan_mode>")}},
		{{Name: "plan_mode", Value: nil}},
	}
	if !reflect.DeepEqual(recorded, wantRecorded) {
		t.Fatalf("recorded sections = %+v, want %+v", recorded, wantRecorded)
	}
	var transcript []ai.Message
	for _, message := range h.session.Messages() {
		if message.System != nil {
			transcript = append(transcript, *message.System)
		}
	}
	if got, want := ai.GetCurrentSystemPrompt(transcript), h.session.SystemPrompt(); got != want {
		t.Errorf("transcript's current system prompt = %q, want the session's %q", got, want)
	}
	for _, message := range h.session.Messages() {
		if message.System != nil && strings.Contains(ai.GetSystemMessageText(*message.System), "Exact prompt.") {
			t.Errorf("the forced text was recorded: %+v", message.System)
		}
	}
}

// system-prompt-updates.test.ts:276-302: a tool declaration recorded in the transcript has no execute member and no constrainedSampling member when the tool sets none, and the persisted JSON replays to the same declarations: a second prompt after the round trip adds no system message.
func TestToolDeclarationsSurviveASessionJSONRoundTrip(t *testing.T) {
	tool := boundaryTool{name: "plain", label: "Plain", description: "Plain tool", text: "ok"}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}}, fauxReply("first", ai.StopReasonStop, 0), fauxReply("second", ai.StopReasonStop, 0))
	if err := h.session.Prompt(t.Context(), "one"); err != nil {
		t.Fatal(err)
	}
	messages := h.session.Messages()
	if len(messages) == 0 || messages[0].System == nil || len(messages[0].System.ToolsAdded) == 0 {
		t.Fatalf("no system message with tool declarations: %+v", messages)
	}
	encoded, err := json.Marshal(messages[0].System.ToolsAdded[0])
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"constrainedSampling", "execute"} {
		if _, present := members[name]; present {
			t.Errorf("declaration has a %q member: %s", name, encoded)
		}
	}
	wire, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	var replayed []agent.AgentMessage
	if err := json.Unmarshal(wire, &replayed); err != nil {
		t.Fatal(err)
	}
	h.session.Agent().SetMessages(replayed)
	if err := h.session.Prompt(t.Context(), "two"); err != nil {
		t.Fatal(err)
	}
	systems := 0
	for _, message := range h.session.Messages() {
		if message.System != nil {
			systems++
		}
	}
	if systems != 1 {
		t.Errorf("the transcript has %d system messages after the round trip, want 1", systems)
	}
}
