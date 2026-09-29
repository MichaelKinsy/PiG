package codingagent

import (
	"fmt"
	"testing"
)

// Session metadata selection reads only the fields belonging to that entry type. Unrelated JSON properties do not change Pi's branch settings.
func TestContextSettingsIgnoreUnrelatedEntryProperties(t *testing.T) {
	entries := []SessionEntry{
		contextFixtureEntry(t, "model", "", "model_change", map[string]any{"provider": "openai", "modelId": "chosen", "message": "not a message"}),
		contextFixtureEntry(t, "thinking", "model", "thinking_level_change", map[string]any{"thinkingLevel": "high", "provider": map[string]any{"ignored": true}}),
	}
	got := BuildSessionContext(entries)
	if got.Model == nil || got.Model.Provider != "openai" || got.Model.ModelID != "chosen" || got.ThinkingLevel != "high" {
		t.Fatalf("context=%+v", got)
	}
	fmt.Printf("SESSION_METADATA model=%s/%s thinking=%s\n", got.Model.Provider, got.Model.ModelID, got.ThinkingLevel)
}
