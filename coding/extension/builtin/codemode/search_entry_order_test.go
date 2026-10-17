package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// execute.ts createDiscoveryGlobals: each searchTools() match is `{ name, description }`, built in that order, and
// the script reads it through JSON.stringify or Object.keys, so the key order is model-visible.
func TestSearchToolsEntriesKeepPisKeyOrder(t *testing.T) {
	tools := []extension.AgentTool{{Name: "read_file", Description: "Read a file."}}
	globals := discoveryGlobals(tools, map[string]string{"read_file": "read_file(input): Read a file."}, nil)
	got, err := globals[0].Execute(context.Background(), json.RawMessage(`["read"]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"name":"read_file","description":"read_file(input): Read a file."}]`; string(got) != want {
		t.Fatalf("searchTools = %s, want %s", got, want)
	}
}
