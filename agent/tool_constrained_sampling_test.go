package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// samplingTool is an AgentTool whose schema carries one constrainedSampling state.
type samplingTool struct {
	fakeTool
	schema ai.ToolSchema
}

func (t *samplingTool) Schema() ai.ToolSchema { return t.schema }

func samplingTools(schema ai.ToolSchema) []AgentTool {
	return []AgentTool{&samplingTool{fakeTool: fakeTool{name: schema.Name}, schema: schema}}
}

func declaredToolsJSON(t *testing.T, tools []AgentTool, committed []AgentMessage) []string {
	t.Helper()
	messages := declareToolChanges(tools, committed, nil)
	var declared []string
	for _, message := range messages {
		if message.System == nil {
			continue
		}
		for _, tool := range message.System.ToolsAdded {
			encoded, err := json.Marshal(tool)
			if err != nil {
				t.Fatal(err)
			}
			declared = append(declared, string(encoded))
		}
	}
	return declared
}

// AgentTool extends Tool (agent/src/types.ts:464), so its constrainedSampling, false | ConstrainedSamplingConfig | undefined, reaches
// the loop's tool declaration through toToolDeclaration (ai/src/utils/transcript.ts:123-129): a config and an explicit false are
// kept, an unset value is omitted, and the loop re-declares a tool whose state changed (agent-loop.ts:330-349).
func TestAgentToolConstrainedSamplingIsDeclaredLikePi(t *testing.T) {
	params := map[string]any{"type": "object", "properties": map[string]any{}}
	config := &ai.ConstrainedSamplingConfig{Type: ai.ConstrainedSamplingJSONSchema, Strict: ai.ConstrainedSamplingStrictRequire}
	unset := ai.ToolSchema{Name: "lookup", Description: "d", Parameters: params}
	withConfig := unset
	withConfig.ConstrainedSampling = config
	disabled := unset
	disabled.ConstrainedSamplingDisabled = true

	for _, tc := range []struct {
		name   string
		schema ai.ToolSchema
		want   string
		absent bool
	}{
		{"config", withConfig, `"constrainedSampling":{"type":"json_schema","strict":"require"}`, false},
		{"explicit false", disabled, `"constrainedSampling":false`, false},
		{"unset", unset, `constrainedSampling`, true},
	} {
		declared := declaredToolsJSON(t, samplingTools(tc.schema), nil)
		if len(declared) != 1 {
			t.Fatalf("%s: %d declarations, want 1", tc.name, len(declared))
		}
		if has := strings.Contains(declared[0], tc.want); has == tc.absent {
			t.Errorf("%s: declaration %s, want contains=%v of %s", tc.name, declared[0], !tc.absent, tc.want)
		}
	}

	// A declared tool whose state then changes is declared again; the same state is not.
	committed := declareToolChanges(samplingTools(unset), nil, nil)
	if again := declaredToolsJSON(t, samplingTools(unset), committed); len(again) != 0 {
		t.Errorf("an unchanged tool was declared again: %v", again)
	}
	for name, changed := range map[string]ai.ToolSchema{"config": withConfig, "explicit false": disabled} {
		if again := declaredToolsJSON(t, samplingTools(changed), committed); len(again) != 1 {
			t.Errorf("a tool changed from unset to %s was declared %d times, want 1", name, len(again))
		}
	}
}
