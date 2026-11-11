package piglet

import (
	"strings"
	"testing"
)

// pig additive (D211): contextFiles takes all or trusted-only and nothing else.
func TestParse_ContextFiles(t *testing.T) {
	for source, want := range map[string]bool{
		"name: x\ncontextFiles: trusted-only\n": true,
		"name: x\ncontextFiles: all\n":          false,
		"name: x\n":                             false,
	} {
		p, err := ParseBytes([]byte(source))
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if got := p.WithholdsUntrustedContextFiles(); got != want {
			t.Errorf("%q: withholds = %v, want %v", source, got, want)
		}
	}
	for _, source := range []string{"name: x\ncontextFiles: never\n", "name: x\ncontextFiles: true\n"} {
		p, err := ParseBytes([]byte(source))
		if err == nil {
			t.Errorf("%q accepted as %q", source, p.ContextFiles)
		}
	}
	var none *Piglet
	if none.WithholdsUntrustedContextFiles() {
		t.Error("a nil Piglet withholds nothing")
	}
}

// pig additive (D211): a child that sets contextFiles: all over a trusted-only base widens it, and a child inherits the base value.
func TestContextFilesWideningNeedsAllowWiden(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\ncontextFiles: trusted-only\n")
	inherit := writePigletSource(t, dir, "inherit", "name: inherit\nextends:\n  source: local:./base.yaml\n")
	effective, err := ResolveEffective(inherit)
	if err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if !effective.Piglet.WithholdsUntrustedContextFiles() {
		t.Error("a child does not inherit contextFiles: trusted-only")
	}
	wide := writePigletSource(t, dir, "wide", "name: wide\nextends:\n  source: local:./base.yaml\ncontextFiles: all\n")
	if _, err := ResolveEffective(wide); err == nil || !strings.Contains(err.Error(), "contextFiles") {
		t.Fatalf("widen error = %v", err)
	}
	allowed := writePigletSource(t, dir, "allowed", "name: allowed\nextends:\n  allowWiden: true\n  source: local:./base.yaml\ncontextFiles: all\n")
	effective, err = ResolveEffective(allowed)
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}
	if effective.Piglet.WithholdsUntrustedContextFiles() {
		t.Error("allowWiden did not widen contextFiles")
	}
}
