package extensionconformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

var promptChainNames = []string{"chain-a", "chain-b", "chain-c", "chain-d"}

// Pi runner.ts:1411-1462 gives every before_agent_start handler the one shared options object and renders event.systemPrompt from it (renderCurrentSystemPrompt, :1426) before each handler, so handler k+1 reads the prompt that includes the edit handler k made. Each extension here appends "|<name>=<length of the prompt it read>" to appendSystemPrompt. The in-process chain is the reference; every SDK in every placement must produce the same string, and the prompt length it reports must grow by exactly the text the previous handler appended. A fallback reader (empty prompt) reports 0.
func TestBeforeAgentStartChainSeesEveryEarlierEditAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(root, "extensions", "sdk-py"))
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(root, "extensions", "sdk-rs"))

	reference := make([]extension.Extension, len(promptChainNames))
	for i, name := range promptChainNames {
		reference[i] = extension.Extension{Name: name, Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
			prompt, err := args[0].(extension.BeforeAgentStartEvent).SystemPrompt.Get()
			if err != nil {
				return nil, err
			}
			options := extension.BeforeAgentStartOptions(args[1].(context.Context))
			options.AppendSystemPrompt += "|" + name + "=" + strconv.Itoa(len(prompt))
			return nil, nil
		}}}}
	}
	want := runPromptChain(t, reference)
	assertPromptChainShape(t, want)

	t.Run("inproc", func(t *testing.T) { assertPromptChain(t, runPromptChain(t, reference), want) })
	t.Run("fused-go", func(t *testing.T) {
		host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
		t.Cleanup(func() { host.Shutdown("prompt chain conformance complete") })
		var loaded []extension.Extension
		for _, name := range promptChainNames {
			ext := sdk.New(name)
			ext.OnEvent("before_agent_start", func(_ sdk.Context, data map[string]any) (any, error) {
				options := data["systemPromptOptions"].(map[string]any)
				prompt, err := data["systemPrompt"].(*sdk.SystemPrompt).Get()
				if err != nil {
					return nil, err
				}
				options["appendSystemPrompt"] = options["appendSystemPrompt"].(string) + "|" + name + "=" + strconv.Itoa(len(prompt))
				return nil, nil
			})
			one, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: name, Enabled: true}, ext.RunWithConn)
			if err != nil {
				t.Fatal(err)
			}
			loaded = append(loaded, *one)
		}
		assertPromptChain(t, runPromptChain(t, loaded), want)
	})
	for _, language := range []string{"go", "python", "rust", "node"} {
		t.Run(language, func(t *testing.T) {
			for _, placement := range []string{"isolated", "packed"} {
				t.Run(placement, func(t *testing.T) {
					host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { host.Shutdown("prompt chain conformance complete") })
					configs := make([]subprocess.ExtConfig, len(promptChainNames))
					for i, name := range promptChainNames {
						configs[i] = promptChainFactory(t, root, language, name)
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
						loaded, err = host.Reload(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						report := host.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("not packed: %+v", report)
						}
					}
					assertPromptChain(t, runPromptChain(t, loaded), want)
				})
			}
		})
	}
}

// runPromptChain returns the appendSystemPrompt the chain of extensions leaves in the shared options.
func runPromptChain(t *testing.T, exts []extension.Extension) string {
	t.Helper()
	runner := inproc.NewRunner(exts, t.TempDir())
	defer runner.Invalidate("prompt chain conformance complete")
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err.Error) })
	result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("ordinary", nil), extension.BuildSystemPromptOptions{AppendSystemPrompt: "base append"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reported) != 0 {
		t.Fatalf("handler errors: %q", reported)
	}
	if result == nil || result.SystemPromptOptions == nil {
		t.Fatalf("the chain's edits did not reach the result: %+v", result)
	}
	return result.SystemPromptOptions.AppendSystemPrompt
}

func assertPromptChain(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("append = %q, want %q", got, want)
	}
}

// assertPromptChainShape checks the reference against Pi's contract without comparing it with itself: the length handler k+1 reads is the length handler k read plus the text handler k appended.
func assertPromptChainShape(t *testing.T, appended string) {
	t.Helper()
	rest, ok := strings.CutPrefix(appended, "base append")
	if !ok {
		t.Fatalf("append = %q", appended)
	}
	parts := strings.Split(strings.TrimPrefix(rest, "|"), "|")
	if len(parts) != len(promptChainNames) {
		t.Fatalf("append = %q", appended)
	}
	previous, previousAppended := 0, 0
	for i, part := range parts {
		name, length, found := strings.Cut(part, "=")
		n, err := strconv.Atoi(length)
		if !found || name != promptChainNames[i] || err != nil || n <= len("base append") {
			t.Fatalf("part %d = %q in %q", i, part, appended)
		}
		if i > 0 && n != previous+previousAppended {
			t.Fatalf("%s read a prompt of %d bytes, want %d (previous %d plus its append of %d)", name, n, previous+previousAppended, previous, previousAppended)
		}
		previous, previousAppended = n, len("|"+part)
	}
}

func promptChainFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		source := fmt.Sprintf(`export default function(pi) { pi.on("before_agent_start", async event => { event.systemPromptOptions.appendSystemPrompt += "|%s=" + event.systemPrompt.length; }); }`, name)
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "prompt-chain-" + name
	var path, source string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		source = fmt.Sprintf(`package chain
import ("strconv"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension {
 e:=sdk.New(%q)
 e.OnEvent("before_agent_start",func(_ sdk.Context,data map[string]any)(any,error){
  o:=data["systemPromptOptions"].(map[string]any)
  prompt,err:=data["systemPrompt"].(*sdk.SystemPrompt).Get()
  if err!=nil {return nil,err}
  o["appendSystemPrompt"]=o["appendSystemPrompt"].(string)+"|%s="+strconv.Itoa(len(prompt))
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
  data["systemPromptOptions"]["appendSystemPrompt"]+="|%s="+str(len(data["systemPrompt"].encode("utf-8")))
 e.on_event("before_agent_start",handler)
 return e
`, name, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		source = fmt.Sprintf(`use pig_sdk::{Extension,Schema};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 e.on_before_agent_start(|_,event| {
  let len=event.system_prompt().map_err(|e| e.to_string())?.len();
  event.edit_system_prompt_options(|options| {
   let appended=format!("{}|%s={}",options["appendSystemPrompt"].as_str().unwrap_or(""),len);
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
