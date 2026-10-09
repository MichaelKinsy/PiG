package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A stripped built-in extension leaves the built-in extension paths silently, as upstream's `--no-mcp` drops a disabled built-in
// (`--no-mcp`, disabledBuiltinExtensions in resource-loader.ts) before loading: the settings default and an explicit
// `-e builtin:<name>` both go, while a name that was never a built-in stays for the loader to report. A Binary that compiled
// the built-in out records it in pigstrip (its OFF shim's init); applyPigletStrip records the runtime strip list there too.
func TestStrippedBuiltinExtensionPathsAreFilteredSilently(t *testing.T) {
	names := []string{"llama.cpp", "codemode", "tool-search", "mcp"}
	explicit := []string{"builtin:mcp", "builtin:missing"}
	for _, tc := range []struct {
		name  string
		strip func(*Args) func()
		want  []string
	}{
		{"stock", func(*Args) func() { return func() {} }, []string{"builtin:mcp", "builtin:missing", "builtin:llama.cpp", "builtin:codemode", "builtin:tool-search"}},
		{"compiled out", func(*Args) func() { return pigstrip.Strip(pigstrip.ListExtensions, "mcp") }, []string{"builtin:missing", "builtin:llama.cpp", "builtin:codemode", "builtin:tool-search"}},
		{"piglet strip list", func(flags *Args) func() {
			return applyPigletStrip(&piglet.Piglet{Name: "lean", Strip: &piglet.StripSpec{Extensions: []string{"mcp"}}}, flags)
		}, []string{"builtin:missing", "builtin:llama.cpp", "builtin:codemode", "builtin:tool-search"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cwd, agentDir := resourceExtensionFixture(t)
			flags := Args{Extensions: explicit}
			t.Cleanup(tc.strip(&flags))
			if got, want := enabledBuiltinExtensionPaths(codingagent.NewSettingsManager(cwd, agentDir), flags, names), withoutCompiledOutBuiltins(tc.want); !slices.Equal(got, want) {
				t.Fatalf("paths = %q, want %q", got, want)
			}
		})
	}
}

// withoutCompiledOutBuiltins drops from paths the built-ins this test binary was built without: their OFF shims recorded them
// in pigstrip, so the filter drops them in every case.
func withoutCompiledOutBuiltins(paths []string) []string {
	return slices.DeleteFunc(slices.Clone(paths), func(path string) bool {
		name, ok := strings.CutPrefix(path, codingagent.BuiltinPathPrefix)
		return ok && pigstrip.Has(pigstrip.ListExtensions, name)
	})
}

// The production built-in pass loads no stripped built-in and reports no error for `-e builtin:<name>` of one, whether the
// Piglet strips it at runtime (the inline list still has it; the path filter drops it) or the Binary compiled it out
// (absent from the inline list and recorded in pigstrip). An unknown name still fails with Pi's message.
func TestStrippedExplicitBuiltinLoadsNothingAndReportsNoError(t *testing.T) {
	strip := &piglet.StripSpec{Extensions: []string{"codemode", "tool-search", "mcp", "pig-login"}}
	explicit := []string{"builtin:codemode", "builtin:tool-search", "builtin:mcp", "builtin:pig-login", "builtin:missing"}
	unknown := []extensionSetError{{Path: "builtin:missing", Error: "Unknown built-in extension: builtin:missing"}}
	for _, tc := range []struct {
		name   string
		inline []extension.InlineExtension
		flags  Args
		strip  func(*Args) func()
	}{
		{"runtime strip", slices.Clone(builtInExtensions), Args{NoExtensions: true, Extensions: explicit}, func(flags *Args) func() {
			return applyPigletStrip(&piglet.Piglet{Name: "lean", Strip: strip}, flags)
		}},
		{"compiled out", slices.DeleteFunc(slices.Clone(builtInExtensions), func(entry extension.InlineExtension) bool {
			named, ok := entry.(extension.NamedInlineExtension)
			return ok && named.Builtin && slices.Contains(strip.Extensions, named.Name)
		}), Args{NoExtensions: true, Extensions: explicit}, func(*Args) func() {
			var undo []func()
			for _, name := range strip.Extensions {
				undo = append(undo, pigstrip.Strip(pigstrip.ListExtensions, name))
			}
			return func() {
				for _, u := range undo {
					u()
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cwd, agentDir := resourceExtensionFixture(t)
			flags := tc.flags
			t.Cleanup(tc.strip(&flags))
			loader := &extensionSetLoader{CWD: cwd, AgentDir: agentDir, Settings: codingagent.NewSettingsManager(cwd, agentDir), Inline: tc.inline, Flags: flags}
			builtins, errs, _ := loader.loadBuiltinsAfter(nil)
			if !slices.Equal(errs, unknown) {
				t.Fatalf("errors = %+v, want %+v", errs, unknown)
			}
			if len(builtins) != 0 {
				t.Fatalf("loaded %d stripped built-ins", len(builtins))
			}
		})
	}
}

// Stock: the same explicit paths load every built-in this build has, and only the unknown name fails.
func TestUnstrippedExplicitBuiltinsLoad(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	var explicit, want []string
	for _, name := range cliBuiltinExtensionNames() {
		// The llama host, not the built-in pass, loads builtin:llama.cpp.
		if path := codingagent.BuiltinPathPrefix + name; path != codingagent.LlamaExtensionPath {
			explicit = append(explicit, path)
			want = append(want, path)
		}
	}
	loader := &extensionSetLoader{CWD: cwd, AgentDir: agentDir, Settings: codingagent.NewSettingsManager(cwd, agentDir), Inline: builtInExtensions,
		Flags: Args{NoExtensions: true, Extensions: append(explicit, "builtin:missing")}}
	builtins, errs, _ := loader.loadBuiltinsAfter(nil)
	if want := []extensionSetError{{Path: "builtin:missing", Error: "Unknown built-in extension: builtin:missing"}}; !slices.Equal(errs, want) {
		t.Fatalf("errors = %+v, want %+v", errs, want)
	}
	var got []string
	for _, ext := range builtins {
		got = append(got, ext.Path)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("loaded %q, want %q", got, want)
	}
}
