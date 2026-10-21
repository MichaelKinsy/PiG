package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, data := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSubpackageSymbolsAreQualified seeds an SDK tree with a tui subpackage in each language and checks
// which symbols a ledger cell can name, and how.
func TestSubpackageSymbolsAreQualified(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"extensions/sdk/tui/text.go":                "package tui\n\nfunc VisibleWidth(s string) int { return len(s) }\nfunc hidden() {}\n\ntype Key struct{}\n\nfunc (Key) Matches() bool { return true }\n",
		"extensions/sdk/tui/alias.go":               "package tui\n\nimport \"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/e\"\n\ntype KeybindingsManager = e.Manager\n",
		"extensions/sdk/tui/internal/e/mgr.go":      "package e\n\ntype Manager struct{ Name string }\n\nfunc (Manager) GetKeys() []string { return nil }\nfunc (Manager) hidden() {}\n",
		"extensions/sdk/tui/internal/e/e.go":        "package e\n\nfunc Engine() {}\n",
		"extensions/sdk/upgrade/u.go":               "package upgrade\n\nfunc Rewrite() {}\n",
		"extensions/sdk/frontend/f.go":              "package frontend\n\nfunc Draw() {}\n",
		"extensions/sdk/testdata/t/ignored.txt":     "x",
		"extensions/sdk-rs/src/tui/mod.rs":          "mod text;\nmod keys;\nmod secret;\n\npub use keys::*;\npub use text::{visible_width, Segments};\npub fn declared_in_mod() {}\npub(crate) use secret::internal_only;\n",
		"extensions/sdk-rs/src/tui/text.rs":         "pub fn visible_width(s: &str) -> usize { s.len() }\npub fn not_reexported() {}\npub(crate) fn crate_only() {}\npub struct Segments {\n    pub width: usize,\n}\nimpl Segments {\n    pub fn first(&self) {}\n    pub(crate) fn private(&self) {}\n}\n",
		"extensions/sdk-rs/src/tui/keys.rs":         "pub fn matches_key() {}\n",
		"extensions/sdk-rs/src/tui/secret.rs":       "pub fn internal_only() {}\n",
		"extensions/sdk-rs/src/tui/tests.rs":        "pub fn helper_in_tests() {}\n",
		"extensions/sdk-rs/src/lib.rs":              "pub fn root_fn() {}\n",
		"extensions/sdk-py/pig_sdk/tui/__init__.py": "from ._text import (\n    visible_width as visible_width,\n    Segments as Segments,\n    _private as _private,\n)\n\nCURSOR_MARKER = 'x'\n\ndef declared_here():\n    pass\n",
		"extensions/sdk-py/pig_sdk/tui/_text.py":    "def visible_width(s):\n    return len(s)\n\ndef not_exported(s):\n    return s\n\nclass Segments:\n    def first(self):\n        pass\n\nclass Hidden:\n    def m(self):\n        pass\n",
		"extensions/sdk-py/pig_sdk/__init__.py":     "def root_fn():\n    pass\n",
	})
	goSyms, rs, py := symbolSet{}, symbolSet{}, symbolSet{}
	if err := addSubpackageSymbols(root, goSyms, rs, py); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		set  symbolSet
		name string
		want bool
	}{
		{goSyms, "tui.VisibleWidth", true}, {goSyms, "tui.Key", true}, {goSyms, "tui.Key.Matches", true}, {goSyms, "frontend.Draw", true}, {goSyms, "tui.KeybindingsManager.GetKeys", true}, {goSyms, "tui.KeybindingsManager.Name", true}, {goSyms, "tui.KeybindingsManager.hidden", false}, {goSyms, "tui.Manager.GetKeys", false},
		{goSyms, "VisibleWidth", false}, {goSyms, "tui.hidden", false}, {goSyms, "tui.Engine", false}, {goSyms, "upgrade.Rewrite", false},
		{rs, "tui::visible_width", true}, {rs, "tui::Segments", true}, {rs, "tui::Segments::first", true}, {rs, "tui::Segments.width", true},
		{rs, "tui::matches_key", true}, {rs, "tui::declared_in_mod", true},
		{rs, "visible_width", false}, {rs, "tui::not_reexported", false}, {rs, "tui::crate_only", false}, {rs, "tui::Segments::private", false},
		{rs, "tui::internal_only", false}, {rs, "tui::helper_in_tests", false}, {rs, "tui::root_fn", false},
		{py, "tui.visible_width", true}, {py, "tui.Segments", true}, {py, "tui.Segments.first", true}, {py, "tui.CURSOR_MARKER", true}, {py, "tui.declared_here", true},
		{py, "visible_width", false}, {py, "tui.not_exported", false}, {py, "tui.Hidden", false}, {py, "tui.Hidden.m", false}, {py, "tui._private", false},
	} {
		if got := c.set.has(c.name); got != c.want {
			t.Errorf("%s: has = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestLedgerCellsNameSubpackageSymbolsQualified: a cell must carry the qualifier, so a same-named root
// symbol cannot stand in for a subpackage helper and an unqualified name does not resolve to the subpackage.
func TestLedgerCellsNameSubpackageSymbolsQualified(t *testing.T) {
	in := cloneInput(seedInput())
	in.Rows = append(in.Rows, ledgerRow{Key: "pi-tui: visibleWidth", Module: "pi-tui", Name: "visibleWidth", Decl: "function", Node: "served"})
	in.Symbols = map[string]symbolSet{
		"go":     {"TruncateHead": true, "kit.Text": true, "tui.VisibleWidth": true},
		"rust":   {"truncate_head": true, "kit::Text": true, "tui::visible_width": true},
		"python": {"truncate_head": true, "kit.Text": true, "tui.visible_width": true},
	}
	in.Corpus = map[string]string{
		"go":     "TruncateHead(x); kit.Text(1); tui.VisibleWidth(s)",
		"rust":   "truncate_head(x); kit::Text::new(1); tui::visible_width(s)",
		"python": "truncate_head(x); kit.Text(1); tui.visible_width(s)",
	}
	in.File.Export["pi-tui: visibleWidth"] = map[string]string{"go": "native:tui.VisibleWidth", "rust": "native:tui::visible_width", "python": "native:tui.visible_width"}
	if p := checkLedger(in); len(p) > 0 {
		t.Fatalf("qualified cells failed:\n  %s", strings.Join(p, "\n  "))
	}
	in.File.Export["pi-tui: visibleWidth"]["go"] = "native:VisibleWidth"
	in.File.Export["pi-tui: visibleWidth"]["python"] = "native:visible_width"
	p := strings.Join(checkLedger(in), "\n")
	if !strings.Contains(p, `names native "VisibleWidth", which the Go SDK does not define`) || !strings.Contains(p, `"visible_width", which the Python SDK does not define`) {
		t.Fatalf("an unqualified subpackage name resolved:\n%s", p)
	}
}
