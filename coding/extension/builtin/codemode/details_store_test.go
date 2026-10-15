package codemode

// pi: packages/coding-agent/src/extensions/codemode/execute.ts

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: execute.ts store() appends one `codemode-store` custom entry whose data is { set, delete } (CodemodeStoreEntryData), and a result over
// the output budget carries the temp file of the full output in details.fullOutputPath (CodemodeToolDetails).
// Pi: packages/coding-agent/src/extensions/codemode/execute.ts:319 (ToolDetails.fullOutputPath).
func TestStoreEntryDataAndFullOutputPathDetails(t *testing.T) {
	var appended []struct {
		customType string
		data       any
	}
	base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: emptyBranch{}})
	ctx := extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call-1", context.Background(), extension.ToolActions{
		AppendEntry: func(customType string, data any) error {
			appended = append(appended, struct {
				customType string
				data       any
			}{customType, data})
			return nil
		},
	}))
	code := "// @options: {\"max_output_tokens\": 10}\nstore(\"a\", {b: [1, 2]});\nstore(\"gone\", 1);\nstore(\"gone\", undefined);\nreturn \"x\".repeat(400);"
	params, _ := json.Marshal(map[string]string{"code": code})
	result, err := Execute(ctx, "call-1", params, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if len(appended) != 1 || appended[0].customType != StoreEntryType {
		t.Fatalf("appended entries = %+v, want one %q entry", appended, StoreEntryType)
	}
	data, ok := appended[0].data.(StoreEntryData)
	if !ok {
		t.Fatalf("entry data is %T, want StoreEntryData", appended[0].data)
	}
	encoded, _ := json.Marshal(data)
	var wire struct {
		Set    map[string]json.RawMessage `json:"set"`
		Delete []string                   `json:"delete"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire.Set["a"]) != `{"b":[1,2]}` {
		t.Errorf("set = %s, want a = {\"b\":[1,2]}", encoded)
	}
	if len(wire.Delete) != 1 || wire.Delete[0] != "gone" {
		t.Errorf("delete = %v in %s, want [gone]", wire.Delete, encoded)
	}

	details, ok := result.Details.(ToolDetails)
	if !ok {
		t.Fatalf("details are %T, want ToolDetails", result.Details)
	}
	if details.FullOutputPath == "" {
		t.Fatalf("details = %+v, want fullOutputPath for output over the budget", details)
	}
	full, err := os.ReadFile(details.FullOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
	if !strings.Contains(string(full), strings.Repeat("x", 400)) {
		t.Errorf("full output file holds %d bytes, want the whole 400-character result", len(full))
	}
}
