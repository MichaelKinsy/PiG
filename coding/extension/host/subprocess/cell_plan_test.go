package subprocess

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestPlanCellsGroupsGoFactoryAndFissionsQuarantinedCell(t *testing.T) {
	configs := []ExtConfig{
		packableConfig("a", "ha"),
		packableConfig("b", "hb"),
		{Name: "rust", Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "rust", EntrypointKind: "source", Source: "/tmp/rust"},
	}
	cells := PlanCells(configs, nil)
	var packed *CellSpec
	isolated := 0
	for i := range cells {
		if cells[i].Strategy == CellStrategyPackedGo {
			packed = &cells[i]
		} else {
			isolated++
		}
	}
	if packed == nil || len(packed.Extensions) != 2 {
		t.Fatalf("cells = %+v, want one packed-go cell with a+b", cells)
	}
	if isolated != 1 {
		t.Fatalf("isolated count = %d, want rust isolated", isolated)
	}

	fissioned := PlanCells(configs, map[string]string{packed.Key: "crashed"})
	for _, cell := range fissioned {
		if cell.Strategy == CellStrategyPackedGo {
			t.Fatalf("quarantined packed cell still planned: %+v", fissioned)
		}
	}
	if len(fissioned) != 3 {
		t.Fatalf("fissioned cells = %+v, want 3 isolated cells", fissioned)
	}
}

// z-packed-first and a-packed-last share a packGroupKey but are not adjacent
// in configured order (middle-isolated sits between them), so they must NOT
// merge into one cell (CNEO-001): pig starts a later cell's process only
// after every earlier cell has committed (stageCellsInOrder), so merging them
// would activate a-packed-last's factory before middle-isolated's, even
// though a-packed-last was configured after it. This gives up packing them
// together (two packed-go cells/processes instead of one) in exchange for
// exact activation order; see the PlanCells doc comment.
func TestPlanCellsPreservesConfiguredCellAndMemberOrder(t *testing.T) {
	configs := []ExtConfig{
		packableConfig("z-packed-first", "hz"),
		{Name: "middle-isolated", Path: "/tmp/middle", Enabled: true},
		packableConfig("a-packed-last", "ha"),
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 3 {
		t.Fatalf("cells = %+v, want packed(z), isolated(middle), packed(a): one cell per config, none merged across the interleaving isolated cell", cells)
	}
	if cells[0].Strategy != CellStrategyPackedGo || !slices.Equal(cellExtNames(cells[0]), []string{"z-packed-first"}) {
		t.Fatalf("first cell = %+v, want a packed cell holding only z-packed-first", cells[0])
	}
	if cells[1].Strategy != CellStrategyIsolated || cells[1].Extensions[0].Name != "middle-isolated" {
		t.Fatalf("second cell = %+v, want middle isolated extension", cells[1])
	}
	if cells[2].Strategy != CellStrategyPackedGo || !slices.Equal(cellExtNames(cells[2]), []string{"a-packed-last"}) {
		t.Fatalf("third cell = %+v, want a separate packed cell holding only a-packed-last", cells[2])
	}
}

// TestPlanCellsSplitsPackGroupAcrossInterleavedIsolatedCell is CNEO-001's
// regression: a Node factory, an isolated extension (any language; a
// packable Go factory forced isolated stands in for the interleaved-Go
// example in the finding, since PlanCells' contiguous-run logic makes no
// language distinction here), and a second Node factory in that order must
// activate in that order. Before the fix, PlanCells grouped every packable
// config sharing a packGroupKey regardless of what sat between them, so the
// two Node factories packed into one cell ordered at the first one's index -
// activating the second Node factory (in the same cell/process, per
// cell.mjs's plan-order install loop) before the isolated extension, even
// though it was configured after it.
func TestPlanCellsSplitsPackGroupAcrossInterleavedIsolatedCell(t *testing.T) {
	middle := packableConfig("go-mid-isolated", "hmid")
	middle.Isolation = "isolated"
	configs := []ExtConfig{
		packableNodeConfig("node-a", "hnode-a"),
		middle,
		packableNodeConfig("node-c", "hnode-c"),
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 3 {
		t.Fatalf("cells = %+v, want 3: packed(node-a), isolated(go-mid-isolated), packed(node-c)", cells)
	}
	if cells[0].Strategy != CellStrategyPackedNode || !slices.Equal(cellExtNames(cells[0]), []string{"node-a"}) {
		t.Fatalf("first cell = %+v, want a Node cell holding only node-a", cells[0])
	}
	if cells[1].Strategy != CellStrategyIsolated || cells[1].Extensions[0].Name != "go-mid-isolated" {
		t.Fatalf("second cell = %+v, want the isolated middle extension", cells[1])
	}
	if cells[2].Strategy != CellStrategyPackedNode || !slices.Equal(cellExtNames(cells[2]), []string{"node-c"}) {
		t.Fatalf("third cell = %+v, want a separate Node cell holding only node-c", cells[2])
	}
	if cells[0].Order >= cells[1].Order || cells[1].Order >= cells[2].Order {
		t.Fatalf("cell Order = [%d %d %d], want strictly increasing (configured order)", cells[0].Order, cells[1].Order, cells[2].Order)
	}
}

func TestPlanCellsDoesNotPackNonFactoryOrStrictIsolation(t *testing.T) {
	factory := packableConfig("factory", "hf")
	strict := packableConfig("strict", "hs")
	strict.Isolation = "strict"
	source := packableConfig("source", "hsrc")
	source.EntrypointKind = "source"
	cells := PlanCells([]ExtConfig{factory, strict, source}, nil)
	packed := 0
	for _, cell := range cells {
		if cell.Strategy == CellStrategyPackedGo {
			packed++
			if len(cell.Extensions) != 1 || cell.Extensions[0].Name != "factory" {
				t.Fatalf("unexpected packed cell: %+v", cell)
			}
		}
	}
	if packed != 1 {
		t.Fatalf("packed count = %d, cells=%+v", packed, cells)
	}
}

func TestPlanCellsGroupsRustFactory(t *testing.T) {
	configs := []ExtConfig{
		packableRustConfig("ra", "hra"),
		packableRustConfig("rb", "hrb"),
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedRust || len(cells[0].Extensions) != 2 {
		t.Fatalf("cells = %+v, want one packed-rust cell", cells)
	}
	exts, err := cells[0].RustExtensions()
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 2 || exts[0].Factory != "new_extension" {
		t.Fatalf("rust extensions = %+v", exts)
	}
}

func TestPlanCellsGroupsPythonFactory(t *testing.T) {
	configs := []ExtConfig{
		packablePythonConfig("pa", "hpa"),
		packablePythonConfig("pb", "hpb"),
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 1 || cells[0].Strategy != CellStrategyPackedPython || len(cells[0].Extensions) != 2 {
		t.Fatalf("cells = %+v, want one packed-python cell", cells)
	}
	exts, err := cells[0].PythonExtensions()
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 2 || exts[0].Factory != "new_extension" {
		t.Fatalf("python extensions = %+v", exts)
	}
}

func TestCellSpecGoExtensionsRequiresPackedGo(t *testing.T) {
	cell := isolatedCell(packableConfig("a", "ha"), "test")
	if _, err := cell.GoExtensions(); err == nil {
		t.Fatal("GoExtensions on isolated cell error = nil")
	}
	packed := packedCell("test", []ExtConfig{packableConfig("a", "ha")})
	exts, err := packed.GoExtensions()
	if err != nil {
		t.Fatal(err)
	}
	if len(exts) != 1 || exts[0].Name != "a" || exts[0].Factory != "Extension" {
		t.Fatalf("go extensions = %+v", exts)
	}
}

func TestPlanCellsSeparatesExplicitGoSDKOverrides(t *testing.T) {
	configs := make([]ExtConfig, 0, 2)
	for i, name := range []string{"a", "b"} {
		sdkRoot := filepath.Join(t.TempDir(), "sdk")
		if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sdkRoot, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		extRoot := t.TempDir()
		goMod := "module example.com/" + name + "\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + modfile.AutoQuote(filepath.ToSlash(sdkRoot)) + "\n"
		if err := os.WriteFile(filepath.Join(extRoot, "go.mod"), []byte(goMod), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := packableConfig(name, string(rune('a'+i)))
		cfg.Source = extRoot
		configs = append(configs, cfg)
	}
	cells := PlanCells(configs, nil)
	if len(cells) != 2 {
		t.Fatalf("cells = %+v, want one cell per explicit SDK override", cells)
	}
	for _, cell := range cells {
		if len(cell.Extensions) != 1 {
			t.Fatalf("mixed explicit SDK overrides in cell: %+v", cell)
		}
	}
}

func packablePythonConfig(name, hash string) ExtConfig {
	cfg := packableConfig(name, hash)
	cfg.RuntimeLanguage = "python"
	cfg.SDKName = "pig-sdk-py"
	cfg.Package = "example_" + name
	cfg.Factory = "new_extension"
	return cfg
}

func packableRustConfig(name, hash string) ExtConfig {
	cfg := packableConfig(name, hash)
	cfg.RuntimeLanguage = "rust"
	cfg.SDKName = "pig-sdk"
	cfg.Package = "example_" + name
	cfg.Factory = "new_extension"
	return cfg
}

func packableNodeConfig(name, hash string) ExtConfig {
	return ExtConfig{
		Name:            name,
		Enabled:         true,
		Source:          "/tmp/" + name + ".mjs",
		RuntimeKind:     "subprocess",
		RuntimeLanguage: "node",
		SDKName:         "pi-node",
		Isolation:       "shared-ok",
		EntrypointKind:  "factory",
		ContentHash:     hash,
	}
}

func packableConfig(name, hash string) ExtConfig {
	return ExtConfig{
		Name:            name,
		Enabled:         true,
		Source:          "/tmp/" + name,
		RuntimeKind:     "subprocess",
		RuntimeLanguage: "go",
		SDKName:         "github.com/MichaelKinsy/PiG/extensions/sdk",
		Isolation:       "shared-ok",
		EntrypointKind:  "factory",
		Package:         "example.com/" + name,
		Factory:         "Extension",
		ContentHash:     hash,
	}
}
