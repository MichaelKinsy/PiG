package subprocess

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestPlanCellsSeparatesIndependentPythonModuleRoots(t *testing.T) {
	configs := []ExtConfig{
		{Name: "first", Source: "/first", Package: "flags.first", Factory: "new_extension", RuntimeKind: "subprocess", RuntimeLanguage: "python", EntrypointKind: "factory", Enabled: true},
		{Name: "second", Source: "/second", Package: "flags.second", Factory: "new_extension", RuntimeKind: "subprocess", RuntimeLanguage: "python", EntrypointKind: "factory", Enabled: true},
	}
	cells := PlanCells(configs, nil)
	if len(cells) != len(configs) {
		t.Fatalf("independent modules share sys.modules: %#v", cells)
	}
	for i, cell := range cells {
		if len(cell.Extensions) != 1 || cell.Extensions[0].Name != configs[i].Name || cell.Order != i {
			t.Fatalf("configured order changed: %#v", cells)
		}
	}
	configs[1].Source = configs[0].Source
	if shared := PlanCells(configs, nil); len(shared) != 1 {
		t.Fatalf("same module root should remain shareable: %#v", shared)
	}
}

func BenchmarkRegisteredFlagSnapshot(b *testing.B) {
	h := NewHost(b.TempDir())
	bridge := NewUIBridge(func() {})
	h.SetUIBridge(bridge)
	names := make([]string, 8)
	for i := range names {
		names[i] = fmt.Sprintf("flag-%d", i)
	}
	for i := range 64 {
		name := fmt.Sprintf("extension-%d", i)
		flags := make(map[string]extension.ExtensionFlag)
		defaults := make(map[string]any)
		for _, name := range names {
			flags[name] = extension.ExtensionFlag{Name: name, Type: "string", Default: "default"}
			defaults[name] = "default"
		}
		h.exts[name] = &managedExt{ext: &extension.Extension{Flags: flags}, flagDefaults: defaults}
		h.loadOrder[name] = i
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot := bridge.Snapshot(names, 0, false)
		if len(snapshot.Flags) != len(names) {
			b.Fatal("flag snapshot incomplete")
		}
	}
}
