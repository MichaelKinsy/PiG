package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ctxReadEdit is the append text the handlers write into the emission's shared options before they read the prompt.
const ctxReadEdit = "CTX-READ-EDIT"

// Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt: ctx.getSystemPrompt() in a before_agent_start handler renders the emission's shared options when it
// is read. Each SDK's one handler edits systemPromptOptions and then reads the prompt through the context alone, with no event read in between, and returns what
// it read as a message. An SDK whose context cannot see the handler's own edit answers with the prompt rendered before it, and fails the row.
func TestContextGetSystemPromptSeesTheHandlersOwnEditAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	t.Run("host-process-go", func(t *testing.T) {
		ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
			pi.OnBeforeAgentStart(func(ctx context.Context, _ extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
				before, err := extension.FromContext(ctx).GetSystemPrompt()
				if err != nil {
					return extension.BeforeAgentStartEventResult{}, err
				}
				extension.BeforeAgentStartOptions(ctx).AppendSystemPrompt = ctxReadEdit
				after, err := extension.FromContext(ctx).GetSystemPrompt()
				return extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "seen", Content: "before=" + before + "\nafter=" + after}}, err
			})
			return nil
		}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<ctx-read>")
		if err != nil {
			t.Fatal(err)
		}
		ext.Name = "ctx-read"
		assertContextReadSeesEdit(t, ext)
	})
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("ctx read conformance complete") })
		ext := sdk.New("ctx-read")
		ext.OnEvent("before_agent_start", func(ctx sdk.Context, data map[string]any) (any, error) {
			before, err := ctx.GetSystemPrompt()
			if err != nil {
				return nil, err
			}
			data["systemPromptOptions"].(map[string]any)["appendSystemPrompt"] = ctxReadEdit
			after, err := ctx.GetSystemPrompt()
			return map[string]any{"message": map[string]any{"customType": "seen", "content": "before=" + before + "\nafter=" + after, "display": false}}, err
		})
		loaded, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "ctx-read", Enabled: true}, ext.RunWithConn)
		if err != nil {
			t.Fatal(err)
		}
		assertContextReadSeesEdit(t, *loaded)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("ctx read conformance complete") })
					configs := []subprocess.ExtConfig{ctxReadFactory(t, root, language, "first")}
					if placement == "packed" {
						configs = append(configs, ctxReadFactory(t, root, language, "second"))
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
						assertContextReadSeesEdit(t, ext)
					}
				})
			}
		})
	}
}

// assertContextReadSeesEdit emits before_agent_start twice, so the second emission starts from the base options again, and compares the handler's record with the
// prompts Pi's builder renders for the base and the edited options.
func assertContextReadSeesEdit(t *testing.T, ext extension.Extension) {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	defer runner.Invalidate("ctx read conformance complete")
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
	edited := base
	edited.AppendSystemPrompt = ctxReadEdit
	basePrompt, editedPrompt := render(base), render(edited)
	if basePrompt == editedPrompt {
		t.Fatalf("the edit does not change the rendered prompt: %q", editedPrompt)
	}
	want := "before=" + basePrompt + "\nafter=" + editedPrompt
	for emission := range 2 {
		result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("hi", nil), base)
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || len(result.Messages) != 1 {
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

func ctxReadFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := `export default function(pi) {
  pi.on("before_agent_start", (event, ctx) => {
    const before = ctx.getSystemPrompt();
    event.systemPromptOptions.appendSystemPrompt = "` + ctxReadEdit + `";
    const after = ctx.getSystemPrompt();
    return { message: { customType: "seen", content: "before=" + before + "\nafter=" + after, display: false } };
  });
}`
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "ctx-system-prompt-read-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package ctxread
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(ctx sdk.Context,data map[string]any)(any,error){
  before,err:=ctx.GetSystemPrompt()
  if err!=nil {return nil,err}
  data["systemPromptOptions"].(map[string]any)["appendSystemPrompt"]=%q
  after,err:=ctx.GetSystemPrompt()
  return map[string]any{"message":map[string]any{"customType":"seen","content":"before="+before+"\nafter="+after,"display":false}},err
 })
 return e
}`, name, ctxReadEdit)
	case "python":
		path = filepath.Join(cfg.Source, name+".py")
		source = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 def handler(ctx,data):
  before=ctx.get_system_prompt()
  data["systemPromptOptions"]["appendSystemPrompt"]=%q
  after=ctx.get_system_prompt()
  return {"message":{"customType":"seen","content":"before="+before+"\nafter="+after,"display":False}}
 e.on_event("before_agent_start",handler)
 return e
`, name, ctxReadEdit)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_before_agent_start(|ctx,event| {
  let before=ctx.get_system_prompt().map_err(|e| e.to_string())?;
  event.edit_system_prompt_options(|options| options["appendSystemPrompt"]=Schema::String(%q.into()));
  let after=ctx.get_system_prompt().map_err(|e| e.to_string())?;
  let mut message=Schema::Object(Default::default());
  message["customType"]=Schema::String("seen".into());
  message["content"]=Schema::String(format!("before={}\nafter={}",before,after));
  message["display"]=Schema::Bool(false);
  let mut result=Schema::Object(Default::default());
  result["message"]=message;
  Ok(Some(result))
 });
 e
}`, name, ctxReadEdit)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
