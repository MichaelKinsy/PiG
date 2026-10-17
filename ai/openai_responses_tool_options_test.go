package ai

import (
	"encoding/json"
	"testing"
)

// openai-responses-shared.ts:131,191,210: options.toolOptions reaches convertResponsesTools for the additional_tools item, and
// for the tool_search_output with toolSearchResult added (every tool gets defer_loading). It decides the strict field of an
// unconstrained function tool: true, null, false by default, and absent when supportsStrictMode is false.
func TestConvertResponsesMessagesPassesToolOptionsToTheToolsItIssues(t *testing.T) {
	model := sharedModel("gpt-5", "openai", APIOpenAIResponses, true, []string{"text"}, nil)
	allowed := map[string]bool{"openai": true}
	transcript := NormalizeContext(Context{SystemPrompt: "base", Messages: []Message{
		UserMessage{Content: UserText("go"), Timestamp: 1},
		SystemMessage{Content: SystemText("rule update"), ToolsAdded: mustTools(t, `[{"name":"extra","description":"e","parameters":{"type":"object","properties":{}}}]`), Timestamp: 2},
		UserMessage{Content: UserText("again"), Timestamp: 3},
	}})

	tests := []struct {
		name       string
		options    ConvertResponsesMessagesOptions
		wantType   string
		wantStrict string // "" means the field is absent
		wantDefer  bool
	}{
		{"additional tools default strict false", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true}, "additional_tools", `false`, false},
		{"additional tools strict true", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictTrue}}, "additional_tools", `true`, false},
		{"additional tools strict null", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictNull}}, "additional_tools", `null`, false},
		{"additional tools without strict mode", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsAdditionalTools: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictTrue, SupportsStrictMode: new(false)}}, "additional_tools", "", false},
		{"tool search strict true", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictTrue}}, "tool_search_output", `true`, true},
		{"tool search strict null", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true, ToolOptions: ConvertResponsesToolsOptions{Strict: ResponsesStrictNull}}, "tool_search_output", `null`, true},
		{"tool search without strict mode", ConvertResponsesMessagesOptions{SupportsMidConvoSystemMessages: true, SupportsToolSearch: true, ToolOptions: ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}}, "tool_search_output", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := ConvertResponsesMessages(model, transcript, allowed, tt.options)
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, item := range items {
				if item.Type != tt.wantType {
					continue
				}
				found = true
				raw, err := json.Marshal(item)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					Tools []map[string]json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Tools) != 1 {
					t.Fatalf("%s item %s: %v", tt.wantType, raw, err)
				}
				tool := wire.Tools[0]
				if string(tool["name"]) != `"extra"` {
					t.Fatalf("tool = %s", raw)
				}
				strict, hasStrict := tool["strict"]
				if tt.wantStrict == "" && hasStrict {
					t.Fatalf("strict = %s, want the field absent", strict)
				}
				if tt.wantStrict != "" && string(strict) != tt.wantStrict {
					t.Fatalf("strict = %s (present %v), want %s", strict, hasStrict, tt.wantStrict)
				}
				if gotDefer := string(tool["defer_loading"]) == "true"; gotDefer != tt.wantDefer {
					t.Fatalf("defer_loading = %s, want %v", tool["defer_loading"], tt.wantDefer)
				}
			}
			if !found {
				t.Fatalf("no %s item in %+v", tt.wantType, items)
			}
		})
	}
}
