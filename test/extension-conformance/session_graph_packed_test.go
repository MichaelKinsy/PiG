package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/sessiongraph"
)

// sessionGraphPackedSDKs are the session-graph fixtures of the Go, Rust and Python SDKs loaded into a shared cell next to a second extension of their language, so that the extension under test shares its runtime process with another. A cell is shared only by extensions of one dependency set, so the fixture and its companion are built from the same manifest.
func sessionGraphPackedSDKs() map[string]sessionGraphLoader {
	pack := func(language string, build func(t testing.TB, root string) (fixture, companion subprocess.ExtConfig)) sessionGraphLoader {
		return func(t testing.TB, r *sessionGraphRig) {
			fixture, companion := build(t, findModuleRoot(t))
			configs := []subprocess.ExtConfig{fixture, companion}
			for i := range configs {
				configs[i].Isolation, configs[i].Enabled = "shared-ok", true
				configs[i].RuntimeKind, configs[i].RuntimeLanguage, configs[i].EntrypointKind = "subprocess", language, "factory"
			}
			r.host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			loaded, err := r.host.Reload(t.Context())
			if err != nil || len(loaded) != len(configs) {
				t.Fatalf("packed %s: %v (%+v)", language, err, r.host.LastReloadReport())
			}
			if report := r.host.LastReloadReport(); report == nil || len(report.Cells) != 1 || len(report.Cells[0].Extensions) != len(configs) {
				t.Fatalf("packed %s is not one shared cell: %+v", language, report)
			}
			for _, ext := range loaded {
				if ext.Name == sessiongraph.Name {
					r.finish(ext)
					return
				}
			}
			t.Fatalf("packed %s: %s not loaded", language, sessiongraph.Name)
		}
	}
	write := func(t testing.TB, files map[string]string) {
		t.Helper()
		for path, text := range files {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return map[string]sessionGraphLoader{
		"go-packed": pack("go", func(t testing.TB, root string) (subprocess.ExtConfig, subprocess.ExtConfig) {
			dir := t.TempDir()
			cfg := func(name, body string) subprocess.ExtConfig {
				source := filepath.Join(dir, name)
				write(t, map[string]string{
					filepath.Join(source, "extension.go"): body,
					filepath.Join(source, "go.mod"): fmt.Sprintf("module example.com/%s\n\ngo 1.26.0\nrequire (\n github.com/MichaelKinsy/PiG v%s\n github.com/MichaelKinsy/PiG/extensions/sdk v%s\n)\nreplace github.com/MichaelKinsy/PiG => %s\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n",
						name, pigversion.PigVersion, pigversion.PigVersion, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "extensions", "sdk"))),
				})
				return subprocess.ExtConfig{Name: name, Source: source, SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/" + name, Package: "example.com/" + name, Factory: "Extension"}
			}
			return cfg(sessiongraph.Name, "package fixture\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nimport \"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/sessiongraph\"\nfunc Extension() *sdk.Extension { return sessiongraph.Extension() }\n"),
				cfg("packed-companion", "package fixture\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return sdk.New(\"packed-companion\") }\n")
		}),
		"rust-packed": pack("rust", func(t testing.TB, root string) (subprocess.ExtConfig, subprocess.ExtConfig) {
			dir := t.TempDir()
			manifest := func(name string) string {
				return fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2024\"\n[dependencies]\npig-sdk = {path = %q}\nserde_json = {version = \"1\", features = [\"preserve_order\"]}\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs")))
			}
			fixtureSource := filepath.Join(dir, "session-graph-fixture")
			sources, err := filepath.Glob(filepath.Join(root, "test/extension-conformance/testdata/session-graph-rust/src/*.rs"))
			if err != nil || len(sources) == 0 {
				t.Fatalf("session-graph-rust sources: %v %v", sources, err)
			}
			files := map[string]string{filepath.Join(fixtureSource, "Cargo.toml"): manifest("session-graph-fixture")}
			for _, source := range sources {
				text, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				files[filepath.Join(fixtureSource, "src", filepath.Base(source))] = string(text)
			}
			companionSource := filepath.Join(dir, "packed-companion")
			files[filepath.Join(companionSource, "Cargo.toml")] = manifest("packed-companion")
			files[filepath.Join(companionSource, "src", "lib.rs")] = "pub fn new_extension() -> pig_sdk::Extension { pig_sdk::Extension::new(\"packed-companion\") }\n"
			write(t, files)
			return subprocess.ExtConfig{Name: sessiongraph.Name, Source: fixtureSource, SDKName: "pig-sdk", Package: "session-graph-fixture", Factory: "new_extension"},
				subprocess.ExtConfig{Name: "packed-companion", Source: companionSource, SDKName: "pig-sdk", Package: "packed-companion", Factory: "new_extension"}
		}),
		"python-packed": pack("python", func(t testing.TB, root string) (subprocess.ExtConfig, subprocess.ExtConfig) {
			fixture := packedFlagFactory(t, root, "python", sessiongraph.Name, false)
			fixture.ContentHash = "python-session-graph-packed-" + fmt.Sprint(len(sessionGraphPython))
			write(t, map[string]string{filepath.Join(fixture.Source, fixture.Package+".py"): sessionGraphPython})
			return fixture, packedFlagFactory(t, root, "python", "packed-companion", false)
		}),
	}
}
