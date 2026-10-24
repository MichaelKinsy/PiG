package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// codemode.ts:233 `DEFAULT_SEARCH_LIMIT = 8` and :936 `options?.limit ?? DEFAULT_SEARCH_LIMIT`: searchTools() without a
// limit returns eight tools of the ten that match, and an explicit limit overrides it.
func TestSearchToolsReturnsEightMatchesWithoutALimit(t *testing.T) {
	chat := codemodeChat(t)
	for i := range 10 {
		addTool(t, chat.setup.registry, tool(fmt.Sprintf("issue_%d", i), func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		}, durable.ReplayUnsafe))
	}
	run := runScript(t, `return {
		none: (await searchTools("issue")).length,
		undefinedLimit: (await searchTools("issue", {})).length,
		ten: (await searchTools("issue", { limit: 10 })).length,
		three: (await searchTools("issue", { limit: 3 })).length,
	};`, chat)
	var got map[string]int
	if err := json.Unmarshal([]byte(scriptHeader.ReplaceAllString(run.text, "")), &got); err != nil {
		t.Fatalf("%q: %v", run.text, err)
	}
	if want := map[string]int{"none": 8, "undefinedLimit": 8, "ten": 10, "three": 3}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
