//go:build !pig_strip_llama_cpp

package cli

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A pig_strip_llama_cpp build of cmd/pig compiles coding/cli's llama.go out, so it does not link internal/codingagent/llama; stock links it.
func TestStripLlamaBuildOmitsTheLlamaPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("go list of cmd/pig in -short mode")
	}
	const pkg = "github.com/MichaelKinsy/PiG/internal/codingagent/llama"
	deps := func(tags ...string) []string {
		args := append(append([]string{"list"}, tags...), "-deps", "github.com/MichaelKinsy/PiG/cmd/pig")
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}
	tag := pigstrip.Tag(llamaBuiltinName)
	if tag != "pig_strip_llama_cpp" {
		t.Fatalf("pigstrip.Tag(%q) = %q", llamaBuiltinName, tag)
	}
	if !slices.Contains(deps(), pkg) {
		t.Errorf("the stock cmd/pig does not link %s", pkg)
	}
	if slices.Contains(deps("-tags", tag), pkg) {
		t.Errorf("a %s build of cmd/pig links %s", tag, pkg)
	}
}

// A Piglet that strips llama.cpp at runtime acts like a Binary that compiled it out: no llama.cpp entry among the built-in
// extensions, so no llama.cpp provider or /llama, even for an explicit `-e builtin:llama.cpp`. Stock has the entry.
func TestRuntimeStripLlamaLoadsNoExtension(t *testing.T) {
	t.Setenv("LLAMA_BASE_URL", "")
	t.Setenv("LLAMA_API_KEY", "")
	if entries := llamaInlineExtensions(); len(entries) != 1 || entries[0].(extension.NamedInlineExtension).Name != llamaBuiltinName {
		t.Fatalf("stock llama.cpp entries = %+v", entries)
	}

	t.Cleanup(pigstrip.Strip(pigstrip.ListExtensions, llamaBuiltinName))
	if entries := llamaInlineExtensions(); entries != nil {
		t.Fatalf("stripped llama.cpp entries = %+v, want none", entries)
	}
	if slices.ContainsFunc(nativeBuiltInExtensions(nil), func(entry extension.InlineExtension) bool {
		named, ok := entry.(extension.NamedInlineExtension)
		return ok && named.Name == llamaBuiltinName
	}) {
		t.Fatal("stripped nativeBuiltInExtensions still lists llama.cpp")
	}
}
