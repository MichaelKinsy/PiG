package main

import (
	"os"
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

// TestRustScannerSkipsRawStrings pins the raw-string case: a backslash before the closing
// quote of r"\\?\" must not hide the declarations after it.
func TestRustScannerSkipsRawStrings(t *testing.T) {
	dir := t.TempDir()
	src := "fn a(text: &str) -> String {\n    if let Some(rest) = text.strip_prefix(r\"\\\\?\\\") { rest.to_string() } else { r#\"x\"y\"#.to_string() }\n}\n\npub fn after<T>(x: T) -> T { x }\n"
	if err := os.WriteFile(filepath.Join(dir, "a.rs"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	syms, err := parseRust(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !syms.has("after") {
		t.Fatalf("a pub fn after a raw string was hidden: %v", syms.sorted())
	}
}
