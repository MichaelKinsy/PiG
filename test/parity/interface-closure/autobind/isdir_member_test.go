package main

import (
	"go/types"
	"slices"
	"strings"
	"testing"
)

// Node's fs.Stats.isDirectory() is io/fs.FileInfo's IsDir() in Go, so an object literal member isDirectory agrees with an
// interface whose method is IsDir.
func TestObjectLiteralIsDirectoryIsIsDir(t *testing.T) {
	gt := goTypes(t, `
type Info interface{ IsDir() bool }
type Plain interface{ Name() string }
var (
	IV Info
	PV Plain
)`)
	c := &checker{generics: map[string]bool{}, renames: map[string]string{}, pkg: "coding-agent"}
	if got := c.literalAgainstInterface("{ isDirectory: () => boolean }", gt["IV"].Underlying().(*types.Interface)); got.ok != yes {
		t.Errorf("isDirectory vs IsDir = %v (%s), want yes", got.ok, got.why)
	}
	if got := c.literalAgainstInterface("{ isDirectory: () => boolean }", gt["PV"].Underlying().(*types.Interface)); got.ok == yes {
		t.Errorf("isDirectory vs Name = yes, want not yes")
	}
}

// A union member names an interface the alias row's own package declares: pi-mcp's ContentBlock is TextContent | ImageContent | ... with
// mcp's TextContent (text, annotations, _meta), never pi-ai's TextContent that also carries textSignature, even though pi-ai is listed first.
func TestUnionMemberResolvesInTheAliasPackage(t *testing.T) {
	prop := func(id, name string) *upstreamEntry {
		parent, _, _ := strings.Cut(id, "::")
		return &upstreamEntry{ID: id, ParentID: parent, Role: "property", Name: name, Kind: "property", Shape: shape{Name: name, Type: "string"}}
	}
	aiText := &upstreamEntry{ID: "pkg:ai/.#TextContent", Name: "TextContent", Kind: "interface"}
	mcpText := &upstreamEntry{ID: "pkg:mcp/.#TextContent", Name: "TextContent", Kind: "interface"}
	l := &ledger{entries: []*upstreamEntry{
		aiText, prop("pkg:ai/.#TextContent::property:textSignature", "textSignature"),
		mcpText, prop("pkg:mcp/.#TextContent::property:annotations", "annotations"),
		{ID: "pkg:ai/.#OnlyAi", Name: "OnlyAi", Kind: "interface"}, prop("pkg:ai/.#OnlyAi::property:only", "only"),
	}, children: map[string][]*upstreamEntry{}}
	for _, e := range l.entries {
		if e.ParentID != "" {
			l.children[e.ParentID] = append(l.children[e.ParentID], e)
		}
	}
	d := &detector{l: l}
	names := func(aliasID, member string) []string {
		var out []string
		for _, p := range d.unionMemberProps(aliasID, member) {
			out = append(out, p.Name)
		}
		return out
	}
	if got := names("pkg:mcp/.#ContentBlock", "TextContent"); !slices.Equal(got, []string{"annotations"}) {
		t.Errorf("mcp's TextContent resolved to %v, want [annotations]", got)
	}
	if got := names("pkg:ai/compat#Block", "TextContent"); !slices.Equal(got, []string{"textSignature"}) {
		t.Errorf("ai's TextContent resolved to %v, want [textSignature]", got)
	}
	if got := names("pkg:mcp/.#ContentBlock", "OnlyAi"); !slices.Equal(got, []string{"only"}) {
		t.Errorf("a name only another package declares falls back to it, got %v", got)
	}
}
