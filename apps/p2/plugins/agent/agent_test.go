package agent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/test/p2/agent.test.ts "code mode agent prompt".

// agentDescription is the tool's description as Pi's test spells it.
const agentDescription = "Run a one-shot subagent in your cwd and resolve to its final answer, parsed as JSON and validated against `schema` (a JSON Schema object). Rejects when the subagent fails, or when its answer still does not validate after 3 attempts. Its answer is not reported to you, only what the script returns. Run many at once with `Promise.all`."

func TestDescribesTheToolItselfForCodeModesListingWithoutAPromptSectionOfItsOwn(t *testing.T) {
	extension := newExtension(nil, nil)
	if len(extension.Extension.Sections) != 0 {
		t.Fatalf("sections = %v", extension.Extension.Sections)
	}
	if len(extension.Tools) < 1 {
		t.Fatal("no tool")
	}
	tool := extension.Tools[0]
	if tool.Description != agentDescription {
		t.Fatalf("description = %q", tool.Description)
	}
	if !reflect.DeepEqual(tool.Callers, []durable.ToolCaller{durable.CallerTools}) {
		t.Fatalf("callers = %v", tool.Callers)
	}
	properties, _ := tool.Parameters["properties"].(map[string]any)
	described := func(name string) any {
		property, _ := properties[name].(map[string]any)
		return property["description"]
	}
	if got := described("thinkingLevel"); got != "off|minimal|low|medium|high|xhigh|max; default: the caller's." {
		t.Fatalf("thinkingLevel description = %v", got)
	}
	if got := described("model"); got != "'provider/modelId'; default: the caller's." {
		t.Fatalf("model description = %v", got)
	}
}
