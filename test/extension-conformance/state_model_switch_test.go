package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's ctx.model is live (runner.ts:906), so a model change that no extension call announced, here one a native in-process extension makes between two handlers of the same emission, is what the next handler reads. The host's replicated state must not hold the model a handler earlier. The recorded ids are catalog models, not any SDK's fallback.
func TestModelChangedWithoutACallReachesTheNextHandlerAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	small := ai.GetBuiltinModel("anthropic", "claude-haiku-4-5")
	large := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
	if small == nil || large == nil || small.ID == large.ID {
		t.Fatalf("catalog models %v %v", small, large)
	}
	recorders := []string{"chain-a", "chain-c"}
	// Node reads ctx.model, which is replicated state. The Go, Python and Rust recorders also read their replicated model accessor (Go ctx.Model(), Python ctx.model and Rust ctx.model() all replicate the id, Pi's ctx.model.id), so a host that pushes a stale model fails in every SDK, not only where the SDK asks the host.
	wants := map[string]string{
		"node":   "base append|chain-a=" + small.ID + "|chain-c=" + large.ID,
		"go":     "base append|chain-a=" + small.ID + "/" + small.ID + "|chain-c=" + large.ID + "/" + large.ID,
		"python": "base append|chain-a=" + small.ID + "/" + small.ID + "|chain-c=" + large.ID + "/" + large.ID,
		"rust":   "base append|chain-a=" + small.ID + "/" + small.ID + "|chain-c=" + large.ID + "/" + large.ID,
	}

	for _, language := range []string{"go", "python", "rust", "node"} {
		want := wants[language]
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					current := small
					bridge := subprocess.NewUIBridge(func() {})
					codingagent.WireModelOperations(bridge, codingagent.ModelOperationBindings{CurrentModel: func() *ai.Model { return current }})
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					host.SetUIBridge(bridge)
					t.Cleanup(func() { host.Shutdown("model switch conformance complete") })
					configs := make([]subprocess.ExtConfig, len(recorders))
					for i, name := range recorders {
						configs[i] = modelRecorderFactory(t, root, language, name)
						if placement == "isolated" {
							configs[i].Isolation = "always"
						}
					}
					loaded, failures := host.LoadAll(t.Context(), configs)
					if len(failures) != 0 || len(loaded) != len(configs) {
						t.Fatalf("loaded=%d failures=%v", len(loaded), failures)
					}
					if placement == "packed" {
						host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
						var err error
						if loaded, err = host.Reload(t.Context()); err != nil {
							t.Fatal(err)
						}
						report := host.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("not packed: %+v", report)
						}
					}
					// The native extension sits between the two recorders and switches the Session's model without any call through the host.
					native := extension.Extension{Name: "native-switch", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(...any) (any, error) {
						current = large
						return nil, nil
					}}}}
					exts := []extension.Extension{loaded[0], native, loaded[1]}
					if got := runPromptChain(t, exts); got != want {
						t.Fatalf("append = %q, want %q", got, want)
					}
				})
			}
		})
	}
}

func modelRecorderFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := fmt.Sprintf(`export default function(pi) { pi.on("before_agent_start", async (event, ctx) => { event.systemPromptOptions.appendSystemPrompt += "|%s=" + (ctx.model ? ctx.model.id : "none"); }); }`, name)
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "model-recorder-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package recorder
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(ctx sdk.Context,data map[string]any)(any,error){
  info,err:=ctx.GetModelInfo()
  if err!=nil {return nil,err}
  id:="none"
  if info!=nil {id=info.ID}
  o:=data["systemPromptOptions"].(map[string]any)
  o["appendSystemPrompt"]=o["appendSystemPrompt"].(string)+"|%s="+id+"/"+ctx.Model()
  return nil,nil
 })
 return e
}`, name, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		source = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 def handler(ctx,data):
  info=ctx.get_model_info()
  data["systemPromptOptions"]["appendSystemPrompt"]+="|%s="+(info["id"] if info else "none")+"/"+ctx.model
 e.on_event("before_agent_start",handler)
 return e
`, name, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_before_agent_start(|ctx,event| {
  let id=ctx.get_model_info().map_err(|e| e.to_string())?.map(|m| m.id).unwrap_or_else(|| "none".to_string());
  event.edit_system_prompt_options(|options| {
   let appended=format!("{}|%s={}/{}",options["appendSystemPrompt"].as_str().unwrap_or(""),id,ctx.model());
   options["appendSystemPrompt"]=Schema::String(appended);
  });
  Ok(None)
 });
 e
}`, name, name)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
