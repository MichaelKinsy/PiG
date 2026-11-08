package toolsearch_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
)

// Ports packages/coding-agent/test/tool-search.test.ts "schema references in the search text" (Pi 6d3e342b2, #11803).
//
// The text only feeds BM25, which ignores the order of its words (CreateDocument sorts property names, where Pi keeps their order), so the cases
// compare the sorted words.

func searchWords(t *testing.T, parameters string) []string {
	t.Helper()
	text := toolsearch.CreateDocument(extension.ToolInfo{Name: "mcp__ledger__inspect", Description: "Inspect records", Parameters: json.RawMessage(parameters)}, nil).Text
	words := strings.Fields(text)
	slices.Sort(words)
	return words
}

func wantWords(t *testing.T, got []string, want string) {
	t.Helper()
	expected := strings.Fields(want)
	slices.Sort(expected)
	if !slices.Equal(got, expected) {
		t.Fatalf("search text words = %q, want %q", got, expected)
	}
}

func TestSchemaRefsReadsNamesAndDescriptionsBehindLocalReferences(t *testing.T) {
	wantWords(t, searchWords(t, `{"type":"object","properties":{"options":{"$ref":"#/$defs/options"}},"$defs":{"options":{"type":"object","properties":{"mode":{"type":"string","description":"reconciliation"}}}}}`),
		"Inspect records options mode reconciliation")
}

func TestSchemaRefsFollowsEscapedPointerSegments(t *testing.T) {
	wantWords(t, searchWords(t, `{"type":"object","properties":{"range":{"$ref":"#/$defs/date~1range"}},"$defs":{"date/range":{"description":"fiscal period"}}}`),
		"Inspect records range fiscal period")
}

func TestSchemaRefsReadsRecursiveAndSharedDefinitionsOnce(t *testing.T) {
	wantWords(t, searchWords(t, `{"type":"object","properties":{"root":{"$ref":"#/$defs/node"},"other":{"$ref":"#/$defs/node"},"self":{"$ref":"#"}},"$defs":{"node":{"description":"tree node","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/node"}}}}}}`),
		"Inspect records root tree node children other self")
}

func TestSchemaRefsLeavesOutUnreferencedDefinitionsAndRemoteOrMissingReferences(t *testing.T) {
	wantWords(t, searchWords(t, `{"type":"object","properties":{"remote":{"$ref":"https://example.com/schema.json#/$defs/x"},"missing":{"$ref":"#/$defs/missing"},"malformed":{"$ref":"#/$defs/%E0%A4%A"}},"$defs":{"unused":{"description":"reconciliation"}}}`),
		"Inspect records remote missing malformed")
}
