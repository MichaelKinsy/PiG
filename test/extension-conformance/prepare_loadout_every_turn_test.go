package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const (
	loadoutBefore = "Watch, before the tool ran."
	loadoutAfter  = "Watch, after the tool ran."
)

// Pi runs every active tool's prepareLoadout hook before each turn of a run (agent-session.ts:899-933 prepareNextTurnWithContext calls _preparePromptAndToolLoadout, which calls _applyToolLoadout at :1747). A hook that reads state the run changed therefore shapes the second request of a run: its description and its hidden declarations. The hook runs in the extension, so each SDK answers it from state of its own process, which the tool changed; the values are distinct from the tool's registered description and from every SDK's fallback, so an SDK whose hook is not asked before the later turn fails.
func TestPrepareLoadoutRunsBeforeEveryTurnAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	for _, language := range []string{"go", "python", "rust", "node", "fused-go"} {
		for _, isolation := range []string{"strict", "shared-ok"} {
			if language == "fused-go" && isolation == "shared-ok" {
				continue
			}
			t.Run(language+"/"+isolation, func(t *testing.T) {
				h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
				t.Cleanup(func() { h.Shutdown("test complete") })
				bridge := subprocess.NewUIBridge(func() {})
				h.SetUIBridge(bridge)
				var loaded []extension.Extension
				if language == "fused-go" {
					ext, err := h.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "loadout", Enabled: true}, loadoutGoExtension().RunWithConn)
					if err != nil {
						t.Fatal(err)
					}
					loaded = append(loaded, *ext)
				} else {
					configs := []subprocess.ExtConfig{loadoutFactory(t, root, language, "loadout")}
					configs[0].Isolation = isolation
					if isolation == "shared-ok" {
						configs = append(configs, loadoutSibling(t, root, language))
					}
					var failures []error
					loaded, failures = h.LoadAll(t.Context(), configs)
					if len(failures) != 0 || len(loaded) != len(configs) {
						t.Fatalf("load: %v", failures)
					}
					if isolation == "shared-ok" {
						h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
						var err error
						if loaded, err = h.Reload(t.Context()); err != nil {
							t.Fatal(err)
						}
						report := h.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("expected packed %s realization: %+v", language, report)
						}
					}
				}
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(services.Close)
				configureFauxAuth(t, services)
				faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
				if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
					t.Fatal(err)
				}
				runner := inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
				session, err := coding.NewSession(services, coding.SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: runner, NoSession: true, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				events := make(chan struct{})
				go func() {
					defer close(events)
					for event := range session.Events() {
						coding.AcknowledgeEvent(event)
					}
				}()
				t.Cleanup(func() { _ = session.Close(); <-events })
				bridge.SetHostAction("refreshTools", session.RefreshTools)
				bridge.SetHostAction("setActiveTools", session.SetActiveToolsByName)
				bridge.SetHostAction("getActiveTools", session.ActiveToolNames)
				bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
					return codingagent.ExtensionToolInfos(runner, map[string]struct{}{"watch": {}, "other": {}}, nil)
				})
				if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{}); err != nil {
					t.Fatal(err)
				}
				var declared [][]string
				record := func(request ai.TranscriptContext) {
					var entries []string
					for _, tool := range ai.GetCurrentTools(request.Messages()) {
						entries = append(entries, tool.Name+": "+tool.Description)
					}
					slices.Sort(entries)
					declared = append(declared, entries)
				}
				faux.SetResponses([]ai.FauxResponseStep{
					ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
						record(request)
						return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxToolCall("watch", map[string]any{}, &ai.FauxToolCallOptions{ID: "call_watch"})}, ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)}), nil
					}),
					ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
						record(request)
						return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
					}),
				})
				if err := session.Prompt(t.Context(), "go"); err != nil {
					t.Fatal(err)
				}
				want := [][]string{{"other: Other.", "watch: " + loadoutBefore}, {"watch: " + loadoutAfter}}
				if fmt.Sprint(declared) != fmt.Sprint(want) {
					t.Fatalf("declarations per request = %q, want %q", declared, want)
				}
			})
		}
	}
}

// loadoutGoExtension is the Go reference of the loadout fixture: a tool whose hook reads whether the tool ran.
func loadoutGoExtension() *sdk.Extension {
	e := sdk.New("loadout")
	executed := false
	e.RegisterTool(sdk.ToolDefinition{
		Name: "watch", Label: "watch", Description: "Watch.", Parameters: sdk.Schema{"type": "object"},
		PrepareLoadout: func(sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
			if !executed {
				return &sdk.ToolLoadoutChanges{Descriptions: map[string]string{"watch": loadoutBefore}}
			}
			return &sdk.ToolLoadoutChanges{Descriptions: map[string]string{"watch": loadoutAfter}, HiddenDeclarations: []string{"other"}}
		},
		Execute: func(sdk.Context, map[string]any) (any, error) { executed = true; return "watched", nil },
	})
	e.Tool("other", "Other.", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) { return "other", nil })
	return e
}

// loadoutSibling is a second extension of the language, which the loader packs with the loadout fixture.
func loadoutSibling(t *testing.T, root, language string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), "sibling.mjs")
		if err := os.WriteFile(path, []byte("export default function(pi){}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: "sibling", Source: path, Enabled: true, Isolation: "shared-ok"}
	}
	cfg := packedFlagFactory(t, root, language, "sibling", false)
	cfg.ContentHash = "loadout-sibling"
	return cfg
}

func loadoutFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		code := fmt.Sprintf(`export default function(pi){
let executed=false;
pi.registerTool({name:"watch",label:"watch",description:"Watch.",parameters:{type:"object",properties:{}},
prepareLoadout:()=>executed?{descriptions:{watch:%q},hiddenDeclarations:["other"]}:{descriptions:{watch:%q}},
execute:async()=>{executed=true;return{content:[{type:"text",text:"watched"}]}}});
pi.registerTool({name:"other",label:"other",description:"Other.",parameters:{type:"object",properties:{}},execute:async()=>({content:[{type:"text",text:"other"}]})});
}
`, loadoutAfter, loadoutBefore)
		if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: "shared-ok"}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "loadout-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension()*sdk.Extension{
e:=sdk.New(%q)
executed:=false
e.RegisterTool(sdk.ToolDefinition{Name:"watch",Label:"watch",Description:"Watch.",Parameters:sdk.Schema{"type":"object"},
PrepareLoadout:func(sdk.ToolLoadout)*sdk.ToolLoadoutChanges{
if !executed{return &sdk.ToolLoadoutChanges{Descriptions:map[string]string{"watch":%q}}}
return &sdk.ToolLoadoutChanges{Descriptions:map[string]string{"watch":%q},HiddenDeclarations:[]string{"other"}}},
Execute:func(sdk.Context,map[string]any)(any,error){executed=true;return "watched",nil}})
e.Tool("other","Other.",sdk.Schema{"type":"object"},func(sdk.Context,map[string]any)(any,error){return "other",nil})
return e
}`, name, loadoutBefore, loadoutAfter)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 state={"executed":False}
 def prepare(loadout):
  if not state["executed"]:
   return {"descriptions":{"watch":%q}}
  return {"descriptions":{"watch":%q},"hiddenDeclarations":["other"]}
 def execute(ctx,args):
  state["executed"]=True
  return "watched"
 e.register_tool(pig_sdk.ToolDefinition(name="watch",label="watch",description="Watch.",parameters={"type":"object"},prepare_loadout=prepare,execute=execute))
 e.tool("other","Other.",{"type":"object"},lambda ctx,args:"other")
 return e
`, name, loadoutBefore, loadoutAfter)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,ToolDefinition,ToolLoadoutChanges,ToolResult,Schema};
use std::sync::{Arc,atomic::{AtomicBool,Ordering}};
pub fn new_extension()->Extension{
 let mut e=Extension::new(%q);
 let executed=Arc::new(AtomicBool::new(false));
 let ran=executed.clone();
 let mut watch=ToolDefinition::new("watch","watch","Watch.","{\"type\":\"object\"}".parse::<Schema>().unwrap(),move|_,_|{ran.store(true,Ordering::SeqCst);ToolResult::text("watched")});
 watch.prepare_loadout=Some(Box::new(move|_|{
  if !executed.load(Ordering::SeqCst){
   return Some(ToolLoadoutChanges{descriptions:Some([("watch".to_string(),%q.to_string())].into()),hidden_declarations:None});
  }
  Some(ToolLoadoutChanges{descriptions:Some([("watch".to_string(),%q.to_string())].into()),hidden_declarations:Some(vec!["other".to_string()])})
 }));
 e.register_tool(watch);
 e.tool("other","Other.","{\"type\":\"object\"}".parse::<Schema>().unwrap(),|_,_|ToolResult::text("other"));
 e
}`, name, loadoutBefore, loadoutAfter)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
