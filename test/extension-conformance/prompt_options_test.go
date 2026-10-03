package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi runner.ts:1411-1462 hands every before_agent_start handler one options object and returns it, and agent-session.ts:1669-1683 builds the run's prompt from it. An edit to toolSnippets, toolGuidelines or appendSystemPrompt, not only to sections and selectedTools, therefore reaches the run, also when the handler then fails. The values below are not any SDK's fallback.
func TestPromptOptionEditsAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	t.Run("inproc", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			options := extension.BeforeAgentStartOptions(args[1].(context.Context))
			options.ToolSnippets["read"] = "edited read snippet"
			options.ToolGuidelines["read"] = []string{"edited guideline"}
			options.AppendSystemPrompt += " + edited append"
			if event.Prompt == "error" {
				return nil, errors.New("options failure")
			}
			return nil, nil
		}}}}
		assertPromptOptionEdits(t, ext)
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("option conformance complete") })
		ext := sdk.New("options")
		ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
			options := data["systemPromptOptions"].(map[string]any)
			options["toolSnippets"].(map[string]any)["read"] = "edited read snippet"
			options["toolGuidelines"].(map[string]any)["read"] = []any{"edited guideline"}
			options["appendSystemPrompt"] = options["appendSystemPrompt"].(string) + " + edited append"
			if data["prompt"] == "error" {
				return nil, errors.New("options failure")
			}
			return nil, nil
		})
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "options", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertPromptOptionEdits(t, *loaded)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("option conformance complete") })
					configs := []subprocess.ExtConfig{promptOptionsFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, promptOptionsFactory(t, root, language, "second"))
					} else {
						configs[0].Isolation = "always"
					}
					loaded, failures := host.LoadAll(t.Context(), configs)
					if len(failures) != 0 || len(loaded) != len(configs) {
						t.Fatalf("loaded=%d failures=%v", len(loaded), failures)
					}
					if placement == "packed" {
						host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
						var err error
						loaded, err = host.Reload(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						report := host.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("not packed: %+v", report)
						}
					}
					for _, ext := range loaded {
						assertPromptOptionEdits(t, ext)
					}
				})
			}
		})
	}
}

func assertPromptOptionEdits(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("option conformance complete")
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	for _, prompt := range []string{"ordinary", "error"} {
		base := extension.BuildSystemPromptOptions{
			ToolSnippets:       map[string]string{"read": "base read snippet"},
			ToolGuidelines:     map[string][]string{"read": {"base guideline"}},
			AppendSystemPrompt: "base append",
		}
		result, err := runner.EmitBeforeAgentStart(t.Context(), prompt, nil, "base", base)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.SystemPromptOptions == nil {
			t.Fatalf("%s prompt=%s: edits to the options did not reach the result: %+v", ext.Name, prompt, result)
		}
		got := result.SystemPromptOptions
		if got.ToolSnippets["read"] != "edited read snippet" || !reflect.DeepEqual(got.ToolGuidelines["read"], []string{"edited guideline"}) || got.AppendSystemPrompt != "base append + edited append" {
			t.Fatalf("%s prompt=%s options=%+v", ext.Name, prompt, got)
		}
		if base.ToolSnippets["read"] != "base read snippet" || base.AppendSystemPrompt != "base append" {
			t.Fatalf("base options mutated: %+v", base)
		}
	}
	if !reflect.DeepEqual(reported, []string{"options failure"}) {
		t.Fatalf("errors=%q", reported)
	}
}

func promptOptionsFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := `export default function(pi) { pi.on("before_agent_start", async event => { const o = event.systemPromptOptions; o.toolSnippets.read = "edited read snippet"; o.toolGuidelines.read = ["edited guideline"]; o.appendSystemPrompt += " + edited append"; if (event.prompt === "error") throw new Error("options failure"); }); }`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "prompt-options-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package sections
import ("errors"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(_ sdk.Context,data map[string]any)(any,error){
  o:=data["systemPromptOptions"].(map[string]any)
  o["toolSnippets"].(map[string]any)["read"]="edited read snippet"
  o["toolGuidelines"].(map[string]any)["read"]=[]any{"edited guideline"}
  o["appendSystemPrompt"]=o["appendSystemPrompt"].(string)+" + edited append"
  if data["prompt"]=="error" {return nil,errors.New("options failure")}
  return nil,nil
 })
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, name+".py")
		source = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 def handler(ctx,data):
  o=data["systemPromptOptions"]
  o["toolSnippets"]["read"]="edited read snippet"
  o["toolGuidelines"]["read"]=["edited guideline"]
  o["appendSystemPrompt"]+=" + edited append"
  if data["prompt"]=="error": raise RuntimeError("options failure")
 e.on_event("before_agent_start",handler)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_event_result("before_agent_start",false,|_,data| {
  let appended=format!("{} + edited append",data["systemPromptOptions"]["appendSystemPrompt"].as_str().unwrap_or(""));
  data["systemPromptOptions"]["toolSnippets"]["read"]=Schema::String("edited read snippet".into());
  data["systemPromptOptions"]["toolGuidelines"]["read"]=Schema::Array(vec![Schema::String("edited guideline".into())]);
  data["systemPromptOptions"]["appendSystemPrompt"]=Schema::String(appended);
  if data["prompt"]=="error" {return Err("options failure".into())}
  Ok(None)
 });
 e
}`, name)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
