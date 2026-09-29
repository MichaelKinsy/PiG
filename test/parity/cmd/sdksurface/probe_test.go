package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageProbeDoesNotConstructArbitraryClasses(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "constructed")
	module := filepath.Join(dir, "unsafe.mjs")
	source := fmt.Sprintf(`import {writeFileSync} from "node:fs";
export class Unsafe { constructor() { writeFileSync(%q, "side effect"); this.field = "present"; } }
`, marker)
	if err := os.WriteFile(module, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDir, err := filepath.Abs(filepath.Join(repoRoot, "coding/extension/host/subprocess/runtime-node"))
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(runtimeDir, module)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runProbe(repoRoot, []probePackage{{Module: "probe", File: filepath.ToSlash(rel), Exports: []probeExport{{Name: "Unsafe", Kind: "class", Members: []string{"field"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("probe constructed a class with arbitrary side effects: %v", err)
	}
}

// Pi's virtual-modules.ts exposes pi-agent-core. HStack/VStack implement
// LAYOUT_NODE inherited from Stack (packages/tui/src/components/stack.ts).
func TestProbeFindsVendoredPackageAndSymbolMembers(t *testing.T) {
	packages, err := packageExports(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runProbe(repoRoot, packages)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ pkg, name, member string }{
		{"pi-agent-core", "Agent", "abort"},
		{"pi-agent-core", "LaneBusy", "lane"},
		{"pi-agent-core", "LaneBusy", "cause"},
		{"pi-agent-core", "FileError", "cause"},
		{"pi-ai", "ModelsError", "cause"},
		{"pi-tui", "HStack", "[Symbol.LAYOUT_NODE]"},
		{"pi-tui", "VStack", "[Symbol.LAYOUT_NODE]"},
		{"pi-tui", "TuiAltScreen", "[Symbol.VIEWPORT_TUI]"},
	} {
		got := report.Packages[test.pkg][test.name].Members[test.member]
		if got.Status != "vendored" {
			t.Errorf("%s %s.%s = %+v, want vendored", test.pkg, test.name, test.member, got)
		}
	}
	credential := report.Packages["pi-coding-agent"]["CredentialSynchronizationError"].Members["cause"]
	if credential.Status != "vendored" {
		t.Fatalf("CredentialSynchronizationError.cause = %+v, want vendored", credential)
	}
}
