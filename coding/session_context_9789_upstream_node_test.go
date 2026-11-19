//go:build !pig_strip_node_extensions

package coding

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:87. The Node factory exercises in-place prompt-option mutation across the production wire.
func TestUpstream9789KeepsMidConversationSystems(t *testing.T) {
	ext := context9789Node(t, `export default function(pi){let turn=0;pi.on("before_agent_start",event=>{if(++turn===2)event.systemPromptOptions.sections.plan_mode="Plan only.";});pi.on("context",async event=>({messages:event.messages}));}`)
	h := context9789Harness(t, ext)
	var handlerErrors []string
	h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { handlerErrors = append(handlerErrors, err.Error) })
	h.provider.responses = []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0)}
	context9789Prompt(t, h, "first")
	request := context9789Capture(h, "two")
	context9789Prompt(t, h, "second")
	var systems []ai.SystemMessage
	for _, m := range request(t) {
		if system, ok := m.(ai.SystemMessage); ok {
			systems = append(systems, system)
		}
	}
	if len(systems) != 2 {
		t.Fatalf("system messages=%d want=2", len(systems))
	}
	want := ai.OrderedSections{{Name: "plan_mode", Value: new("<plan_mode>\nPlan only.\n</plan_mode>")}}
	if !reflect.DeepEqual(systems[1].Sections, want) {
		t.Fatalf("sections=%v want=%v", systems[1].Sections, want)
	}
	if len(handlerErrors) != 0 {
		t.Fatalf("handler errors=%q", handlerErrors)
	}
	// system-prompt-updates.test.ts:114-171: a per-run section is recorded, not written back into the base options, and a later unmodified run records its removal.
	if base := h.session.GetSystemPromptOptions().Sections; base != nil && len(*base) != 0 {
		t.Fatalf("run mutated base sections: %+v", *base)
	}
	request = context9789Capture(h, "three")
	context9789Prompt(t, h, "third")
	systems = nil
	for _, message := range request(t) {
		if system, ok := message.(ai.SystemMessage); ok {
			systems = append(systems, system)
		}
	}
	if len(systems) != 3 || !reflect.DeepEqual(systems[2].Sections, ai.OrderedSections{{Name: "plan_mode"}}) {
		t.Fatalf("unmodified run lost section removal: %+v", systems)
	}
}

// Ports packages/coding-agent/test/suite/regressions/9789-context-handler-system-messages.test.ts:111. Use the exact Node splice rather than returning a replacement array, which would hide the missing mutation contract.
func TestUpstream9789InPlaceInsertionWithoutReturn(t *testing.T) {
	ext := context9789Node(t, `export default function(pi){pi.on("context",async event=>{event.messages.splice(0,0,{role:"user",content:[{type:"text",text:"injected"}],timestamp:0});});}`)
	h := context9789Harness(t, ext)
	request := context9789Capture(h, "done")
	context9789Prompt(t, h, "hello")
	if got := context9789Roles(request(t)); !slices.Equal(got, []string{"system", "user", "user"}) {
		t.Errorf("roles=%v", got)
	}
	if got := context9789ToolNames(request(t)); !slices.Equal(got, h.session.ActiveToolNames()) {
		t.Errorf("tools=%v want=%v", got, h.session.ActiveToolNames())
	}
}
