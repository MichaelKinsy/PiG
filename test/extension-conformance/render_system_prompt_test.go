package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt: in a before_agent_start handler, event.systemPrompt and
// ctx.getSystemPrompt() render the emission's shared options when read. A handler sees its own edit to
// systemPromptOptions at once, the next handler sees it too, and a returned systemPrompt (forceSystemPrompt) is exactly
// what a later handler reads. Each SDK's handlers record what they read and the last returns the record as a message, so
// an SDK that keeps the prompt rendered before the handler ran, or answers with the Session's prompt, fails the row.
func TestRenderCurrentSystemPromptAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	// A host-process Go extension reads the event's SystemPrompt with Get and the context's with GetSystemPrompt, and
	// edits the shared options through BeforeAgentStartOptions.
	t.Run("host-process-go", func(t *testing.T) {
		var seen []string
		read := func(ctx context.Context, evt extension.BeforeAgentStartEvent) (string, error) {
			event, err := evt.SystemPrompt.Get()
			if err != nil {
				return "", err
			}
			current, err := extension.FromContext(ctx).GetSystemPrompt()
			return "event=" + event + " ctx=" + current, err
		}
		ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
			pi.OnBeforeAgentStart(func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
				seen = nil
				first, err := read(ctx, evt)
				if err != nil {
					return extension.BeforeAgentStartEventResult{}, err
				}
				extension.BeforeAgentStartOptions(ctx).AppendSystemPrompt = "APPENDED-BY-FIRST"
				edited, err := read(ctx, evt)
				seen = append(seen, "first "+first, "first after-edit "+edited)
				return extension.BeforeAgentStartEventResult{}, err
			})
			pi.OnBeforeAgentStart(func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
				second, err := read(ctx, evt)
				seen = append(seen, "second "+second)
				return extension.BeforeAgentStartEventResult{SystemPrompt: new("FORCED-BY-SECOND")}, err
			})
			pi.OnBeforeAgentStart(func(ctx context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
				third, err := read(ctx, evt)
				seen = append(seen, "third "+third)
				return extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "seen", Content: strings.Join(seen, "\n")}}, err
			})
			return nil
		}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<render>")
		if err != nil {
			t.Fatal(err)
		}
		ext.Name = "render"
		assertRenderCurrentSystemPrompt(t, ext)
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("render conformance complete") })
		var seen []string
		read := func(ctx sdk.Context, data map[string]any) (string, error) {
			event, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
			if err != nil {
				return "", err
			}
			current, err := ctx.GetSystemPrompt()
			return "event=" + event + " ctx=" + current, err
		}
		ext := sdk.New("render")
		ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
			seen = nil
			first, err := read(ctx, data)
			if err != nil {
				return nil, err
			}
			data["systemPromptOptions"].(map[string]any)["appendSystemPrompt"] = "APPENDED-BY-FIRST"
			edited, err := read(ctx, data)
			seen = append(seen, "first "+first, "first after-edit "+edited)
			return nil, err
		})
		ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
			second, err := read(ctx, data)
			seen = append(seen, "second "+second)
			return map[string]any{"systemPrompt": "FORCED-BY-SECOND"}, err
		})
		ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
			third, err := read(ctx, data)
			seen = append(seen, "third "+third)
			return map[string]any{"message": map[string]any{"customType": "seen", "content": strings.Join(seen, "\n"), "display": false}}, err
		})
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "render", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertRenderCurrentSystemPrompt(t, *loaded)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("render conformance complete") })
					configs := []subprocess.ExtConfig{renderSystemPromptFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, renderSystemPromptFactory(t, root, language, "second"))
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
						assertRenderCurrentSystemPrompt(t, ext)
					}
				})
			}
		})
	}
}

// assertRenderCurrentSystemPrompt emits before_agent_start twice, so the second emission starts from the base options
// again, and compares the record with the prompts Pi's builder renders for the base and the edited options.
func assertRenderCurrentSystemPrompt(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("render conformance complete")
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	base := extension.BuildSystemPromptOptions{CustomPrompt: "BASE PROMPT", CustomPromptSet: true, Cwd: "/work"}
	render := func(options extension.BuildSystemPromptOptions) string {
		t.Helper()
		prompt, err := inproc.RenderSystemPrompt(extension.NormalizeBuildSystemPromptOptions(options))
		if err != nil {
			t.Fatal(err)
		}
		return prompt
	}
	appended := base
	appended.AppendSystemPrompt = "APPENDED-BY-FIRST"
	basePrompt, appendedPrompt := render(base), render(appended)
	if basePrompt == appendedPrompt || !strings.Contains(appendedPrompt, "APPENDED-BY-FIRST") {
		t.Fatalf("the edit does not change the rendered prompt: %q", appendedPrompt)
	}
	want := strings.Join([]string{
		"first event=" + basePrompt + " ctx=" + basePrompt,
		"first after-edit event=" + appendedPrompt + " ctx=" + appendedPrompt,
		"second event=" + appendedPrompt + " ctx=" + appendedPrompt,
		"third event=FORCED-BY-SECOND ctx=FORCED-BY-SECOND",
	}, "\n")
	for emission := range 2 {
		result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("hi", nil), base)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || len(result.Messages) != 1 || result.SystemPrompt == nil || *result.SystemPrompt != "FORCED-BY-SECOND" {
			t.Fatalf("%s emission %d: result=%+v errors=%q", ext.Name, emission, result, reported)
		}
		if got := result.Messages[0].Content; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s emission %d read %q, want %q; errors=%q", ext.Name, emission, got, want, reported)
		}
	}
	if len(reported) != 0 {
		t.Fatalf("%s errors=%q", ext.Name, reported)
	}
}

func renderSystemPromptFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := `export default function(pi) {
  let seen = [];
  const read = (event, ctx) => "event=" + event.systemPrompt + " ctx=" + ctx.getSystemPrompt();
  pi.on("before_agent_start", (event, ctx) => { seen = []; const first = read(event, ctx); event.systemPromptOptions.appendSystemPrompt = "APPENDED-BY-FIRST"; seen.push("first " + first, "first after-edit " + read(event, ctx)); });
  pi.on("before_agent_start", (event, ctx) => { seen.push("second " + read(event, ctx)); return { systemPrompt: "FORCED-BY-SECOND" }; });
  pi.on("before_agent_start", (event, ctx) => { seen.push("third " + read(event, ctx)); return { message: { customType: "seen", content: seen.join("\n"), display: false } }; });
}`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "render-system-prompt-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package render
import ("strings"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 var seen []string
 read:=func(ctx sdk.Context,data map[string]any)(string,error){
  event,err:=data["systemPrompt"].(*sdk.SystemPrompt).Get()
  if err!=nil {return "",err}
  current,err:=ctx.GetSystemPrompt()
  return "event="+event+" ctx="+current,err
 }
 e.OnEvent("before_agent_start",func(ctx sdk.Context,data map[string]any)(any,error){
  seen=nil
  first,err:=read(ctx,data)
  if err!=nil {return nil,err}
  data["systemPromptOptions"].(map[string]any)["appendSystemPrompt"]="APPENDED-BY-FIRST"
  edited,err:=read(ctx,data)
  seen=append(seen,"first "+first,"first after-edit "+edited)
  return nil,err
 })
 e.OnEvent("before_agent_start",func(ctx sdk.Context,data map[string]any)(any,error){
  second,err:=read(ctx,data)
  seen=append(seen,"second "+second)
  return map[string]any{"systemPrompt":"FORCED-BY-SECOND"},err
 })
 e.OnEvent("before_agent_start",func(ctx sdk.Context,data map[string]any)(any,error){
  third,err:=read(ctx,data)
  seen=append(seen,"third "+third)
  return map[string]any{"message":map[string]any{"customType":"seen","content":strings.Join(seen,"\n"),"display":false}},err
 })
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, name+".py")
		source = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 seen=[]
 def read(ctx,data):
  return "event="+data["systemPrompt"]+" ctx="+ctx.get_system_prompt()
 def first(ctx,data):
  seen.clear()
  before=read(ctx,data)
  data["systemPromptOptions"]["appendSystemPrompt"]="APPENDED-BY-FIRST"
  seen.extend(["first "+before,"first after-edit "+read(ctx,data)])
 def second(ctx,data):
  seen.append("second "+read(ctx,data))
  return {"systemPrompt":"FORCED-BY-SECOND"}
 def third(ctx,data):
  seen.append("third "+read(ctx,data))
  return {"message":{"customType":"seen","content":"\n".join(seen),"display":False}}
 e.on_event("before_agent_start",first)
 e.on_event("before_agent_start",second)
 e.on_event("before_agent_start",third)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		// Rust reads Pi's event.systemPrompt with ctx.current_system_prompt(data): the handler holds data mutably, so ctx
		// cannot read it. ctx.get_system_prompt() then returns the prompt that read rendered.
		source = fmt.Sprintf(`use pig_sdk::{Context,Extension,Schema};
use std::sync::{Arc,Mutex};
fn read(ctx:&Context,data:&Schema)->Result<String,String> {
 let event=ctx.current_system_prompt(data).map_err(|e| e.to_string())?;
 let current=ctx.get_system_prompt().map_err(|e| e.to_string())?;
 Ok(format!("event={} ctx={}",event,current))
}
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 let seen=Arc::new(Mutex::new(Vec::<String>::new()));
 let record=seen.clone();
 e.on_event_result("before_agent_start",move |ctx,data| {
  let first=read(ctx,data)?;
  data["systemPromptOptions"]["appendSystemPrompt"]=Schema::String("APPENDED-BY-FIRST".into());
  let edited=read(ctx,data)?;
  *record.lock().unwrap_or_else(|p| p.into_inner())=vec![format!("first {}",first),format!("first after-edit {}",edited)];
  Ok(None)
 });
 let record=seen.clone();
 e.on_event_result("before_agent_start",move |ctx,data| {
  let second=read(ctx,data)?;
  record.lock().unwrap_or_else(|p| p.into_inner()).push(format!("second {}",second));
  let mut result=Schema::Object(Default::default());
  result["systemPrompt"]=Schema::String("FORCED-BY-SECOND".into());
  Ok(Some(result))
 });
 e.on_event_result("before_agent_start",move |ctx,data| {
  let third=read(ctx,data)?;
  let mut seen=seen.lock().unwrap_or_else(|p| p.into_inner());
  seen.push(format!("third {}",third));
  let mut message=Schema::Object(Default::default());
  message["customType"]=Schema::String("seen".into());
  message["content"]=Schema::String(seen.join("\n"));
  message["display"]=Schema::Bool(false);
  let mut result=Schema::Object(Default::default());
  result["message"]=message;
  Ok(Some(result))
 });
 e
}`, name)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
