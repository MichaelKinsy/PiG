package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestPackedUserBashUndefinedAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			configs := []subprocess.ExtConfig{packedUserBashFactory(t, root, language, "first"), packedUserBashFactory(t, root, language, "second")}
			loaded, errs := h.LoadAll(t.Context(), configs)
			if len(errs) > 0 || len(loaded) != len(configs) {
				t.Fatalf("loaded=%d errors=%v", len(loaded), errs)
			}
			h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			for _, phase := range []string{"startup", "reload"} {
				t.Run(phase, func(t *testing.T) {
					if phase == "reload" {
						var err error
						loaded, err = h.Reload(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						report := h.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("not packed: %+v", report)
						}
					}
					for _, ext := range loaded {
						runner := inproc.NewRunner([]extension.Extension{ext}, ".")
						result, err := runner.EmitUserBash(t.Context(), extension.UserBashEvent{Type: "user_bash", Command: "undefined", Cwd: "."})
						if err != nil || result == nil {
							t.Fatalf("%s: result=%+v error=%v", ext.Name, result, err)
						}
						record, ok := result.Result.(map[string]any)
						if !ok || record["output"] != ext.Name {
							t.Fatalf("%s: record=%v", ext.Name, record)
						}
						exit, present := record["exitCode"]
						if !present || exit != nil {
							t.Fatalf("%s: exit=%v present=%v", ext.Name, exit, present)
						}
					}
				})
			}
		})
	}
}

func packedUserBashFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := subprocess.ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: language, Isolation: "shared-ok", EntrypointKind: "factory", ContentHash: "user-bash-" + name}
	switch language {
	case "go":
		cfg.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		cfg.ModulePath = "example.test/" + name
		cfg.Package = cfg.ModulePath
		cfg.Factory = "Extension"
		write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath))
		write(filepath.Join(dir, "extension.go"), fmt.Sprintf(`package bash
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension{
 e:=sdk.New(%q)
 e.OnEvent("user_bash",func(sdk.Context,map[string]any)(any,error){return map[string]any{"result":map[string]any{"output":%q,"exitCode":nil,"cancelled":false,"truncated":false}},nil})
 return e
}`, name, name))
	case "python":
		cfg.SDKName = "pig-sdk-py"
		cfg.Package = strings.ReplaceAll(name, "-", "_")
		cfg.Factory = "new_extension"
		write(filepath.Join(dir, cfg.Package+".py"), fmt.Sprintf(`import pig_sdk
def new_extension():
    e=pig_sdk.Extension(%q)
    e.on_event("user_bash",lambda ctx,data: {"result":{"output":%q,"exitCode":None,"cancelled":False,"truncated":False}})
    return e
`, name, name))
	case "rust":
		cfg.SDKName = "pig-sdk"
		cfg.Package = name
		cfg.Factory = "new_extension"
		write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\nserde_json=\"1\"\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write(filepath.Join(dir, "src", "lib.rs"), fmt.Sprintf(`use pig_sdk::Extension;
pub fn new_extension()->Extension{
 let mut e=Extension::new(%q);
 e.on_event("user_bash",false,|_,_|Some(serde_json::json!({"result":{"output":%q,"exitCode":null,"cancelled":false,"truncated":false}})));
 e
}`, name, name))
	}
	return cfg
}
