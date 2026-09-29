package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func makePackedScopeHarness(t *testing.T, language string) *harness {
	t.Helper()
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	configs := []subprocess.ExtConfig{packedScopeFactory(t, root, language, "first", "scoped-models-probe"), packedScopeFactory(t, root, language, "second", "scoped-models-probe-two")}
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	notify, status := []string{}, []string{}
	ui := newRecordingUI(&notify, &status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	h.SetUIBridge(bridge)
	loaded, failures := h.LoadAll(t.Context(), configs)
	if len(failures) != 0 || len(loaded) != len(configs) {
		t.Fatalf("loaded=%d errors=%v", len(loaded), failures)
	}
	h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
	loaded, err := h.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	report := h.LastReloadReport()
	if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
		t.Fatalf("not packed: %+v", report)
	}
	runner := inproc.NewRunner(loaded, ".")
	runner.SetUIContext(ui)
	return &harness{runner: runner, host: h, bridge: bridge, ui: ui, notify: &notify, status: &status}
}

func packedScopeFactory(t *testing.T, root, language, name, command string) subprocess.ExtConfig {
	t.Helper()
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "scope-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import ("encoding/json"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.Command(%q,"",func(ctx sdk.Context,_ string)error{models,err:=ctx.ScopedModels();if err!=nil{return err};data,err:=json.Marshal(models);if err!=nil{return err};ctx.Notify(string(data),"info");return nil})
 return e
}`, name, command)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import json, pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 e.command(%q,"",lambda ctx,args:ctx.notify(json.dumps(ctx.scoped_models()),"info"))
 return e
`, name, command)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,CommandResult,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.command(%q,"",|ctx,_|{match ctx.scoped_models(){Ok(models)=>{ctx.notify(&Schema::Array(models).to_string(),"info");CommandResult::Ok},Err(error)=>CommandResult::Error(error.to_string())}});
 e
}`, name, command)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
