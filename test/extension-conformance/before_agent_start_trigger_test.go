package extensionconformance

import (
	"context"
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

// Pi 1.2.0 types.ts:932-957 (Pi PR #10739): before_agent_start carries messageType "user" with prompt and images, or "custom" with the
// starting message and no prompt; runner.ts:1427 emitBeforeAgentStart spreads its trigger into each handler's event.
// Each handler reports what it read by appending it to appendSystemPrompt, so a handler that never reads the members cannot pass: the values come from the trigger.
const (
	triggerUserSuffix   = "|user|prompt-7c1"
	triggerCustomSuffix = "|custom|wake-9a|WAKE-BODY|prompt-absent"
)

func TestBeforeAgentStartTriggerAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))
	t.Run("inproc", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeAgentStartEvent)
			options := extension.BeforeAgentStartOptions(args[1].(context.Context))
			if event.MessageType == extension.BeforeAgentStartCustom {
				options.AppendSystemPrompt += "|custom|" + event.Message.CustomType + "|" + event.Message.Content.(string) + "|prompt-absent"
			} else {
				options.AppendSystemPrompt += "|" + string(event.MessageType) + "|" + event.Prompt
			}
			return nil, nil
		}}}}
		assertBeforeAgentStartTrigger(t, ext)
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("trigger conformance complete") })
		ext := sdk.New("trigger")
		ext.OnEvent("before_agent_start", triggerGoHandler)
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "trigger", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertBeforeAgentStartTrigger(t, *loaded)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("trigger conformance complete") })
					configs := []subprocess.ExtConfig{triggerFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, triggerFactory(t, root, language, "second"))
					} else {
						configs[0].Isolation = "always"
					}
					loaded, failures := host.LoadAll(t.Context(), configs)
					if len(failures) != 0 || len(loaded) != len(configs) {
						t.Fatalf("loaded=%d failures=%v", len(loaded), failures)
					}
					for _, ext := range loaded {
						assertBeforeAgentStartTrigger(t, ext)
					}
				})
			}
		})
	}
}

func triggerGoHandler(_ sdk.Context, data map[string]any) (any, error) {
	options := data["systemPromptOptions"].(map[string]any)
	suffix := "|" + data["messageType"].(string)
	if message, ok := data["message"].(map[string]any); ok {
		suffix += "|" + message["customType"].(string) + "|" + message["content"].(string)
		if _, hasPrompt := data["prompt"]; !hasPrompt {
			suffix += "|prompt-absent"
		}
	} else {
		suffix += "|" + data["prompt"].(string)
	}
	options["appendSystemPrompt"] = options["appendSystemPrompt"].(string) + suffix
	return nil, nil
}

func assertBeforeAgentStartTrigger(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("trigger conformance complete")
	for name, tc := range map[string]struct {
		trigger extension.BeforeAgentStartTrigger
		want    string
	}{
		"user":   {extension.UserPromptTrigger("prompt-7c1", nil), "base" + triggerUserSuffix},
		"custom": {extension.CustomMessageTrigger(extension.CustomMessage{CustomType: "wake-9a", Content: "WAKE-BODY", Display: true}), "base" + triggerCustomSuffix},
	} {
		result, err := runner.EmitBeforeAgentStart(t.Context(), tc.trigger, extension.BuildSystemPromptOptions{AppendSystemPrompt: "base"})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || result.SystemPromptOptions == nil {
			t.Fatalf("%s %s: no options in the result: %+v", ext.Name, name, result)
		}
		got := result.SystemPromptOptions.AppendSystemPrompt
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %s: appendSystemPrompt = %q, want %q", ext.Name, name, got, tc.want)
		}
	}
}

func triggerFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := `export default function(pi) { pi.on("before_agent_start", async event => { const o = event.systemPromptOptions; if (event.messageType === "custom") { o.appendSystemPrompt += "|custom|" + event.message.customType + "|" + event.message.content + (event.prompt === undefined ? "|prompt-absent" : "|prompt-present"); } else { o.appendSystemPrompt += "|" + event.messageType + "|" + event.prompt; } }); }`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "trigger-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package trigger
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(_ sdk.Context,data map[string]any)(any,error){
  o:=data["systemPromptOptions"].(map[string]any)
  suffix:="|"+data["messageType"].(string)
  if m,ok:=data["message"].(map[string]any);ok {
   suffix+="|"+m["customType"].(string)+"|"+m["content"].(string)
   if _,has:=data["prompt"];!has {suffix+="|prompt-absent"}
  } else { suffix+="|"+data["prompt"].(string) }
  o["appendSystemPrompt"]=o["appendSystemPrompt"].(string)+suffix
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
  suffix="|"+data["messageType"]
  if "message" in data:
   suffix+="|"+data["message"]["customType"]+"|"+data["message"]["content"]
   if "prompt" not in data: suffix+="|prompt-absent"
  else:
   suffix+="|"+data["prompt"]
  o["appendSystemPrompt"]+=suffix
 e.on_event("before_agent_start",handler)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_before_agent_start(|_,event| {
  let data=event.payload();
  let mut suffix=format!("|{}",data["messageType"].as_str().unwrap_or("?"));
  if data["message"]["customType"].as_str().is_some() {
   suffix+=&format!("|{}|{}",data["message"]["customType"].as_str().unwrap_or("?"),data["message"]["content"].as_str().unwrap_or("?"));
   if data["prompt"].as_str().is_none() { suffix+="|prompt-absent"; }
  } else { suffix+=&format!("|{}",data["prompt"].as_str().unwrap_or("?")); }
  event.edit_system_prompt_options(|options| {
   let appended=format!("{}{}",options["appendSystemPrompt"].as_str().unwrap_or(""),suffix);
   options["appendSystemPrompt"]=Schema::String(appended);
  });
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
