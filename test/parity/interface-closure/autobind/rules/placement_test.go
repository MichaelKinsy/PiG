package rules

import (
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func memSource(files map[string]string) Source {
	return func(file string) (string, bool) {
		s, ok := files[file]
		return s, ok
	}
}

func TestParseModule(t *testing.T) {
	m := ParseModule(`
import { Local as L, type T } from "./other.ts";
// export { Commented } from "./c.ts";
export { a, type B, c as d } from "./x.ts";
export type { E } from "./e.ts";
export * from "./star.ts";
export * as ns from "./ns.ts";
export { L as Renamed };
export class K {}
export default abstract class Z {}
export async function f() {}
export const enum En { A }
export declare const dc: number;
`)
	got := map[string]Export{}
	for _, e := range m.Exports {
		got[e.Name] = e
	}
	for name, want := range map[string]Export{
		"a":       {Name: "a", Orig: "a", From: "./x.ts"},
		"B":       {Name: "B", Orig: "B", From: "./x.ts", TypeOnly: true},
		"d":       {Name: "d", Orig: "c", From: "./x.ts"},
		"E":       {Name: "E", Orig: "E", From: "./e.ts", TypeOnly: true},
		"ns":      {Name: "ns", From: "./ns.ts"},
		"Renamed": {Name: "Renamed", Orig: "Local", From: "./other.ts"},
		"K":       {Name: "K", Orig: "K"},
		"Z":       {Name: "Z", Orig: "Z"},
		"f":       {Name: "f", Orig: "f"},
		"En":      {Name: "En", Orig: "En"},
		"dc":      {Name: "dc", Orig: "dc"},
	} {
		if got[name] != want {
			t.Errorf("export %s = %+v, want %+v", name, got[name], want)
		}
	}
	if _, ok := got["Commented"]; ok {
		t.Error("a commented export must be ignored")
	}
	if !slices.Equal(m.Stars, []string{"./star.ts"}) {
		t.Errorf("stars = %v", m.Stars)
	}
}

func TestResolveExport(t *testing.T) {
	src := memSource(map[string]string{
		"p/src/index.ts": `export { Widget as Gadget, type Opts } from "./components/widget.ts";
export * from "./util.ts";
export { Marked } from "marked";
export * as colors from "./colors.ts";
export * from "./cycle-a.ts";`,
		"p/src/components/widget.ts": `export class Widget {}
export interface Opts { a: string }`,
		"p/src/util.ts": `export * from "./deep/index.ts";
export function helper() {}`,
		"p/src/deep/index.ts": `export { inner } from "./inner.js";`,
		"p/src/deep/inner.ts": `export const inner = 1;`,
		"p/src/colors.ts":     `export const red = 1;`,
		"p/src/cycle-a.ts":    `export * from "./cycle-b.ts";`,
		"p/src/cycle-b.ts":    `export * from "./cycle-a.ts";`,
	})
	for _, tc := range []struct {
		name string
		want Definition
		ok   bool
	}{
		{"Gadget", Definition{File: "p/src/components/widget.ts", Name: "Widget"}, true},
		{"Opts", Definition{File: "p/src/components/widget.ts", Name: "Opts"}, true},
		{"helper", Definition{File: "p/src/util.ts", Name: "helper"}, true},
		{"inner", Definition{File: "p/src/deep/inner.ts", Name: "inner"}, true},
		{"Marked", Definition{File: "marked", Name: "Marked", External: true}, true},
		{"colors", Definition{File: "p/src/colors.ts", Name: "colors", Alias: true}, true},
		{"Widget", Definition{}, false},  // the original name is not exported by the barrel
		{"missing", Definition{}, false}, // and a cycle terminates
	} {
		got, ok := ResolveExport(src, "p/src/index.ts", tc.name)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ResolveExport(%s) = %+v, %v; want %+v, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPlacementDirs(t *testing.T) {
	pm := ParsePortMap("| `packages/tui/src/components/loader.ts` | `tui/loader.go` | ✅ |\n" +
		"| `packages/tui/src/autocomplete.ts` | `tui/autocomplete.go + tui/file_autocomplete.go` | ✅ |\n" +
		"| `packages/tui/src/utils.ts` | `tui/widthx/width.go (shared), tui/utils.go` | ✅ |\n" +
		"| `packages/tui/src/components/new.ts` | `n/a` | n/a |\n" +
		"not a row\n")
	p := PlacementTable{PortMap: pm, Seeds: map[string][]string{"tui": {"tui", "tui/widthx", "internal/imageprocessing"}, "ai": {"ai"}}}
	for _, tc := range []struct {
		pkg, file string
		want      []string
	}{
		{"tui", "packages/tui/src/components/loader.ts", []string{"tui", "tui/widthx", "internal/imageprocessing"}},
		{"tui", "packages/tui/src/utils.ts", []string{"tui/widthx", "tui", "internal/imageprocessing"}},
		// A file PORT_MAP does not list takes the directories of its listed siblings, then the seeds.
		{"tui", "packages/tui/src/components/unlisted.ts", []string{"tui", "tui/widthx", "internal/imageprocessing"}},
		{"ai", "packages/ai/src/x.ts", []string{"ai"}},
	} {
		if got := p.Dirs(tc.pkg, tc.file); !slices.Equal(got, tc.want) {
			t.Errorf("Dirs(%s) = %v, want %v", tc.file, got, tc.want)
		}
	}
	if !slices.Equal(pm["packages/tui/src/autocomplete.ts"], []string{"tui/autocomplete.go", "tui/file_autocomplete.go"}) {
		t.Errorf("PORT_MAP go files = %v", pm["packages/tui/src/autocomplete.ts"])
	}
	if _, ok := pm["packages/tui/src/components/new.ts"]; ok {
		t.Error("a row with no Go file maps nowhere")
	}
}

func TestMethodAndFunctionForms(t *testing.T) {
	scope := checkCall(t, `
type Box struct{}
type Other struct{}
func (Box) Render(width int) error                          { return nil }
func (Other) Render(width int) error                        { return nil }
func Render(b Box, width int) error                         { return nil }
func RenderCtx(ctx context.Context, b Box, width int) error { return nil }
func RenderOther(o Other, width int) error                  { return nil }
`)
	env := testEnv(scope)
	box := scope.Lookup("Box").Type()
	msig := func(recv types.Type, name string) *types.Signature {
		obj, _, _ := types.LookupFieldOrMethod(recv, false, scope.Lookup("Box").Pkg(), name)
		return obj.Type().(*types.Signature)
	}
	up := call("void", Param{Name: "b", Type: "Box"}, Param{Name: "width", Type: "number"})
	// Free function render(b: Box, width) is the method Box.Render(width).
	if v := MethodOfFirstParam(env, up, box, msig(box, "Render")); v.OK != Yes {
		t.Fatalf("method form: %+v", v)
	}
	// The same function is not the method of an unrelated receiver.
	other := scope.Lookup("Other").Type()
	if v := MethodOfFirstParam(env, up, other, msig(other, "Render")); v.OK == Yes {
		t.Fatalf("wrong receiver accepted: %+v", v)
	}
	if v := MethodOfFirstParam(env, call("void"), box, msig(box, "Render")); v.OK != No {
		t.Fatalf("no first parameter: %+v", v)
	}
	// An optional first parameter cannot be a receiver, which is always present.
	optFirst := call("void", Param{Name: "b", Type: "Box", Optional: true}, Param{Name: "width", Type: "number"})
	if v := MethodOfFirstParam(env, optFirst, box, msig(box, "Render")); v.OK != No || !strings.Contains(v.Why, "optional") {
		t.Fatalf("optional first parameter: %+v", v)
	}
	// Method Box.render(width) of an upstream class is the Go function Render(b Box, width), with or without a context.
	meth := call("void", Param{Name: "width", Type: "number"})
	for _, fn := range []string{"Render", "RenderCtx"} {
		if v := FirstParamOfMethod(env, "Box", meth, sigOf(t, scope, fn)); v.OK != Yes {
			t.Errorf("function form %s: %+v", fn, v)
		}
	}
	if v := FirstParamOfMethod(env, "Box", meth, sigOf(t, scope, "RenderOther")); v.OK == Yes {
		t.Errorf("function form with another owner accepted: %+v", v)
	}
}

func TestGenerics(t *testing.T) {
	scope := checkCall(t, `
type Pair[A, B any] struct{ a A; b B }
type Plain struct{}
type Strings = []string
func Map[T, U any](xs []T, f func(T) U) []U { return nil }
`)
	one, two := []TypeParam{{Name: "T"}}, []TypeParam{{Name: "T"}, {Name: "U"}}
	pair, plain, mp := scope.Lookup("Pair").Type(), scope.Lookup("Plain").Type(), scope.Lookup("Map").Type()
	for _, tc := range []struct {
		name  string
		up    []TypeParam
		typ   types.Type
		insts map[string]string
		want  Tri
	}{
		{"same count", two, pair, nil, Yes},
		{"generic function", two, mp, nil, Yes},
		{"count differs", one, pair, nil, No},
		{"not generic both sides", nil, plain, nil, Yes},
		{"upstream not generic", nil, pair, nil, No},
		{"concrete without documentation", one, plain, nil, Unknown},
		{"documented instantiation", one, plain, map[string]string{"Box": "x.Plain"}, Yes},
		{"wrong documented instantiation", one, plain, map[string]string{"Box": "x.Pair"}, No},
	} {
		if got := Generics(tc.up, "Box", tc.typ, tc.insts); got.OK != tc.want {
			t.Errorf("%s: %+v, want %v", tc.name, got, tc.want)
		}
	}
	if names := TypeParamNames(Call{TypeParameters: two}); !names["T"] || !names["U"] || len(names) != 2 {
		t.Errorf("type parameter names %v", names)
	}
}

// mirror locates the pinned upstream mirror and PORT_MAP of the repository.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, rel)
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found")
		}
	}
}

// Trust check on the pinned mirror: every name that a package barrel exports resolves to a declaration or an external package,
// and the Go directory table places the declaring file of well-known symbols.
func TestBarrelsOfPinnedUpstream(t *testing.T) {
	root := repoFile(t, ".upstream/current")
	src := func(file string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		return string(b), err == nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	resolver := NewResolver(src)
	for _, pkg := range entries {
		file := "packages/" + pkg.Name() + "/src/index.ts"
		text, ok := src(file)
		if !ok {
			continue
		}
		for _, e := range ParseModule(text).Exports {
			total++
			if _, ok := resolver.Resolve(file, e.Name); !ok {
				t.Errorf("%s: export %s does not resolve to a declaration", file, e.Name)
			}
		}
	}
	if total < 300 {
		t.Fatalf("only %d barrel exports parsed; the parser lost most of them", total)
	}
	def, ok := resolver.Resolve("packages/tui/src/index.ts", "Loader")
	if !ok || def.File != "packages/tui/src/components/loader.ts" {
		t.Fatalf("Loader resolves to %+v", def)
	}
	def, ok = resolver.Resolve("packages/tui/src/index.ts", "CombinedAutocompleteProvider")
	if !ok || def.File != "packages/tui/src/autocomplete.ts" {
		t.Fatalf("CombinedAutocompleteProvider resolves to %+v", def)
	}
	if def, ok = resolver.Resolve("packages/tui/src/index.ts", "Marked"); !ok || !def.External {
		t.Fatalf("Marked resolves to %+v", def)
	}
	md, err := os.ReadFile(repoFile(t, "docs/parity/PORT_MAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	pm := ParsePortMap(string(md))
	if len(pm) < 500 {
		t.Fatalf("PORT_MAP parsed to %d rows", len(pm))
	}
	p := PlacementTable{PortMap: pm, Seeds: map[string][]string{"tui": {"tui"}}}
	if dirs := p.Dirs("tui", "packages/tui/src/components/loader.ts"); len(dirs) == 0 || dirs[0] != "tui" {
		t.Fatalf("loader.ts placed in %v", dirs)
	}
	if dirs := p.Dirs("tui", "packages/tui/src/stdin-buffer.ts"); !slices.Contains(dirs, "internal/codingagent") || !strings.HasPrefix(dirs[0], "tui") {
		t.Fatalf("stdin-buffer.ts placed in %v", dirs)
	}
}

const portMapSample = "| `packages/durable/src/harness/agent.ts` | `durable/harness/agent.go` | ✅ |\n" +
	"| `packages/durable/src/harness/tool.ts` | `durable/harness/tool.go + durable/harness/tool_args.go` | ✅ |\n" +
	"| `packages/durable/src/env/index.ts` | `durable/env/env.go (shared), durable/env/errors.go` | ✅ |\n" +
	"| `packages/durable/src/x.ts` | `durable/testonly/agent_test.go` | ✅ |\n" +
	"| `packages/durable/src/y.ts` | `n/a: designed out` | n/a |\n" +
	"| `packages/chord/src/index.ts` | `chord/chord.go` | ✅ |\n" +
	"not a table row\n"

// P2: the directories of the Go files PORT_MAP lists for the package, most listed first; test files and rows without a Go file add none.
func TestPortMapDirs(t *testing.T) {
	got := PortMapDirs(portMapSample, "durable")
	want := []string{"durable/harness", "durable/env"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("PortMapDirs(durable) = %v, want %v", got, want)
	}
	if got := PortMapDirs(portMapSample, "chord"); len(got) != 1 || got[0] != "chord" {
		t.Fatalf("PortMapDirs(chord) = %v", got)
	}
	if got := PortMapDirs(portMapSample, "ai"); len(got) != 0 {
		t.Fatalf("PortMapDirs(ai) = %v, want none", got)
	}
}

// The registered rule reads the repository's own PORT_MAP: durable's counterparts live under durable/, which the engine's seed list does not name.
func TestPortMapPlacementCoversPackagesWithoutSeeds(t *testing.T) {
	if !strings.Contains(portMapMarkdown(), "packages/durable/") {
		t.Skip("this tree's PORT_MAP has no durable rows (a tree before the durable package was ported)")
	}
	dirs := Dirs("durable")
	for _, want := range []string{"durable/harness", "durable/env"} {
		found := false
		for _, d := range dirs {
			found = found || d == want
		}
		if !found {
			t.Errorf("Dirs(durable) = %v, missing %s", dirs, want)
		}
	}
}

// P2 shape directories: a directory that PORT_MAP also lists for another upstream package is searched by name only. Without this,
// the engine's unique-shape rule (N5) accepted cmd/pig rpcTemplateString for ai inferCopilotInitiator and internal/codingagent
// readClipboardFilePaths for ai getBuiltinProviders.
func TestPortMapExclusiveDirs(t *testing.T) {
	sample := portMapSample +
		"| `packages/durable/src/z.ts` | `internal/shared/z.go` | ✅ |\n" +
		"| `packages/ai/src/z.ts` | `internal/shared/y.go` | ✅ |\n"
	if got := PortMapDirs(sample, "ai"); len(got) != 1 || got[0] != "internal/shared" {
		t.Fatalf("PortMapDirs(ai) = %v, want [internal/shared]", got)
	}
	if got := PortMapExclusiveDirs(sample, "ai"); len(got) != 0 {
		t.Fatalf("PortMapExclusiveDirs(ai) = %v, want none: internal/shared also holds a durable port", got)
	}
	got := PortMapExclusiveDirs(sample, "durable")
	if len(got) != 2 || got[0] != "durable/harness" || got[1] != "durable/env" {
		t.Fatalf("PortMapExclusiveDirs(durable) = %v, want [durable/harness durable/env]", got)
	}
	var rule PlacementRule = portMapPlacement{}
	if _, ok := rule.(ShapePlacementRule); !ok {
		t.Fatal("P2 must restrict its shape directories")
	}
}
