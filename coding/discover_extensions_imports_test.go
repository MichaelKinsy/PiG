package coding

import (
	"os/exec"
	"strings"
	"testing"
)

// The public coding package must not link the SDK sources the staged-SDK check compares against (coding/extension/pigsdk embeds
// extensions/sdk): a program that imports coding and builds as a Go extension cell would compile against the staged SDK, which
// omits bundle.go, and fail with `undefined: sdk.BundledFiles`. The pig command registers the check instead
// (subprocess.SetDefaultStagedSDKVerifier).
func TestCodingDoesNotImportThePigSDKStager(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.SplitSeq(string(out), "\n") {
		if strings.HasSuffix(dep, "/coding/extension/pigsdk") {
			t.Fatalf("package coding links %s", dep)
		}
	}
}

// The public coding package must not link the compiled-in extensions an extension names by reference (createCodemodeExtension, createToolSearchExtension): the
// sandbox's wasm runtime and the tool-search index are most of a megabyte that a program embedding coding without them never runs. coding/extension/builtin registers
// their factories (internal/compiledin), and the pig command links it. Nor may it link the TypeBox type engine, which coding/extension/pigsdk registers with the host.
func TestCodingDoesNotLinkTheBuiltInsOrTheTypeEngine(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for dep := range strings.SplitSeq(string(out), "\n") {
		for _, suffix := range []string{"/coding/extension/builtin", "/coding/extension/builtin/codemode", "/coding/extension/builtin/toolsearch", "/internal/typebox/engine"} {
			if strings.HasSuffix(dep, suffix) {
				t.Errorf("package coding links %s", dep)
			}
		}
		if strings.HasPrefix(dep, "github.com/tetratelabs/wazero") {
			t.Errorf("package coding links %s", dep)
		}
	}
}
