package mcp

import (
	"encoding/json"
	"testing"
)

// ListToolsResult is a tools/list page as Pi declares it (packages/mcp/src/protocol/types.ts:87-91: `tools: Tool[]; nextCursor?: string; _meta?: Record<string, unknown>`): the server's JSON decodes into `tools`, `nextCursor` and `_meta`, and marshalling the page gives the same members back (the wire shape LEAD-RULINGS-1520 lg-imla-2 census item 4 says is ported when a JSON round-trip test reads it).
//
// mutation-checked: the mutant "ListToolsResult.NextCursor loses its json tag" fails it.
func TestListToolsResultRoundTripsTheWirePage(t *testing.T) {
	const wire = `{"tools":[{"name":"search","inputSchema":{"type":"object"}},{"name":"fetch","description":"gets a page","inputSchema":{"type":"object"}}],"nextCursor":"page-2","_meta":{"trace":"abc"}}`
	var page ListToolsResult
	if err := json.Unmarshal([]byte(wire), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Tools) != 2 || page.Tools[0].Name != "search" || page.Tools[1].Name != "fetch" || page.Tools[1].Description != "gets a page" {
		t.Fatalf("tools = %+v, want search and fetch in order", page.Tools)
	}
	if page.NextCursor != "page-2" {
		t.Fatalf("nextCursor = %q, want page-2", page.NextCursor)
	}
	if string(page.Meta) != `{"trace":"abc"}` {
		t.Fatalf("_meta = %s, want the server's object", page.Meta)
	}
	out, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	var cursor string
	if err := json.Unmarshal(back["nextCursor"], &cursor); err != nil || cursor != "page-2" || string(back["_meta"]) != `{"trace":"abc"}` {
		t.Fatalf("marshalled page %s lost nextCursor or _meta", out)
	}
}
