package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestUniqueExtensionPathsPreservesDistinctFactoriesAndFirstAlias(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	if err := os.Mkdir(module, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	// A junction on Windows: it needs no privilege, and Pi's canonicalizePath resolves it as it resolves a symbolic link.
	testenv.RequireDirectoryLink(t, module, alias)
	first := subprocess.ExtConfig{Name: "first", Source: alias, ModulePath: "example.com/extensions", Package: "example.com/extensions/first", Factory: "Extension", Enabled: true}
	second := subprocess.ExtConfig{Name: "second", Source: module, ModulePath: "example.com/extensions", Package: "example.com/extensions/second", Factory: "Extension", Enabled: true}
	duplicate := first
	duplicate.Name, duplicate.Source, duplicate.Enabled = "alias-of-first", module, false
	got := uniqueExtensionPaths([]subprocess.ExtConfig{first, second, duplicate})
	if want := []subprocess.ExtConfig{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources=%+v; want first alias and distinct native factories %+v", got, want)
	}
}

func TestUniqueExtensionPathsKeepsUnresolvedSelections(t *testing.T) {
	configs := []subprocess.ExtConfig{{Name: "first unresolved"}, {Name: "second unresolved"}}
	if got := uniqueExtensionPaths(configs); !reflect.DeepEqual(got, configs) {
		t.Fatalf("unresolved selections=%+v; want %+v", got, configs)
	}
	if got := uniqueExtensionPaths(nil); len(got) != 0 {
		t.Fatalf("empty sources=%+v", got)
	}
}

func BenchmarkUniqueExtensionPaths(b *testing.B) {
	for _, count := range []int{0, 8, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			var configs []subprocess.ExtConfig
			for i := range count {
				path := filepath.Join(root, fmt.Sprintf("extension-%d.mjs", i))
				if err := os.WriteFile(path, []byte("export default function() {}"), 0o600); err != nil {
					b.Fatal(err)
				}
				config := subprocess.ExtConfig{Name: fmt.Sprint(i), Source: path, Enabled: true}
				configs = append(configs, config, config)
			}
			b.ReportAllocs()
			for b.Loop() {
				if got := uniqueExtensionPaths(configs); len(got) != count {
					b.Fatalf("unique sources=%d; want %d", len(got), count)
				}
			}
		})
	}
}
