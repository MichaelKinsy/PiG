package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func TestResolveExtConfigMapsConventionalFactory(t *testing.T) {
	root := t.TempDir()
	writeResolverFile(t, filepath.Join(root, "go.mod"), "module example.com/review\n\ngo 1.26\n")
	writeResolverFile(t, filepath.Join(root, "extension.go"), "package review\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n")
	config, definition, err := ResolveExtConfigWithIdentity(root, "review")
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "review" || config.Source != root || config.RuntimeLanguage != "go" || config.EntrypointKind != "factory" || config.Factory != "Extension" || config.Isolation != "shared-ok" {
		t.Fatalf("config = %#v", config)
	}
	if definition == nil || !definition.Packable {
		t.Fatalf("definition = %#v", definition)
	}
}

func TestResolveExtConfigBuildsEachExactFactoryPackageInOneModule(t *testing.T) {
	root := t.TempDir()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	writeResolverFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module example.com/multi\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", modfile.AutoQuote(sdkRoot)))
	for _, name := range []string{"alpha", "beta"} {
		writeResolverFile(t, filepath.Join(root, name, "extension.go"), "package "+name+"\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\""+name+"\") }\n")
	}
	if _, _, err := ResolveExtConfig(root); err == nil || !strings.Contains(err.Error(), "example.com/multi/alpha") || !strings.Contains(err.Error(), "example.com/multi/beta") {
		t.Fatalf("module root ambiguity = %v", err)
	}
	cacheRoot := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		config, definition, err := ResolveExtConfig(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if config.Name != name || config.Source != root || config.Package != "example.com/multi/"+name || definition.Root != root {
			t.Fatalf("%s config=%#v definition=%#v", name, config, definition)
		}
		extension, err := goExtensionFromConfig(config)
		if err != nil {
			t.Fatal(err)
		}
		cell, err := runtimecell.BuildGoPackedCell(context.Background(), cacheRoot, "exact-"+name, []runtimecell.GoExtension{extension})
		if err != nil {
			t.Fatalf("build %s: %v", name, err)
		}
		if _, err := os.Stat(cell.BinaryPath); err != nil {
			t.Fatalf("%s binary: %v", name, err)
		}
	}
}

// TestResolveExtConfigPacksNodeFactoryByDefault pins N8's intentional
// default: a conventional Node factory (a default export, whether resolved
// for a project-discovered extension or a `-e` command-line path -
// ResolveExtConfig is the exact resolver pig's `-e` handling uses,
// cmd/pig/configured_resources.go's pathToExtConfigs) packs into the
// session's one shared Node cell, matching Go/Rust/Python factories. This
// mirrors upstream: Pi's cli/args.ts merges a `-e`/`--extension` path into
// the same extension list as every other discovered extension with no
// special isolation, and loader.ts loads that whole list into its single
// process. Before N8, every Node factory defaulted to Isolation: "isolated"
// (one process each); this test used to pin that old default under the name
// TestResolveExtConfigKeepsNodeFactoryIsolated. definition.Packable stays
// false for node regardless: that field also drives Piglet Binary
// fusibility, which Node components never support (D19); packing and
// fusibility are separate questions. The standalone/exact-executable form
// (a shebang script, not a factory) is unaffected and still isolated - see
// TestResolveExtConfigMapsExactExecutableToStandalone.
func TestResolveExtConfigPacksNodeFactoryByDefault(t *testing.T) {
	root := t.TempDir()
	writeResolverFile(t, filepath.Join(root, "index.js"), "export default function extension(pi) {}\n")
	config, definition, err := ResolveExtConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeLanguage != "node" || config.EntrypointKind != "factory" || config.Isolation != "shared-ok" || definition.Packable {
		t.Fatalf("node config/definition = %#v / %#v", config, definition)
	}
}

func TestResolveExtConfigMapsExactExecutableToStandalone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "standalone")
	writeResolverFile(t, path, "binary")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	config, _, err := ResolveExtConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "standalone" || config.Path != path || config.Source != "" || config.EntrypointKind != "standalone" || config.Isolation != "isolated" {
		t.Fatalf("config = %#v", config)
	}
}

func writeResolverFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
