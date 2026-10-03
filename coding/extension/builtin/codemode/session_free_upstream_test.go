package codemode

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The cases of packages/coding-agent/test/suite/agent-session-codemode.test.ts (v1.0.0) that use no AgentSession: two
// call the tool and readCodemodeStore directly, and one checks a tool created without model access. The other nineteen run
// a Session in coding/codemode_session_upstream_test.go.

func TestRunsWithoutASessionStartingFromAnEmptyStore(t *testing.T) {
	// upstream: createCodemodeTool().execute("direct", { code: increment }) without a context.
	params, _ := json.Marshal(map[string]string{"code": "const next = (load(\"count\") ?? 0) + 1;\nstore(\"count\", next);\nreturn next;"})
	result, err := Execute(context.Background(), "direct", params, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) < 2 || result.Content[1] != (ai.TextContent{Text: "1"}) {
		t.Fatalf("content = %#v, want the header then \"1\"", result.Content)
	}
}

func TestFoldsStoreEntriesFromTheRootIgnoringMalformedData(t *testing.T) {
	entry := func(data any, customType string) codingagent.SessionEntry {
		raw, err := json.Marshal(map[string]any{"type": "custom", "customType": customType, "data": data, "id": "x", "parentId": nil, "timestamp": "1970-01-01T00:00:00.000Z"})
		if err != nil {
			t.Fatal(err)
		}
		return codingagent.NewSessionEntry(raw, codingagent.SessionEntryBase{Type: "custom"})
	}
	got := storeOf([]codingagent.SessionEntry{
		entry(map[string]any{"set": map[string]any{"a": 1, "b": map[string]any{"c": 2}}, "delete": []string{}}, StoreEntryType),
		entry(map[string]any{"set": map[string]any{"a": 3}, "delete": []string{"b"}}, StoreEntryType),
		entry(map[string]any{"set": map[string]any{"z": 1}}, StoreEntryType),
		entry(map[string]any{"set": map[string]any{"other": 1}, "delete": []string{}}, "other-extension"),
	})
	if want := map[string]json.RawMessage{"a": json.RawMessage("3")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("store = %v, want %v", got, want)
	}
}

// The "overridden" half of "declares models only for the session's own codemode tool" (v1.0.0): a tool created without
// model access (createCodemodeTool()) does not name `models`; the session's own tool does.
func TestDeclaresModelsOnlyForAToolCreatedWithModelAccess(t *testing.T) {
	if description := Definition(Options{}).Description; strings.Contains(description, "`models`") {
		t.Error("a tool created without model access names `models`")
	}
	if description := Definition(Options{Models: true}).Description; !strings.Contains(description, "`models`: classifiers and image generation") {
		t.Error("a tool created with model access does not name `models`")
	}
}
