package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestPackedToolSchemaRejectionAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			b := subprocess.NewUIBridge(func() {})
			b.SetUIContext(newRecordingUI(new([]string), new([]string)))
			messages := make(chan string, 1)
			b.SetNotifyFunc(func(message, _ string) { messages <- message })
			if !b.Snapshot(nil, 0, false).HasUI {
				t.Fatal("schema notification probe requires a bound UI, not only a notification observer")
			}
			h.SetUIBridge(b)
			configs := []subprocess.ExtConfig{packedSchemaFactory(t, root, language, "first"), packedSchemaFactory(t, root, language, "second")}
			loaded, failures := h.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("loaded=%d errors=%v", len(loaded), failures)
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
						if _, exists := ext.Tools["schema-invalid"]; exists {
							t.Fatal("invalid schema survived registration")
						}
						if got := string(ext.Tools["schema-valid"].Definition.Parameters); got != `{}` {
							t.Fatalf("schema=%s; want empty object", got)
						}
						if err := ext.Commands["schema-probe"].Handler(t.Context(), ""); err != nil {
							t.Fatal(err)
						}
						select {
						case got := <-messages:
							if want := ext.Name + ":true"; got != want {
								t.Fatalf("probe=%q; want %q", got, want)
							}
						case <-t.Context().Done():
							t.Fatal(t.Context().Err())
						}
					}
				})
			}
		})
	}
}

func packedSchemaFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "tool-schema-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import ("fmt"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {
 e:=sdk.New(%q)
 rejected:=false
 func(){defer func(){rejected=recover()!=nil}();e.Tool("schema-invalid","",nil,func(sdk.Context,map[string]any)(any,error){return nil,nil})}()
 e.Tool("schema-valid","",sdk.Schema{},func(sdk.Context,map[string]any)(any,error){return map[string]any{"content":"ok"},nil})
 e.Command("schema-probe","",func(ctx sdk.Context,_ string)error{ctx.Notify(fmt.Sprintf("%%s:%%t",e.Name(),rejected),"info");return nil})
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import pig_sdk
def new_extension():
 e = pig_sdk.Extension(%q)
 rejected = False
 try:
  e.tool("schema-invalid", "", None, lambda ctx,args: {"content":"bad"})
 except ValueError:
  rejected = True
 e.tool("schema-valid", "", {}, lambda ctx,args: {"content":"ok"})
 e.command("schema-probe", "", lambda ctx,args: ctx.notify(e.name+":"+str(rejected).lower(), "info"))
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,Schema,ToolResult,CommandResult};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 let rejected=std::panic::catch_unwind(std::panic::AssertUnwindSafe(||e.tool("schema-invalid","",Schema::Null,|_,_|ToolResult::text("bad")))).is_err();
 e.tool("schema-valid","",Schema::Object(Default::default()),|_,_|ToolResult::text("ok"));
 e.command("schema-probe","",move|ctx,_|{ctx.notify(&format!("{}:{}",%q,rejected),"info");CommandResult::Ok});
 e
}`, name, name)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
