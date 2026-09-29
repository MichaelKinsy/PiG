package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestPackedFlagValuesAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			// These factories register only local flags and commands; each language owns its Host and config root.
			t.Parallel()
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			b := subprocess.NewUIBridge(func() {})
			b.SetUIContext(newRecordingUI(new([]string), new([]string)))
			messages := make(chan string, 1)
			b.SetNotifyFunc(func(message, _ string) { messages <- message })
			if !b.Snapshot(nil, 0, false).HasUI {
				t.Fatal("flag notification probe requires a bound UI, not only a notification observer")
			}
			h.SetUIBridge(b)
			configs := []subprocess.ExtConfig{
				packedFlagFactory(t, root, language, "z-first", true),
				packedFlagFactory(t, root, language, "a-second", false),
			}
			loaded, errs := h.LoadAll(t.Context(), configs)
			if len(errs) > 0 || len(loaded) != len(configs) {
				t.Fatalf("loaded=%d errors=%v", len(loaded), errs)
			}
			h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			var err error
			loaded, err = h.Reload(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			report := h.LastReloadReport()
			if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
				t.Fatalf("not one packed cell: %#v", report)
			}
			for _, override := range []any{nil, false, nil} {
				b.SetHostAction("getFlag", func(_, _ string) any { return override })
				for _, ext := range loaded {
					if err := ext.Commands["flags"].Handler(context.Background(), ""); err != nil {
						t.Fatal(err)
					}
					want := "true"
					if override != nil {
						want = "false"
					}
					select {
					case got := <-messages:
						if got != want {
							t.Fatalf("%s: shared default/override=%s, want %s", ext.Name, got, want)
						}
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					}
				}
			}
		})
	}
}

func packedFlagFactory(t *testing.T, root, language, name string, value bool) subprocess.ExtConfig {
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
	cfg := subprocess.ExtConfig{Name: name, Source: dir, Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: language, Isolation: "shared-ok", EntrypointKind: "factory", ContentHash: fmt.Sprintf("flags-%s-%t", name, value)}
	switch language {
	case "go":
		cfg.SDKName = "github.com/MichaelKinsy/PiG/extensions/sdk"
		cfg.ModulePath = "example.test/" + name
		cfg.Package = cfg.ModulePath
		cfg.Factory = "Extension"
		write(filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", cfg.ModulePath))
		write(filepath.Join(dir, "extension.go"), fmt.Sprintf(`package flags
import ("fmt"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {
 e:=sdk.New(%q)
 e.Flag("shared",sdk.FlagOptions{Type:sdk.FlagBoolean,Default:%t})
 e.Command("flags","",func(ctx sdk.Context,_ string)error{value,err:=ctx.GetFlag("shared");if err!=nil{return err};ctx.Notify(fmt.Sprint(value),"info");return nil})
 return e
}`, name, value))
	case "python":
		cfg.SDKName = "pig-sdk-py"
		cfg.Package = strings.ReplaceAll(name, "-", "_")
		cfg.Factory = "new_extension"
		pyValue := "False"
		if value {
			pyValue = "True"
		}
		write(filepath.Join(dir, cfg.Package+".py"), fmt.Sprintf(`import pig_sdk
def new_extension():
    e = pig_sdk.Extension(%q)
    e.flag("shared", flag_type="boolean", default=%s)
    e.command("flags", "", lambda ctx, args: ctx.notify(str(ctx.get_flag("shared")).lower(), "info"))
    return e
`, name, pyValue))
	case "rust":
		cfg.SDKName = "pig-sdk"
		cfg.Package = name
		cfg.Factory = "new_extension"
		write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write(filepath.Join(dir, "src", "lib.rs"), fmt.Sprintf(`use pig_sdk::{Extension,FlagOptions,CommandResult};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.flag("shared",FlagOptions::boolean("",%t));
 e.command("flags","",|ctx,_args| {ctx.notify(&ctx.get_flag("shared").unwrap().unwrap().to_string(),"info");CommandResult::Ok});
 e
}`, name, value))
	}
	return cfg
}
