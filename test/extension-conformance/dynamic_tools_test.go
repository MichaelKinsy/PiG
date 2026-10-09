package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// sessionAPI is the Go reference for the tool members of extension.API: the production Session behind getActiveTools, getAllTools
// and setActiveTools, the actions the host wires for every SDK.
type sessionAPI struct {
	extension.API
	session *coding.Session
}

func (a sessionAPI) GetActiveTools() []string          { return a.session.ActiveToolNames() }
func (a sessionAPI) GetAllTools() []extension.ToolInfo { return a.session.GetAllTools() }
func (a sessionAPI) SetActiveTools(names []string)     { a.session.SetActiveToolsByName(names) }

// Pi pi.registerTool (packages/coding-agent/src/core/extensions/types.ts:1632, loader.ts:289-301) and agent-session.ts:3481-3579 _refreshToolRegistry require immediate publication, in-place replacement, and activation only for newly admitted names.
func TestDynamicToolRegistrationAcrossSDKs(t *testing.T) {
	requireAPIMember(t, "GetActiveTools", extension.API.GetActiveTools)
	requireAPIMember(t, "SetActiveTools", extension.API.SetActiveTools)
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
					ext, err := h.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "dynamic", Enabled: true}, dynamicGoExtension().RunWithConn)
					if err != nil {
						t.Fatal(err)
					}
					loaded = append(loaded, *ext)
				} else {
					cfg := dynamicToolFactory(t, root, language, "dynamic")
					cfg.Isolation = isolation
					configs := []subprocess.ExtConfig{cfg}
					if isolation == "shared-ok" {
						sibling := dynamicToolFactory(t, root, language, "sibling")
						configs = append(configs, sibling)
					}
					var failures []error
					loaded, failures = h.LoadAll(t.Context(), configs)
					if len(failures) != 0 || len(loaded) != len(configs) {
						t.Fatalf("load: %v", failures)
					}
					if isolation == "shared-ok" {
						h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
						var err error
						loaded, err = h.Reload(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						report := h.LastReloadReport()
						if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
							t.Fatalf("expected packed %s realization: %+v", language, report)
						}
					}
				}
				runner := inproc.NewRunner(loaded, t.TempDir())
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, NoSession: true, SkipBuiltinTools: true})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				var api extension.API = sessionAPI{session: session}
				bridge.SetHostAction("refreshTools", session.RefreshTools)
				bridge.SetHostAction("setActiveTools", session.SetActiveToolsByName)
				bridge.SetHostAction("getActiveTools", session.ActiveToolNames)
				bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
					return codingagent.ExtensionToolInfos(runner, map[string]struct{}{"late": {}, "stable": {}, "shout": {}}, nil)
				})
				if got := api.GetActiveTools(); !slices.Equal(got, []string{"stable"}) {
					t.Fatal(got)
				}
				if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{}); err != nil {
					t.Fatal(err)
				}
				if got := api.GetActiveTools(); !slices.Equal(got, []string{"stable", "late"}) {
					t.Fatalf("startup active=%v", got)
				}
				executeDynamicTool(t, session, "late", "v1:hello")
				command := loaded[0].Commands["change"]
				if err := command.Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				if got := api.GetActiveTools(); !slices.Equal(got, []string{"stable", "shout"}) {
					t.Fatalf("registerTool replacement reactivated disabled tool or failed to activate new tool: %v", got)
				}
				infos := api.GetAllTools()
				names := make([]string, len(infos))
				for i, info := range infos {
					names[i] = info.Name
				}
				if !slices.Equal(names, []string{"stable", "late", "shout"}) {
					t.Fatalf("ordered registry=%v", names)
				}
				def, ok := session.GetToolDefinition("late")
				if !ok || def.Description != "v2" || def.PromptSnippet != "summary v2" || !reflect.DeepEqual(def.PromptGuidelines, []string{"Use late v2"}) {
					t.Fatalf("replacement metadata=%+v", def)
				}
				api.SetActiveTools([]string{"late", "shout"})
				executeDynamicTool(t, session, "late", "v2:hello")
				executeDynamicTool(t, session, "shout", "new:hello")
			})
		}
	}
}

func executeDynamicTool(t *testing.T, s *coding.Session, name, want string) {
	t.Helper()
	for _, tool := range s.Tools() {
		if tool.Name() == name {
			result, err := tool.Execute(t.Context(), "dynamic", []byte(`{"message":"hello"}`), nil)
			if err != nil || result.IsError || result.Text() != want {
				t.Fatalf("%s result=%+v err=%v want=%q", name, result, err, want)
			}
			return
		}
	}
	t.Fatalf("tool %s missing", name)
}

func dynamicGoExtension() *sdk.Extension {
	e := sdk.New("dynamic")
	e.Tool("stable", "stable", sdk.Schema{}, func(sdk.Context, map[string]any) (any, error) { return "stable", nil })
	def := func(name, value string) sdk.ToolDefinition {
		return sdk.ToolDefinition{Name: name, Label: name, Description: value, PromptSnippet: "summary " + value, PromptGuidelines: []string{"Use " + name + " " + value}, Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}, "required": []string{"message"}}, Execute: func(_ sdk.Context, args map[string]any) (any, error) {
			return value + ":" + args["message"].(string), nil
		}}
	}
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.RegisterTool(def("late", "v1"))
		return nil, nil
	})
	e.Command("change", "", func(ctx sdk.Context, _ string) error {
		ctx.SetActiveTools([]string{"stable"})
		ctx.RegisterTool(def("late", "v2"))
		ctx.RegisterTool(def("shout", "new"))
		got, err := ctx.GetActiveTools()
		if err != nil {
			return fmt.Errorf("immediate active tools: %w", err)
		}
		if !slices.Equal(got, []string{"stable", "shout"}) {
			return fmt.Errorf("immediate active tools=%v", got)
		}
		return nil
	})
	return e
}

func dynamicToolFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		code := `export default function(pi){
const def=(name,value)=>({name,label:name,description:value,promptSnippet:"summary "+value,promptGuidelines:["Use "+name+" "+value],parameters:{type:"object",properties:{message:{type:"string"}},required:["message"]},execute:async(_id,args)=>({content:[{type:"text",text:value+":"+args.message}]})});
pi.registerTool({name:"stable",label:"stable",description:"stable",parameters:{},execute:async()=>({content:[]})});
pi.on("session_start",()=>pi.registerTool(def("late","v1")));
pi.events.on("inspect-dynamic-tools",values=>values.push(pi.getAllTools().some(t=>t.name==="shout")));
pi.registerCommand("change",{handler:async()=>{pi.setActiveTools(["stable"]);pi.registerTool(def("late","v2"));pi.registerTool(def("shout","new"));if(JSON.stringify(pi.getActiveTools())!==JSON.stringify(["stable","shout"]))throw new Error("immediate active tools="+JSON.stringify(pi.getActiveTools()));if(!pi.getAllTools().some(t=>t.name==="shout"))throw new Error("immediate getAllTools missing shout");const values=[];pi.events.emit("inspect-dynamic-tools",values);if(values.some(v=>!v))throw new Error("peer registry is stale");}});
}`
		if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: "shared-ok"}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "dynamic-tools-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import("fmt";"slices";sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension{
e:=sdk.New(%q)
e.Tool("stable","stable",sdk.Schema{},func(sdk.Context,map[string]any)(any,error){return "stable",nil})
def:=func(name,value string)sdk.ToolDefinition{return sdk.ToolDefinition{Name:name,Label:name,Description:value,PromptSnippet:"summary "+value,PromptGuidelines:[]string{"Use "+name+" "+value},Parameters:sdk.Schema{"type":"object","properties":map[string]any{"message":map[string]any{"type":"string"}},"required":[]string{"message"}},Execute:func(_ sdk.Context,args map[string]any)(any,error){return value+":"+args["message"].(string),nil}}}
e.OnSessionStart(func(ctx sdk.Context,_ map[string]any)(any,error){e.RegisterTool(def("late","v1"));return nil,nil})
e.Command("change","",func(ctx sdk.Context,_ string)error{ctx.SetActiveTools([]string{"stable"});ctx.RegisterTool(def("late","v2"));ctx.RegisterTool(def("shout","new"));if got,err:=ctx.GetActiveTools();err!=nil||!slices.Equal(got,[]string{"stable","shout"}){return fmt.Errorf("immediate active tools=%%v %%v",got,err)};return nil})
return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import pig_sdk
schema={"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}
def definition(name,value):
 return pig_sdk.ToolDefinition(name=name,label=name,description=value,prompt_snippet="summary "+value,prompt_guidelines=["Use "+name+" "+value],parameters=schema,execute=lambda ctx,args:value+":"+args["message"])
def new_extension():
 e=pig_sdk.Extension(%q)
 e.tool("stable","stable",{},lambda ctx,args:"stable")
 e.on_event("session_start",lambda ctx,data:e.register_tool(definition("late","v1")))
 def change(ctx,args):
  ctx.set_active_tools(["stable"])
  ctx.register_tool(definition("late","v2"))
  ctx.register_tool(definition("shout","new"))
  assert ctx.get_active_tools()==["stable","shout"]
 e.command("change","",change)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,ToolDefinition,ToolResult,CommandResult,Schema};
fn definition(name:&str,value:&str)->ToolDefinition{
 let value=value.to_owned();let result=value.clone();
 let mut d=ToolDefinition::new(name,name,&value,r#"{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}"#.parse::<Schema>().unwrap(),move|_,args|ToolResult::text(format!("{}:{}",result,args["message"].as_str().unwrap())));
 d.prompt_snippet=Some(format!("summary {}",value));d.prompt_guidelines=vec![format!("Use {} {}",name,value)];d
}
pub fn new_extension()->Extension{
 let mut e=Extension::new(%q);
 e.tool("stable","stable","{}".parse::<Schema>().unwrap(),|_,_|ToolResult::text("stable"));
 e.on_event("session_start",false,|ctx,_|{ctx.register_tool(definition("late","v1")).unwrap();None});
 e.command("change","",|ctx,_|{ctx.set_active_tools(&["stable"]);if let Err(e)=ctx.register_tool(definition("late","v2")).and_then(|_|ctx.register_tool(definition("shout","new"))){return CommandResult::Error(e.to_string())};if ctx.get_active_tools().unwrap()!=vec!["stable","shout"]{return CommandResult::Error("immediate active tools".into())};CommandResult::Ok});
 e
}`, name)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
