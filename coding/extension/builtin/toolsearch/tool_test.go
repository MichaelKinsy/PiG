package toolsearch_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
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
	details := result.(agent.AgentToolResult).Details.(toolsearch.Details)
	if !slices.Equal(details.Loaded, []string{"read_file", "read_dir"}) || !slices.Equal(active, details.Loaded) {
		t.Fatalf("loaded %q, active %q, want both tools", details.Loaded, active)
	}
}
