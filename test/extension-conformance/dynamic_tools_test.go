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
					return codingagent.ExtensionToolInfos(runner, dynamicToolNames(), nil)
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
				if !ok || def.Label != "Label late v2" || def.Description != "v2" || def.PromptSnippet != "summary v2" || !reflect.DeepEqual(def.PromptGuidelines, []string{"Use late v2"}) {
					t.Fatalf("replacement metadata=%+v", def)
				}
				if shout, ok := session.GetToolDefinition("shout"); !ok || shout.Label != "Label shout new" {
					t.Fatalf("a tool registered after startup keeps its label: %+v", shout)
				}
				// Registering many tools one after another, an extension's getAllTools and getActiveTools answer the registry as it is after each registration, and a replaced definition reads back as the replacement: the host's reply to registerTool carries no catalog for the SDK to keep, so every read is the host's.
				if err := loaded[0].Commands["burst"].Handler(t.Context(), ""); err != nil {
					t.Fatalf("registry read after each registration: %v", err)
				}
				if def, ok := session.GetToolDefinition("b0"); !ok || def.Description != "d0x" {
					t.Fatalf("b0 after replacement=%+v", def)
				}
				executeDynamicTool(t, session, "b7", "db7:hello")
				api.SetActiveTools([]string{"late", "shout"})
				executeDynamicTool(t, session, "late", "v2:hello")
				executeDynamicTool(t, session, "shout", "new:hello")
			})
		}
	}
}

// dynamicToolNames is the registry filter of the getAllTools host action: the tools the dynamic extension registers.
func dynamicToolNames() map[string]struct{} {
	names := map[string]struct{}{"late": {}, "stable": {}, "shout": {}}
	for i := range dynamicBurstTools {
		names[fmt.Sprintf("b%d", i)] = struct{}{}
	}
	return names
}

// dynamicBurstTools is how many tools the burst command registers, b0 to b7; after b3 it registers b0 again with another description.
const dynamicBurstTools = 8

// checkDynamicRegistry is the burst command's read after a registration: getAllTools lists exactly names in registration order with the described descriptions, and getActiveTools lists exactly active.
func checkDynamicRegistry(ctx sdk.Context, names, active []string, described map[string]string) error {
	infos, err := ctx.GetAllTools()
	if err != nil {
		return err
	}
	got := make([]string, len(infos))
	for i, info := range infos {
		got[i] = info.Name
		if want, ok := described[info.Name]; ok && info.Description != want {
			return fmt.Errorf("%s description=%q, want %q", info.Name, info.Description, want)
		}
	}
	if !slices.Equal(got, names) {
		return fmt.Errorf("getAllTools=%v, want %v", got, names)
	}
	gotActive, err := ctx.GetActiveTools()
	if err != nil {
		return err
	}
	if !slices.Equal(gotActive, active) {
		return fmt.Errorf("getActiveTools=%v, want %v", gotActive, active)
	}
	return nil
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
		return sdk.ToolDefinition{Name: name, Label: "Label " + name + " " + value, Description: value, PromptSnippet: "summary " + value, PromptGuidelines: []string{"Use " + name + " " + value}, Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}, "required": []string{"message"}}, Execute: func(_ sdk.Context, args map[string]any) (any, error) {
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
	e.Command("burst", "", func(ctx sdk.Context, _ string) error {
		names, active, described := []string{"stable", "late", "shout"}, []string{"stable", "shout"}, map[string]string{}
		for i := range dynamicBurstTools {
			name := fmt.Sprintf("b%d", i)
			described[name] = "d" + name
			ctx.RegisterTool(def(name, described[name]))
			names, active = append(names, name), append(active, name)
			if err := checkDynamicRegistry(ctx, names, active, described); err != nil {
				return err
			}
			if i == 3 {
				described["b0"] = "d0x"
				ctx.RegisterTool(def("b0", "d0x"))
				if err := checkDynamicRegistry(ctx, names, active, described); err != nil {
					return err
				}
			}
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
const def=(name,value)=>({name,label:"Label "+name+" "+value,description:value,promptSnippet:"summary "+value,promptGuidelines:["Use "+name+" "+value],parameters:{type:"object",properties:{message:{type:"string"}},required:["message"]},execute:async(_id,args)=>({content:[{type:"text",text:value+":"+args.message}]})});
pi.registerTool({name:"stable",label:"stable",description:"stable",parameters:{},execute:async()=>({content:[]})});
pi.on("session_start",()=>pi.registerTool(def("late","v1")));
pi.events.on("inspect-dynamic-tools",values=>values.push(pi.getAllTools().some(t=>t.name==="shout")));
pi.registerCommand("change",{handler:async()=>{pi.setActiveTools(["stable"]);pi.registerTool(def("late","v2"));pi.registerTool(def("shout","new"));if(JSON.stringify(pi.getActiveTools())!==JSON.stringify(["stable","shout"]))throw new Error("immediate active tools="+JSON.stringify(pi.getActiveTools()));if(!pi.getAllTools().some(t=>t.name==="shout"))throw new Error("immediate getAllTools missing shout");const values=[];pi.events.emit("inspect-dynamic-tools",values);if(values.some(v=>!v))throw new Error("peer registry is stale");}});
pi.registerCommand("burst",{handler:async()=>{const names=["stable","late","shout"],active=["stable","shout"],desc={};const check=()=>{const tools=pi.getAllTools();const got=tools.map(t=>t.name);if(JSON.stringify(got)!==JSON.stringify(names))throw new Error("getAllTools="+JSON.stringify(got)+" want "+JSON.stringify(names));for(const t of tools)if(t.name in desc&&t.description!==desc[t.name])throw new Error(t.name+" description="+t.description+" want "+desc[t.name]);const act=pi.getActiveTools();if(JSON.stringify(act)!==JSON.stringify(active))throw new Error("getActiveTools="+JSON.stringify(act)+" want "+JSON.stringify(active));};for(let i=0;i<8;i++){const name="b"+i;desc[name]="d"+name;pi.registerTool(def(name,desc[name]));names.push(name);active.push(name);check();if(i===3){desc.b0="d0x";pi.registerTool(def("b0","d0x"));check();}}}});
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
def:=func(name,value string)sdk.ToolDefinition{return sdk.ToolDefinition{Name:name,Label:"Label "+name+" "+value,Description:value,PromptSnippet:"summary "+value,PromptGuidelines:[]string{"Use "+name+" "+value},Parameters:sdk.Schema{"type":"object","properties":map[string]any{"message":map[string]any{"type":"string"}},"required":[]string{"message"}},Execute:func(_ sdk.Context,args map[string]any)(any,error){return value+":"+args["message"].(string),nil}}}
e.OnSessionStart(func(ctx sdk.Context,_ map[string]any)(any,error){e.RegisterTool(def("late","v1"));return nil,nil})
e.Command("change","",func(ctx sdk.Context,_ string)error{ctx.SetActiveTools([]string{"stable"});ctx.RegisterTool(def("late","v2"));ctx.RegisterTool(def("shout","new"));if got,err:=ctx.GetActiveTools();err!=nil||!slices.Equal(got,[]string{"stable","shout"}){return fmt.Errorf("immediate active tools=%%v %%v",got,err)};return nil})
e.Command("burst","",func(ctx sdk.Context,_ string)error{
names,active,described:=[]string{"stable","late","shout"},[]string{"stable","shout"},map[string]string{}
check:=func()error{
infos,err:=ctx.GetAllTools();if err!=nil{return err}
got:=make([]string,len(infos))
for i,info:=range infos{got[i]=info.Name;if want,ok:=described[info.Name];ok&&info.Description!=want{return fmt.Errorf("%%s description=%%q, want %%q",info.Name,info.Description,want)}}
if !slices.Equal(got,names){return fmt.Errorf("getAllTools=%%v, want %%v",got,names)}
gotActive,err:=ctx.GetActiveTools();if err!=nil{return err}
if !slices.Equal(gotActive,active){return fmt.Errorf("getActiveTools=%%v, want %%v",gotActive,active)}
return nil}
for i:=0;i<8;i++{
name:=fmt.Sprintf("b%%d",i);described[name]="d"+name
ctx.RegisterTool(def(name,described[name]))
names,active=append(names,name),append(active,name)
if err:=check();err!=nil{return err}
if i==3{described["b0"]="d0x";ctx.RegisterTool(def("b0","d0x"));if err:=check();err!=nil{return err}}
}
return nil})
return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import pig_sdk
schema={"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}
def definition(name,value):
 return pig_sdk.ToolDefinition(name=name,label="Label "+name+" "+value,description=value,prompt_snippet="summary "+value,prompt_guidelines=["Use "+name+" "+value],parameters=schema,execute=lambda ctx,args:value+":"+args["message"])
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
 def burst(ctx,args):
  names=["stable","late","shout"];active=["stable","shout"];desc={}
  def check():
   tools=ctx.get_all_tools()
   assert [t["name"] for t in tools]==names,[t["name"] for t in tools]
   for t in tools:
    if t["name"] in desc: assert t["description"]==desc[t["name"]],t
   assert ctx.get_active_tools()==active,ctx.get_active_tools()
  for i in range(8):
   name="b%%d"%%i
   desc[name]="d"+name
   ctx.register_tool(definition(name,desc[name]))
   names.append(name);active.append(name);check()
   if i==3:
    desc["b0"]="d0x";ctx.register_tool(definition("b0","d0x"));check()
 e.command("burst","",burst)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,ToolDefinition,ToolResult,CommandResult,Schema};
fn definition(name:&str,value:&str)->ToolDefinition{
 let value=value.to_owned();let result=value.clone();
 let mut d=ToolDefinition::new(name,format!("Label {} {}",name,value),&value,r#"{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}"#.parse::<Schema>().unwrap(),move|_,args|ToolResult::text(format!("{}:{}",result,args["message"].as_str().unwrap())));
 d.prompt_snippet=Some(format!("summary {}",value));d.prompt_guidelines=vec![format!("Use {} {}",name,value)];d
}
pub fn new_extension()->Extension{
 let mut e=Extension::new(%q);
 e.tool("stable","stable","{}".parse::<Schema>().unwrap(),|_,_|ToolResult::text("stable"));
 e.on_event("session_start",|ctx,_|{ctx.register_tool(definition("late","v1")).unwrap();None});
 e.command("change","",|ctx,_|{ctx.set_active_tools(&["stable"]);if let Err(e)=ctx.register_tool(definition("late","v2")).and_then(|_|ctx.register_tool(definition("shout","new"))){return CommandResult::Error(e.to_string())};if ctx.get_active_tools().unwrap()!=vec!["stable","shout"]{return CommandResult::Error("immediate active tools".into())};CommandResult::Ok});
 e.command("burst","",|ctx,_|{
  let mut names:Vec<String>=vec!["stable".into(),"late".into(),"shout".into()];
  let mut active:Vec<String>=vec!["stable".into(),"shout".into()];
  let mut desc=std::collections::HashMap::<String,String>::new();
  let check=|names:&Vec<String>,active:&Vec<String>,desc:&std::collections::HashMap<String,String>|->Result<(),String>{
   let tools=ctx.get_all_tools().map_err(|e|e.to_string())?;
   let got:Vec<String>=tools.iter().map(|t|t.name.clone()).collect();
   if &got!=names{return Err(format!("getAllTools={:?} want {:?}",got,names))}
   for t in &tools{if let Some(d)=desc.get(&t.name){if &t.description!=d{return Err(format!("{} description={:?} want {:?}",t.name,t.description,d))}}}
   let act=ctx.get_active_tools().map_err(|e|e.to_string())?;
   if &act!=active{return Err(format!("getActiveTools={:?} want {:?}",act,active))}
   Ok(())
  };
  for i in 0..8{
   let name=format!("b{}",i);let value=format!("d{}",name);
   desc.insert(name.clone(),value.clone());
   if let Err(e)=ctx.register_tool(definition(&name,&value)){return CommandResult::Error(e.to_string())}
   names.push(name.clone());active.push(name);
   if let Err(e)=check(&names,&active,&desc){return CommandResult::Error(e)}
   if i==3{
    desc.insert("b0".into(),"d0x".into());
    if let Err(e)=ctx.register_tool(definition("b0","d0x")){return CommandResult::Error(e.to_string())}
    if let Err(e)=check(&names,&active,&desc){return CommandResult::Error(e)}
   }
  }
  CommandResult::Ok
 });
 e
}`, name)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
