package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

var packedUIConfigs = map[string]func() ([]subprocess.ExtConfig, error){
	"go":     sync.OnceValues(func() ([]subprocess.ExtConfig, error) { return createPackedUIConfigs("go") }),
	"python": sync.OnceValues(func() ([]subprocess.ExtConfig, error) { return createPackedUIConfigs("python") }),
	"rust":   sync.OnceValues(func() ([]subprocess.ExtConfig, error) { return createPackedUIConfigs("rust") }),
}

// Sources live as long as their cached runners, including Python's imported modules.
func createPackedUIConfigs(language string) ([]subprocess.ExtConfig, error) {
	root := fixtureSourceRoot
	dir := filepath.Join(fixtureRoot, "packed-ui-"+language)
	files := make(map[string]string)
	write := func(path, text string) { files[path] = text }
	name := map[string]string{"go": "sdk-fixture", "python": "python-sdk-fixture", "rust": "rust-sdk-fixture"}[language]
	source := filepath.Join(dir, name)
	peer := filepath.Join(dir, "ui-peer")
	switch language {
	case "go":
		for _, item := range []struct{ path, body string }{
			{source, `package fixture
import "github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension { return testfixture.Extension() }
`},
			{peer, `package fixture
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension { return sdk.New("ui-peer") }
`},
		} {
			write(filepath.Join(item.path, "extension.go"), item.body)
			write(filepath.Join(item.path, "go.mod"), fmt.Sprintf("module example.com/%s\n\ngo 1.26.0\nrequire (\n github.com/MichaelKinsy/PiG v%s\n github.com/MichaelKinsy/PiG/extensions/sdk v%s\n)\nreplace github.com/MichaelKinsy/PiG => %s\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Base(item.path), pigversion.PigVersion, pigversion.PigVersion, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "extensions", "sdk"))))
		}
	case "python":
		data, err := os.ReadFile(filepath.Join(root, "test", "extension-conformance", "testdata", name, "main.py"))
		if err != nil {
			return nil, err
		}
		body := string(data)
		if strings.HasPrefix(body, "#!") {
			_, body, _ = strings.Cut(body, "\n")
		}
		body, _, _ = strings.Cut(body, `if __name__ == "__main__":`)
		write(filepath.Join(source, "python_sdk_fixture.py"), body)
		write(filepath.Join(peer, "ui_peer.py"), "import pig_sdk\ndef new_extension():\n    return pig_sdk.Extension('ui-peer')\n")
	case "rust":
		data, err := os.ReadFile(filepath.Join(root, "test", "extension-conformance", "testdata", name, "src", "main.rs"))
		if err != nil {
			return nil, err
		}
		body := string(data)
		body = strings.Replace(body, "fn main() {", "pub fn new_extension() -> Extension {", 1)
		body = strings.Replace(body, "ext.run().unwrap();", "ext", 1)
		write(filepath.Join(source, "src", "lib.rs"), body)
		write(filepath.Join(peer, "src", "lib.rs"), "pub fn new_extension() -> pig_sdk::Extension { pig_sdk::Extension::new(\"ui-peer\") }\n")
		for _, path := range []string{source, peer} {
			write(filepath.Join(path, "Cargo.toml"), fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2021\"\n[dependencies]\npig-sdk = {path = %q}\nserde_json = \"1\"\n", filepath.Base(path), filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		}
	}
	for path, text := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return nil, err
		}
	}
	configs := []subprocess.ExtConfig{{Name: name, Source: source, Enabled: true}, {Name: "ui-peer", Source: peer, Enabled: true}}
	for i := range configs {
		cfg := &configs[i]
		cfg.RuntimeKind, cfg.RuntimeLanguage, cfg.Isolation, cfg.EntrypointKind = "subprocess", language, "shared-ok", "factory"
		cfg.Factory, cfg.Package = "new_extension", cfg.Name
		switch language {
		case "go":
			cfg.SDKName, cfg.Factory = "github.com/MichaelKinsy/PiG/extensions/sdk", "Extension"
			cfg.ModulePath = "example.com/" + cfg.Name
			cfg.Package = cfg.ModulePath
		case "python":
			cfg.SDKName = "pig-sdk-py"
			cfg.Package = strings.ReplaceAll(cfg.Name, "-", "_")
		case "rust":
			cfg.SDKName = "pig-sdk"
		}
	}
	return configs, nil
}

// Reuse artifacts through the production factory builder, never live extension state.
func makePackedUIHarness(t *testing.T, language string) *harness {
	t.Helper()
	configs, err := packedUIConfigs[language]()
	if err != nil {
		t.Fatal(err)
	}
	configs = slices.Clone(configs)
	notify, status, actions := &[]string{}, &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetActions(conformanceActions(actions))
	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	loaded, err := host.Reload(t.Context())
	if err != nil || len(loaded) != len(configs) {
		host.Shutdown("load failed")
		t.Fatalf("packed %s: %v (%+v)", language, err, host.LastReloadReport())
	}
	report := host.LastReloadReport()
	if report == nil || len(report.Cells) != 1 || len(report.Cells[0].Extensions) != len(configs) {
		host.Shutdown("not packed")
		t.Fatalf("not a shared cell: %+v", report)
	}
	runner := inproc.NewRunner(loaded, t.TempDir())
	bridge.SetUIPromptScope(runner)
	return &harness{runner: runner, host: host, notify: notify, status: status, actions: actions, ui: ui, bridge: bridge}
}
