package extensionconformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Two independent Python roots may use the same conventional module name.
// Sharing sys.modules would run the first imported factory for both roots.
func TestPythonFactoryModuleCollisionKeepsBothIdentities(t *testing.T) {
	root := findModuleRoot(t)
	configs := []subprocess.ExtConfig{
		packedFlagFactory(t, root, "python", "z-first", true),
		packedFlagFactory(t, root, "python", "a-second", false),
	}
	for i := range configs {
		cfg := &configs[i]
		if err := os.Rename(filepath.Join(cfg.Source, cfg.Package+".py"), filepath.Join(cfg.Source, "flags.py")); err != nil {
			t.Fatal(err)
		}
		cfg.Package = "flags"
	}
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("module collision lost a factory: loaded=%d errors=%v", len(loaded), errs)
	}
	for i, ext := range loaded {
		if ext.Name != configs[i].Name {
			t.Fatalf("loaded[%d]=%q, want %q", i, ext.Name, configs[i].Name)
		}
	}
}
