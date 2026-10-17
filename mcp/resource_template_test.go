package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// protocol/types.ts:106-114 ResourceTemplate: uriTemplate and name are required, title, description, mimeType, annotations and _meta optional. The typed
// fields read every member a server sends, and a template built in Go writes only the members it holds (an unset optional member is absent, not "").
func TestResourceTemplateReadsAndWritesEveryDeclaredMember(t *testing.T) {
	const wire = `{"uriTemplate":"repo://{owner}/{repo}","name":"repo","title":"Repository","description":"A repo","mimeType":"application/json","annotations":{"audience":["user"],"priority":0.5},"_meta":{"k":1}}`
	var template mcp.ResourceTemplate
	if err := json.Unmarshal([]byte(wire), &template); err != nil {
		t.Fatal(err)
	}
	if template.URITemplate != "repo://{owner}/{repo}" || template.Name != "repo" || template.Title != "Repository" || template.Description != "A repo" || template.MimeType != "application/json" {
		t.Fatalf("typed members = %+v", template)
	}
	if template.Annotations == nil || string(template.Meta) != `{"k":1}` {
		t.Fatalf("annotations = %+v, _meta = %s", template.Annotations, template.Meta)
	}
	jsonEqual(t, template, wire)

	built, err := json.Marshal(mcp.ResourceTemplate{URITemplate: "a://{x}", Name: "a"})
	if err != nil || string(built) != `{"uriTemplate":"a://{x}","name":"a"}` {
		t.Fatalf("a template built in Go = %s (%v), want only the required members", built, err)
	}
}
