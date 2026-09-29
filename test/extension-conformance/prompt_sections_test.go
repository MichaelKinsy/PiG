package extensionconformance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi runner.ts:1312-1362 awaits before_agent_start and retains section mutations even without a result or after a handler error. This row binds a value that no SDK can satisfy by returning its empty fallback.
func TestPromptSectionMutationsAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	t.Run("inproc", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			*event.SystemPromptOptions.Sections = append(*event.SystemPromptOptions.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
			if event.Prompt == "error" {
				return nil, errors.New("section failure")
			}
			if event.Prompt == "result" {
				return &extension.BeforeAgentStartEventResult{SystemPrompt: new("Exact prompt.")}, nil
			}
			return nil, nil
		}}}}
		assertPromptSections(t, ext)
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("section conformance complete") })
		ext := sdk.New("sections")
		ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
			data["systemPromptOptions"].(map[string]any)["sections"].(*sdk.SystemPromptSections).Set("plan_mode", "Plan only.")
			if data["prompt"] == "error" {
				return nil, errors.New("section failure")
			}
			if data["prompt"] == "result" {
				return map[string]any{"systemPrompt": "Exact prompt."}, nil
			}
			return nil, nil
		})
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "sections", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertPromptSections(t, *loaded)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("section conformance complete") })
					configs := []subprocess.ExtConfig{promptSectionsFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, promptSectionsFactory(t, root, language, "second"))
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
						assertPromptSections(t, ext)
					}
				})
			}
		})
	}
}

func assertPromptSections(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("section conformance complete")
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	for _, prompt := range []string{"ordinary", "error", "result"} {
		sections := ai.OrderedSections{{Name: "z_base", Value: new("retained")}}
		result, err := runner.EmitBeforeAgentStart(t.Context(), prompt, nil, "base", extension.BuildSystemPromptOptions{Sections: &sections})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.SystemPromptOptions == nil || result.SystemPromptOptions.Sections == nil {
			t.Fatalf("missing section result: %+v", result)
		}
		if prompt == "result" {
			if result.SystemPrompt == nil || *result.SystemPrompt != "Exact prompt." {
				t.Fatalf("lost returned prompt: %+v", result)
			}
		} else if result.SystemPrompt != nil || len(result.Messages) != 0 {
			t.Fatalf("prompt=%s result=%+v", prompt, result)
		}
		want := ai.OrderedSections{{Name: "z_base", Value: new("retained")}, {Name: "plan_mode", Value: new("Plan only.")}}
		if !reflect.DeepEqual(*result.SystemPromptOptions.Sections, want) {
			t.Fatalf("%s prompt=%s sections=%+v want=%+v", ext.Name, prompt, *result.SystemPromptOptions.Sections, want)
		}
		if !reflect.DeepEqual(sections, want[:1]) {
			t.Fatalf("base options mutated: %+v", sections)
		}
	}
	if !reflect.DeepEqual(reported, []string{"section failure"}) {
		t.Fatalf("errors=%q", reported)
	}
}

func promptSectionsFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := `export default function(pi) { pi.on("before_agent_start", async event => { event.systemPromptOptions.sections.plan_mode = "Plan only."; if (event.prompt === "error") throw new Error("section failure"); if (event.prompt === "result") return {systemPrompt: "Exact prompt."}; }); }`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "prompt-sections-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package sections
import ("errors"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(_ sdk.Context,data map[string]any)(any,error){
  data["systemPromptOptions"].(map[string]any)["sections"].(*sdk.SystemPromptSections).Set("plan_mode","Plan only.")
  if data["prompt"]=="error" {return nil,errors.New("section failure")}
  if data["prompt"]=="result" {return map[string]any{"systemPrompt":"Exact prompt."},nil};return nil,nil
 })
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, name+".py")
		source = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 def handler(ctx,data):
  data["systemPromptOptions"]["sections"]["plan_mode"]="Plan only."
  if data["prompt"]=="error": raise RuntimeError("section failure")
  if data["prompt"]=="result": return {"systemPrompt":"Exact prompt."}
 e.on_event("before_agent_start",handler)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_event_result("before_agent_start",false,|_,data| {
  data["systemPromptOptions"]["sections"]["plan_mode"]=Schema::String("Plan only.".into());
  if data["prompt"]=="error" {return Err("section failure".into())}
  if data["prompt"]=="result" {let mut result=Schema::Object(Default::default());result["systemPrompt"]=Schema::String("Exact prompt.".into());return Ok(Some(result))};Ok(None)
 });
 e
}`, name)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
