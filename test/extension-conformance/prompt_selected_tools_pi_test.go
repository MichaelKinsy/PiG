package extensionconformance

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// The actual Pi Runner and AgentSession.prompt methods own normalization, filtering, getters, and rejection. Only the Provider run is replaced by a recording sink.
func TestSelectedToolsPiContract(t *testing.T) {
	root := findModuleRoot(t)
	command := exec.CommandContext(t.Context(), "node", filepath.Join(root, "test", "extension-conformance", "testdata", "selected-tools-pi.mjs"), filepath.Join(root, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Pi probe: %v\n%s", err, output)
	}
	type observation struct {
		Tools    []string `json:"tools"`
		Base     []string `json:"base"`
		Observed []any    `json:"observed"`
		Error    *string  `json:"error"`
	}
	var got map[string]observation
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, output)
	}
	want := map[string]observation{
		"duplicates": {[]string{"bash", "read"}, []string{"read"}, []any{"run"}, nil},
		"mixed":      {[]string{"bash"}, []string{"read"}, []any{"run"}, nil},
		"null":       {[]string{"read"}, []string{"read"}, []any{}, new("Cannot read properties of null (reading 'length')")},
		"repair":     {[]string{"write"}, []string{"read"}, []any{true, "run"}, nil},
		"reassign":   {[]string{"read", "bash"}, []string{"read"}, []any{"run"}, nil},
		"getters":    {[]string{"read"}, []string{"read"}, []any{true, true, "run"}, nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pi contract: %s", output)
	}
}
