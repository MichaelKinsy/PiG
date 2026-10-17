package extensionset

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func command(name string, runs *int) extension.ExtensionFactory {
	return func(pi extension.API) error {
		if runs != nil {
			*runs++
		}
		pi.RegisterCommand(name, extension.CommandOptions{Description: name, Handler: func(context.Context, string) error { return nil }})
		return nil
	}
}

// resource-loader.ts:376-377, :1131-1153 loadExtensionFactories: the inputs that are not built-in run in order, a bare factory is named by its
// 1-based place among them, a named one by its name, and the path is `<inline:name>`. Hidden and replaceable come from the named input only.
func TestLoadInlineNamesAndFlagsTheInlineFactories(t *testing.T) {
	var builtinRuns, bareRuns int
	loader := &Loader{CWD: t.TempDir(), Inline: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "core", Builtin: true, Factory: command("core", &builtinRuns)},
		command("bare", &bareRuns),
		extension.NamedInlineExtension{Name: "named", Hidden: true, Replaceable: true, Factory: command("named", nil)},
		extension.ExtensionFactory(func(extension.API) error { return errors.New("boom") }),
	}}
	got, errs := loader.LoadInline()
	var paths []string
	for _, ext := range got {
		paths = append(paths, ext.Path)
	}
	if !reflect.DeepEqual(paths, []string{"<inline:1>", "<inline:named>"}) {
		t.Fatalf("inline paths = %q, want <inline:1> then <inline:named> (the built-in is not an inline extension)", paths)
	}
	if got[0].Hidden || got[0].Replaceable || !got[1].Hidden || !got[1].Replaceable {
		t.Fatalf("hidden/replaceable = %v/%v and %v/%v, want false/false then true/true", got[0].Hidden, got[0].Replaceable, got[1].Hidden, got[1].Replaceable)
	}
	if !reflect.DeepEqual(errs, []LoadError{{Path: "<inline:3>", Error: "boom"}}) {
		t.Fatalf("errors = %+v, want the failing third factory named <inline:3>", errs)
	}
	if builtinRuns != 0 || bareRuns != 1 {
		t.Fatalf("runs: built-in %d, bare %d, want 0 and 1", builtinRuns, bareRuns)
	}
}

// resource-loader.ts:722-742 loadExtensionPaths: a `builtin:<name>` path runs the named built-in, hidden, with its replaceable flag; a name
// with no built-in is the error `Unknown built-in extension: <path>`.
func TestLoadBuiltinRunsKnownNamesAndReportsUnknownOnes(t *testing.T) {
	loader := &Loader{CWD: t.TempDir(), Inline: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Replaceable: true, Factory: command("alpha", nil)},
	}}
	got, errs := loader.LoadBuiltin([]string{"builtin:alpha", "builtin:beta"})
	if len(got) != 1 || got[0].Path != "builtin:alpha" || !got[0].Hidden || !got[0].Replaceable {
		t.Fatalf("loaded = %+v, want builtin:alpha hidden and replaceable", got)
	}
	if !reflect.DeepEqual(errs, []LoadError{{Path: "builtin:beta", Error: "Unknown built-in extension: builtin:beta"}}) {
		t.Fatalf("errors = %+v, want builtin:beta unknown", errs)
	}
	if loader.factoryRuntime() == nil || got[0].Name != "alpha" {
		t.Fatalf("runtime %v name %q", loader.factoryRuntime(), got[0].Name)
	}
}

// resource-loader.ts:114-157 omitReplacedExtensions: a replaceable extension that registers a name another extension also registers is left out;
// a replaced built-in one adds a warning naming the replacement.
func TestOmitReplacedLeavesOutAReplaceableBuiltinAndWarns(t *testing.T) {
	loader := &Loader{CWD: t.TempDir(), Inline: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Replaceable: true, Factory: command("shared", nil)},
		extension.NamedInlineExtension{Name: "other", Factory: command("shared", nil)},
	}}
	builtins, _ := loader.LoadBuiltin([]string{"builtin:alpha"})
	inline, _ := loader.LoadInline()
	var warnings []icodingagent.ExtensionWarning
	kept := OmitReplaced(append(builtins, inline...), &warnings)
	if len(kept) != 1 || kept[0].Path != "<inline:other>" || len(warnings) != 1 || warnings[0].Path != "builtin:alpha" {
		t.Fatalf("kept = %d (%v), warnings = %+v, want only <inline:other> kept and one warning for builtin:alpha", len(kept), kept, warnings)
	}
}

// resource-loader.ts:378 builds the built-in Map from the inputs in order, so of two built-ins with one name the last one runs.
func TestLoadBuiltinKeepsTheLastOfTwoEntriesWithOneName(t *testing.T) {
	var first, last int
	loader := &Loader{CWD: t.TempDir(), Inline: []extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: command("one", &first)},
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: command("two", &last)},
	}}
	got, errs := loader.LoadBuiltin([]string{"builtin:alpha"})
	if len(errs) != 0 || len(got) != 1 || first != 0 || last != 1 {
		t.Fatalf("errors %+v, loaded %d, first ran %d times, last ran %d times; want the last entry only", errs, len(got), first, last)
	}
}

// resource-loader.ts:378-383 hands the package manager [...builtinExtensions.keys()]: a name two built-in inputs share is listed once, at its first
// place, so its `builtin:<name>` path is enabled and loaded once.
// mutation-checked: listing every built-in input's name fails it.
func TestBuiltinNamesListsARepeatedNameOnce(t *testing.T) {
	got := BuiltinNames([]extension.InlineExtension{
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: command("one", nil)},
		command("inline", nil),
		extension.NamedInlineExtension{Name: "beta", Builtin: true, Factory: command("two", nil)},
		extension.NamedInlineExtension{Name: "alpha", Builtin: true, Factory: command("three", nil)},
	})
	if !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("BuiltinNames = %q, want [alpha beta]", got)
	}
}
