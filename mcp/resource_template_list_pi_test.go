package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// TestClientListsResourceTemplatesLikePi ports client.ts isResourceTemplate / toResourceTemplate / validateListPage (:90-123): an entry needs a string
// uriTemplate and, if it has a name, a string name, otherwise the whole result fails with `Invalid entry in MCP resources/templates/list result`
// (invalidRequest); an entry without a name is listed with the URI template as its name, while a present (even empty) name is kept.
func TestClientListsResourceTemplatesLikePi(t *testing.T) {
	list := func(t *testing.T, templates string) ([]mcp.ResourceTemplate, error) {
		t.Helper()
		client, server := connect(t)
		defer func() { _ = client.Close() }()
		server.setHandler("resources/templates/list", func(mcp.JSONRPCMessage) (any, error) {
			return json.RawMessage(`{"resourceTemplates":` + templates + `}`), nil
		})
		return client.ListResourceTemplates(t.Context(), mcp.RequestOptions{})
	}
	got, err := list(t, `[{"uriTemplate":"a://{x}"},{"uriTemplate":"b://{x}","name":"b"},{"uriTemplate":"c://{x}","name":""}]`)
	if err != nil || len(got) != 3 {
		t.Fatalf("templates=%v err=%v", got, err)
	}
	for i, want := range []string{"a://{x}", "b", ""} {
		if got[i].Name != want {
			t.Errorf("template %d name = %q, want %q", i, got[i].Name, want)
		}
	}
	for name, templates := range map[string]string{
		"no uriTemplate":      `[{"name":"n"}]`,
		"numeric uriTemplate": `[{"uriTemplate":1}]`,
		"numeric name":        `[{"uriTemplate":"a://{x}","name":5}]`,
		"null name":           `[{"uriTemplate":"a://{x}","name":null}]`,
		"entry not an object": `["a://{x}"]`,
		"one bad among good":  `[{"uriTemplate":"a://{x}"},{"name":"n"}]`,
	} {
		if _, err := list(t, templates); err == nil || !strings.Contains(err.Error(), "Invalid entry in MCP resources/templates/list result") {
			t.Errorf("%s: err = %v, want the invalid-entry error", name, err)
		}
	}
}
