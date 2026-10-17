package toolsearch_test

// pi: packages/coding-agent/src/extensions/tool-search/index.ts

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// upstream tool-search/tool.ts execute: `limit` must be a positive integer, and the ranker slices with it
// (matches.slice(0, limit)), so a limit beyond the int range loads every match.
func TestToolSearchAcceptsALimitBeyondTheIntRange(t *testing.T) {
	var active []string
	actions := extension.ContextActions{
		GetAllTools: func() []extension.ToolInfo {
			return []extension.ToolInfo{
				{Name: "read_file", Description: "Read a file.", Exposure: extension.ToolExposureDeferred},
				{Name: "read_dir", Description: "Read a directory.", Exposure: extension.ToolExposureCodemode},
			}
		},
		GetActiveTools: func() []string { return active },
		SetActiveTools: func(names []string) { active = names },
	}
	ctx := extension.WithContext(context.Background(), extension.NewContext("", nil, func() error { return nil }, actions))
	result, err := toolsearch.Definition().Execute(ctx, "call", json.RawMessage(`{"query":"read","limit":1e20}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	details := result.Details.(toolsearch.Details)
	if !slices.Equal(details.Loaded, []string{"read_file", "read_dir"}) || !slices.Equal(active, details.Loaded) {
		t.Fatalf("loaded %q, active %q, want both tools", details.Loaded, active)
	}
}

func executeToolSearch(t *testing.T, tools []extension.ToolInfo, active *[]string, params string) (agent.AgentToolResult, error) {
	t.Helper()
	actions := extension.ContextActions{
		GetAllTools:    func() []extension.ToolInfo { return tools },
		GetActiveTools: func() []string { return *active },
		SetActiveTools: func(names []string) { *active = names },
	}
	ctx := extension.WithContext(context.Background(), extension.NewContext("", nil, func() error { return nil }, actions))
	result, err := toolsearch.Definition().Execute(ctx, "call", json.RawMessage(params), nil)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	return result, nil
}

func resultText(t *testing.T, result agent.AgentToolResult) string {
	t.Helper()
	return result.Content[0].(ai.TextContent).Text
}

// upstream tool-search/tool.ts execute: validation messages and the result text. The model-facing text lists each loaded
// tool with the first line (a shorter document ranks first under BM25 length normalization) of its description and counts them with the singular or plural noun.
func TestToolSearchRejectsAnEmptyQueryAndANonPositiveOrFractionalLimit(t *testing.T) {
	for params, want := range map[string]string{
		`{"query":"  \t"}`:             "query must not be empty",
		`{"query":"read","limit":0}`:   "limit must be a positive integer",
		`{"query":"read","limit":-2}`:  "limit must be a positive integer",
		`{"query":"read","limit":1.5}`: "limit must be a positive integer",
	} {
		var active []string
		if _, err := executeToolSearch(t, nil, &active, params); err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", params, err, want)
		}
	}
}

func TestToolSearchLoadsMatchesOfNonActiveSearchableToolsAndNamesTheFirstDescriptionLine(t *testing.T) {
	tools := []extension.ToolInfo{
		{Name: "read_file", Description: "  Read a file.\r\nSecond line.", Exposure: extension.ToolExposureDeferred},
		{Name: "read_dir", Description: "Read a directory.", Exposure: extension.ToolExposureCodemode},
		{Name: "read_model", Description: "Read for the model.", Exposure: extension.ToolExposureDirect},
		{Name: "read_active", Description: "Read, already active.", Exposure: extension.ToolExposureDeferred},
	}
	active := []string{"read_active"}
	result, err := executeToolSearch(t, tools, &active, `{"query":"read","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Loaded 1 tool. They are available from your next call:\n- read_dir: Read a directory."; resultText(t, result) != want {
		t.Fatalf("text = %q, want %q", resultText(t, result), want)
	}
	if !slices.Equal(active, []string{"read_active", "read_dir"}) {
		t.Fatalf("active = %q", active)
	}
	result, err = executeToolSearch(t, tools, &active, `{"query":"read"}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Loaded 1 tool. They are available from your next call:\n- read_file: Read a file."; resultText(t, result) != want {
		t.Fatalf("text = %q, want %q", resultText(t, result), want)
	}
	if !slices.Equal(active, []string{"read_active", "read_dir", "read_file"}) {
		t.Fatalf("active = %q", active)
	}
}

func TestToolSearchReportsNoMatchesWithoutChangingTheActiveTools(t *testing.T) {
	active := []string{"x"}
	tools := []extension.ToolInfo{{Name: "read_file", Description: "Read a file.", Exposure: extension.ToolExposureDeferred}}
	result, err := executeToolSearch(t, tools, &active, `{"query":"zebra"}`)
	if err != nil {
		t.Fatal(err)
	}
	details := result.Details.(toolsearch.Details)
	if resultText(t, result) != "No matching tools found." || len(details.Loaded) != 0 || !slices.Equal(active, []string{"x"}) {
		t.Fatalf("text %q, loaded %q, active %q", resultText(t, result), details.Loaded, active)
	}
	// Without the session's tools (no context), the tool finds nothing.
	bare, err := toolsearch.Definition().Execute(context.Background(), "call", json.RawMessage(`{"query":"read"}`), nil)
	if err != nil || bare.Content[0].(ai.TextContent).Text != "No matching tools found." {
		t.Fatalf("no context: %v, %v", bare, err)
	}
}

// upstream tool-search/index.ts: the tool is registered inactive, so `--tools`, `defaultTools` or `setActiveTools()`
// activates it.
func TestToolSearchExtensionRegistersTheToolInactive(t *testing.T) {
	ext, err := factoryload.LoadExtensionFromFactory(toolsearch.CreateToolSearchExtension(), ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:tool-search")
	if err != nil {
		t.Fatal(err)
	}
	registered, ok := ext.RegisteredTool(toolsearch.ToolName)
	if !ok || len(ext.RegisteredTools()) != 1 {
		t.Fatalf("tools = %v", ext.RegisteredTools())
	}
	if registered.Definition.DefaultActive == nil || *registered.Definition.DefaultActive {
		t.Fatalf("DefaultActive = %v, want false", registered.Definition.DefaultActive)
	}
	if registered.Definition.Exposure != extension.ToolExposureModelOnly {
		t.Fatalf("exposure = %q, want model-only", registered.Definition.Exposure)
	}
}
