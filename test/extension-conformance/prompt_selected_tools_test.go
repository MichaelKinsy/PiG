package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi runner.ts:1317-1363 awaits each before_agent_start handler and hands every handler one normalized options object, so a handler's selectedTools edit is visible to the next handler and survives a handler error; agent-session.ts:1702-1714 then makes an edited list the run's tool loadout. Each handler appends or assigns its own extension name, a value no SDK fallback can produce. Subprocess handlers wait before editing, so a runtime that answers before its handler finishes loses the edit.
func TestPromptSelectedToolsMutationsAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	t.Run("inproc", func(t *testing.T) {
		ext := extension.Extension{Name: "first", Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			options := extension.BeforeAgentStartOptions(args[1].(context.Context))
			if event.Prompt == "repair" {
				raw := json.RawMessage(`null`)
				if string(extension.BeforeAgentStartSelectedTools(args[1].(context.Context))) == "null" {
					raw = json.RawMessage(`["first"]`)
				}
				extension.SetBeforeAgentStartSelectedTools(args[1].(context.Context), raw)
				return nil, nil
			}
			if event.Prompt == "null" || event.Prompt == "mixed" {
				raw := json.RawMessage(`null`)
				if event.Prompt == "mixed" {
					raw = json.RawMessage(`[null,17,"first"]`)
				}
				extension.SetBeforeAgentStartSelectedTools(args[1].(context.Context), raw)
				return nil, nil
			}
			if event.Prompt == "replace" {
				options.SelectedTools = []string{"first"}
			} else {
				options.SelectedTools = append(options.SelectedTools, "first")
			}
			if event.Prompt == "error" {
				return nil, errors.New("selected tools failure")
			}
			if event.Prompt == "result" {
				return &extension.BeforeAgentStartEventResult{SystemPrompt: new("Exact prompt.")}, nil
			}
			return nil, nil
		}}}}
		assertPromptSelectedTools(t, []extension.Extension{ext}, []string{"first"})
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("selected tools conformance complete") })
		ext := sdk.New("first")
		ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
			options := data["systemPromptOptions"].(map[string]any)
			if data["prompt"] == "repair" {
				if options["selectedTools"] == nil {
					options["selectedTools"] = []any{"first"}
				} else {
					options["selectedTools"] = nil
				}
				return nil, nil
			}
			if data["prompt"] == "null" {
				options["selectedTools"] = nil
				return nil, nil
			}
			if data["prompt"] == "mixed" {
				options["selectedTools"] = []any{nil, 17, "first"}
				return nil, nil
			}
			if data["prompt"] == "replace" {
				options["selectedTools"] = []any{"first"}
			} else {
				options["selectedTools"] = append(options["selectedTools"].([]any), "first")
			}
			if data["prompt"] == "error" {
				return nil, errors.New("selected tools failure")
			}
			if data["prompt"] == "result" {
				return map[string]any{"systemPrompt": "Exact prompt."}, nil
			}
			return nil, nil
		})
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "first", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertPromptSelectedTools(t, []extension.Extension{*loaded}, []string{"first"})
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("selected tools conformance complete") })
					configs := []subprocess.ExtConfig{promptSelectedToolsFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, promptSelectedToolsFactory(t, root, language, "second"))
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
					names := make([]string, len(loaded))
					for i, ext := range loaded {
						names[i] = ext.Name
					}
					if sorted := slices.Sorted(slices.Values(names)); !slices.Equal(sorted, []string{"first", "second"}[:len(configs)]) {
						t.Fatalf("loaded=%v", names)
					}
					// One runner dispatches every loaded extension in load order, so a later handler must observe an earlier handler's edit.
					assertPromptSelectedTools(t, loaded, names)
				})
			}
		})
	}
}

func assertPromptSelectedTools(t *testing.T, exts []extension.Extension, names []string) {
	t.Helper()
	runner := inproc.NewRunner(exts, t.TempDir())
	defer runner.Invalidate("selected tools conformance complete")
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	for _, tc := range []struct {
		prompt string
		base   []string
		want   []string
	}{
		{"ordinary", []string{"read", "bash"}, append([]string{"read", "bash"}, names...)},
		// A nil selection is Pi's absent selectedTools, which normalizeBuildSystemPromptOptions (system-prompt.ts:58) defaults to read, bash, edit and write before the first handler; a non-nil empty slice is Pi's [].
		{"absent", nil, append([]string{"read", "bash", "edit", "write"}, names...)},
		{"empty", []string{}, names},
		{"error", []string{"read", "bash"}, append([]string{"read", "bash"}, names...)},
		{"result", []string{"read", "bash"}, append([]string{"read", "bash"}, names...)},
		{"replace", []string{"read", "bash"}, names[len(names)-1:]},
		{"mixed", []string{"read", "bash"}, names[len(names)-1:]},
		{"null", []string{"read", "bash"}, nil},
		// Each handler repairs a null selection and nulls any other, so only a later handler that observes the earlier handler's untyped null can repair it (Pi runner.ts:1339 shares the object).
		{"repair", []string{"read", "bash"}, names[len(names)-1:]},
	} {
		base := slices.Clone(tc.base)
		result, err := runner.EmitBeforeAgentStart(t.Context(), tc.prompt, nil, extension.BuildSystemPromptOptions{SelectedTools: base})
		if tc.prompt == "null" || tc.prompt == "repair" && len(names) == 1 {
			if err == nil || err.Error() != "Cannot read properties of null (reading 'length')" {
				t.Fatalf("null selectedTools: result=%+v err=%v reported=%v", result, err, reported)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.SystemPromptOptions == nil {
			t.Fatalf("prompt=%s missing selected tools result: %+v errors=%q", tc.prompt, result, reported)
		}
		if got := result.SystemPromptOptions.SelectedTools; !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("prompt=%s selectedTools=%q want=%q", tc.prompt, got, tc.want)
		}
		if tc.prompt == "result" {
			if result.SystemPrompt == nil || *result.SystemPrompt != "Exact prompt." {
				t.Fatalf("lost returned prompt: %+v", result)
			}
		} else if result.SystemPrompt != nil || len(result.Messages) != 0 {
			t.Fatalf("prompt=%s result=%+v", tc.prompt, result)
		}
		if !slices.Equal(base, tc.base) {
			t.Fatalf("prompt=%s base options mutated: %q", tc.prompt, base)
		}
	}
	want := make([]string, len(names))
	for i := range want {
		want[i] = "selected tools failure"
	}
	if !reflect.DeepEqual(reported, want) {
		t.Fatalf("errors=%q want=%q", reported, want)
	}
}

func promptSelectedToolsFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := fmt.Sprintf(`export default function(pi) { pi.on("before_agent_start", async event => { const options = event.systemPromptOptions; await new Promise(resolve => setTimeout(resolve, 20)); if (event.prompt === "repair") { options.selectedTools = options.selectedTools === null ? [%[1]q] : null; return; } if (event.prompt === "null") { options.selectedTools = null; return; } if (event.prompt === "mixed") { options.selectedTools = [null,17,%[1]q]; return; } if (event.prompt === "replace") options.selectedTools = [%[1]q]; else options.selectedTools.push(%[1]q); if (event.prompt === "error") throw new Error("selected tools failure"); if (event.prompt === "result") return {systemPrompt: "Exact prompt."}; }); }`, name)
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "prompt-selected-tools-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package selected
import ("errors"; "time"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%[1]q)
 e.OnEvent("before_agent_start",func(_ sdk.Context,data map[string]any)(any,error){
  options:=data["systemPromptOptions"].(map[string]any)
  time.Sleep(20*time.Millisecond)
  if data["prompt"]=="repair" {if options["selectedTools"]==nil {options["selectedTools"]=[]any{%[1]q}} else {options["selectedTools"]=nil};return nil,nil}
  if data["prompt"]=="null" {options["selectedTools"]=nil;return nil,nil}
  if data["prompt"]=="mixed" {options["selectedTools"]=[]any{nil,17,%[1]q};return nil,nil}
  if data["prompt"]=="replace" {options["selectedTools"]=[]any{%[1]q}} else {options["selectedTools"]=append(options["selectedTools"].([]any),%[1]q)}
  if data["prompt"]=="error" {return nil,errors.New("selected tools failure")}
  if data["prompt"]=="result" {return map[string]any{"systemPrompt":"Exact prompt."},nil};return nil,nil
 })
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, name+".py")
		source = fmt.Sprintf(`import time
import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%[1]q)
 def handler(ctx,data):
  options=data["systemPromptOptions"]
  time.sleep(0.02)
  if data["prompt"]=="repair":
   options["selectedTools"]=[%[1]q] if options["selectedTools"] is None else None
   return
  if data["prompt"]=="null":
   options["selectedTools"]=None
   return
  if data["prompt"]=="mixed":
   options["selectedTools"]=[None,17,%[1]q]
   return
  if data["prompt"]=="replace": options["selectedTools"]=[%[1]q]
  else: options["selectedTools"].append(%[1]q)
  if data["prompt"]=="error": raise RuntimeError("selected tools failure")
  if data["prompt"]=="result": return {"systemPrompt":"Exact prompt."}
 e.on_event("before_agent_start",handler)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%[1]q);
 e.on_event_result("before_agent_start",false,|_,data| {
  std::thread::sleep(std::time::Duration::from_millis(20));
  if data["prompt"]=="repair" {data["systemPromptOptions"]["selectedTools"]=if data["systemPromptOptions"]["selectedTools"].is_null() {Schema::Array(vec![Schema::String(%[1]q.into())])} else {Schema::Null};return Ok(None)}
  if data["prompt"]=="null" {data["systemPromptOptions"]["selectedTools"]=Schema::Null;return Ok(None)}
  if data["prompt"]=="mixed" {data["systemPromptOptions"]["selectedTools"]=Schema::Array(vec![Schema::Null,Schema::Number(17.into()),Schema::String(%[1]q.into())]);return Ok(None)}
  if data["prompt"]=="replace" {data["systemPromptOptions"]["selectedTools"]=Schema::Array(vec![Schema::String(%[1]q.into())])} else {data["systemPromptOptions"]["selectedTools"].as_array_mut().ok_or("selectedTools is not a list")?.push(Schema::String(%[1]q.into()))}
  if data["prompt"]=="error" {return Err("selected tools failure".into())}
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
