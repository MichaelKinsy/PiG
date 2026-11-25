// SPDX-License-Identifier: MIT

package runtimecell

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestExtractRunnerSDKRequirementsDerivesFromTemplate proves that required SDK
// symbols are derived directly from the generated runner source rather than a
// hardcoded list: adding a hypothetical second call (`sdk.HypotheticalRunnerHook`)
// to the runner source causes the extractor and missing-symbol check to require it
// automatically.
func TestExtractRunnerSDKRequirementsDerivesFromTemplate(t *testing.T) {
	// 1. Real runner template derives ReportLoadFailure and RunWithSocket.
	pkgSyms, methods := goRunnerSDKRequirements()
	if !slices.Contains(pkgSyms, "ReportLoadFailure") {
		t.Fatalf("derived pkgSymbols = %v, want ReportLoadFailure", pkgSyms)
	}
	if !slices.Contains(methods, "RunWithSocket") {
		t.Fatalf("derived runnableMethods = %v, want RunWithSocket", methods)
	}

	// 2. Inject a made-up second SDK symbol into the rendered runner source and verify
	// that the real current repo SDK (which exports ReportLoadFailure but not the
	// made-up symbol) is flagged with exactly the missing symbol.
	augmented := strings.Replace(renderGoRunner(nil), "_ = sdk.ReportLoadFailure(sock)", "_ = sdk.ReportLoadFailure(sock)\n\t\t_ = sdk.HypotheticalRunnerHook(sock)", 1)
	if augmented == renderGoRunner(nil) {
		t.Fatal("failed to inject second symbol into rendered runner")
	}
	augPkg, augMethods := extractRunnerSDKRequirements(augmented)
	if !slices.Contains(augPkg, "HypotheticalRunnerHook") {
		t.Fatalf("augmented pkgSymbols = %v, want HypotheticalRunnerHook", augPkg)
	}

	currentSDK, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	if got := missingGoSDKRunnerSymbols(currentSDK, pkgSyms, methods); len(got) != 0 {
		t.Fatalf("current repo SDK reported missing symbols for current template: %v", got)
	}
	missing := missingGoSDKRunnerSymbols(currentSDK, augPkg, augMethods)
	if !slices.Equal(missing, []string{"sdk.HypotheticalRunnerHook"}) {
		t.Fatalf("missingGoSDKRunnerSymbols(currentSDK, augmented) = %v, want [sdk.HypotheticalRunnerHook]", missing)
	}
}

// TestBuildGoPackedCellFallsBackFromIncompatibleExtensionReplace reproduces the field bug
// where an extension module carries a local `replace github.com/MichaelKinsy/PiG/extensions/sdk => ...`
// pointing to an older SDK checkout (missing symbols the current packed runner template calls).
// When a compatible staged/preferred SDK is available, the host falls back to it instead of
// compiling the runner against the incompatible SDK and caching a compile error.
func TestBuildGoPackedCellFallsBackFromIncompatibleExtensionReplace(t *testing.T) {
	t.Setenv("PIG_SDK_GO_ROOT", "")
	staleSDK := filepath.Join(t.TempDir(), "stale-sdk")
	if err := os.MkdirAll(staleSDK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoMod(t, staleSDK, "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n")
	if err := os.WriteFile(filepath.Join(staleSDK, "sdk.go"), []byte("package sdk\n\ntype Extension struct{}\nfunc New(name string) *Extension { return &Extension{} }\nfunc (e *Extension) RunWithSocket(sock string) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	extDir := filepath.Join(t.TempDir(), "ext")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/staleext\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.7.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + modfile.AutoQuote(filepath.ToSlash(staleSDK)) + "\n"
	writeGoMod(t, extDir, goMod)
	if err := os.WriteFile(filepath.Join(extDir, "ext.go"), []byte("package staleext\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"staleext\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	currentSDK, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := findSDKRootWithPreferred([]GoExtension{{Root: extDir}}, currentSDK)
	if err != nil {
		t.Fatalf("findSDKRootWithPreferred failed: %v", err)
	}
	if got != currentSDK {
		t.Fatalf("findSDKRootWithPreferred picked incompatible replace %q, want fallback %q", got, currentSDK)
	}

	cell, err := BuildGoPackedCellWithSDKRoot(context.Background(), t.TempDir(), "stale-replace-cell", []GoExtension{{
		Name:       "staleext",
		Root:       extDir,
		ModulePath: "example.com/staleext",
		Package:    "example.com/staleext",
		Factory:    "Extension",
		Hash:       "stale1",
	}}, currentSDK)
	if err != nil {
		t.Fatalf("BuildGoPackedCellWithSDKRoot failed: %v", err)
	}
	if cell == nil || cell.BinaryPath == "" {
		t.Fatal("expected built packed cell binary")
	}
}

// TestFindSDKRootRejectsOnlyIncompatibleCandidateWithActionableError verifies that when
// every candidate SDK root lacks runner-required exports, findSDKRootWithPreferred fails
// explicitly naming the path, how it was selected, and the missing symbol(s).
func TestFindSDKRootRejectsOnlyIncompatibleCandidateWithActionableError(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PIG_SOURCE_ROOT", filepath.Join(tmp, "missing-src"))

	staleSDK := filepath.Join(tmp, "old-sdk")
	if err := os.MkdirAll(staleSDK, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoMod(t, staleSDK, "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n")
	if err := os.WriteFile(filepath.Join(staleSDK, "sdk.go"), []byte("package sdk\ntype Extension struct{}\nfunc (e *Extension) RunWithSocket(sock string) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", staleSDK)

	got, err := findSDKRootWithPreferred(nil, "")
	if err == nil {
		t.Fatalf("expected error for incompatible PIG_SDK_GO_ROOT, got %q", got)
	}
	msg := err.Error()
	if !strings.Contains(msg, staleSDK) || !strings.Contains(msg, "PIG_SDK_GO_ROOT") || !strings.Contains(msg, "sdk.ReportLoadFailure") {
		t.Fatalf("error %q must name path %q, source PIG_SDK_GO_ROOT, and missing symbol sdk.ReportLoadFailure", msg, staleSDK)
	}
}
