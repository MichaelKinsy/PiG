package main

import (
	"path/filepath"
	"testing"
)

// A Go type alias is the same type as its target, so the wire fields a package decodes through the alias are the target's.
// coding/extension re-exports markdowntransform.MarkdownTransformContext; without alias resolution the matrix reported messageType
// and availableWidth as dropped by the host while the host still carries both.
func TestResolveAliasesGivesAnAliasItsTargetsWireFields(t *testing.T) {
	ext, err := parseGoPackage(filepath.Join(repoRoot, "coding/extension"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ext.jsonFields("MarkdownTransformContext"); len(got) != 0 {
		t.Fatalf("before resolution the alias has no fields of its own, got %v", got)
	}
	if err := ext.resolveAliases(repoRoot, "github.com/MichaelKinsy/PiG"); err != nil {
		t.Fatal(err)
	}
	got := ext.jsonFields("MarkdownTransformContext")
	for _, want := range []string{"messageType", "isStreaming", "availableWidth"} {
		if !got[want] {
			t.Errorf("alias fields %v lack %q", got, want)
		}
	}
	if got["Context"] || got["-"] {
		t.Errorf("the json:\"-\" host-only Context field must not be a wire field: %v", got)
	}
}
