package extensionconformance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi 1.2.0 types.ts:2122-2130 picks reachesTools into ToolInfo and agent-session.ts:1507 copies each definition's hook, so an
// extension's handler (Pi's MCP extension in its tool_call handler) asks which tools a call may reach. In every SDK a tool handler
// runs the reachesTools getAllTools hands it for its own tool, for a neighbor extension's tool (in its own process, or in the same
// process when the cell is packed), and for a hook the host itself defines; a tool without the hook has none, and the hook's throw
// reaches the caller as its error. A Node extension waits for its own hook in a synchronous host call, so the answer must not
// queue behind the handler that asked.
func TestGetAllToolsReachesToolsAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	prefixes := map[string]string{"go": "go", "python": "py", "rust": "rs", "node": "node"}
	hostTools := []subprocess.ToolInfo{
		{Name: "host_reach", Description: "Host", Parameters: json.RawMessage(`{}`), Exposure: extension.ToolExposureDirect,
			ReachesTools: func(input json.RawMessage) extension.ToolReach {
				var fields map[string]any
				_ = json.Unmarshal(input, &fields)
				var words []string
				for key := range fields {
					words = append(words, "host:"+key)
				}
				slices.Sort(words)
				return extension.ToolReach{Words: words}
			}},
		{Name: "host_fail", Description: "Host", Parameters: json.RawMessage(`{}`), Exposure: extension.ToolExposureDirect,
			ReachesTools: func(json.RawMessage) extension.ToolReach { panic(errors.New("reach exploded")) }},
	}
	for _, language := range []string{"go", "python", "rust", "node"} {
		for _, isolation := range []string{"strict", "shared-ok"} {
			t.Run(language+"/"+isolation, func(t *testing.T) {
				t.Parallel()
				h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
				t.Cleanup(func() { h.Shutdown("test complete") })
				bridge := subprocess.NewUIBridge(func() {})
				h.SetUIBridge(bridge)
				configs := []subprocess.ExtConfig{reachToolsFactory(t, root, language, "reach"), reachToolsFactory(t, root, language, "peer")}
				for i := range configs {
					configs[i].Isolation = isolation
				}
				loaded, failures := h.LoadAll(t.Context(), configs)
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
				runner := inproc.NewRunner(loaded, t.TempDir())
				bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
					return append(codingagent.ExtensionToolInfos(runner, nil, nil), hostTools...)
				})
				var probe *extension.RegisteredTool
				for _, tool := range runner.Tools() {
					if tool.Definition.Name == "probe" {
						probe = &tool
					}
				}
				if probe == nil {
					t.Fatal("probe is not registered")
				}
				result, err := probe.Definition.Execute(t.Context(), "tc-probe", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				p := prefixes[language]
				if want := fmt.Sprintf("%[1]s:reach:a,%[1]s:reach:b|all|%[1]s:peer:c|host:x|false|threw", p); result.IsError || result.Text() != want {
					t.Fatalf("probe = %q (isError %v), want %q", result.Text(), result.IsError, want)
				}
			})
		}
	}
}

// reachToolsFactory writes an extension named name that registers <name>_finder, whose reachesTools answers "all" for {all: true}
// and otherwise the input's keys as <prefix>:<name>:<key>. The extension named reach also registers probe, which reports what the
// reachesTools of getAllTools answer for its own finder, the peer's finder and the host's tools, joined by "|".
func reachToolsFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		write(path, fmt.Sprintf(`export default function (pi) {
  const name = %q;
  pi.registerTool({ name: name + "_finder", label: "finder", description: "Finds", parameters: { type: "object" },
    reachesTools: (input) => (input.all ? "all" : Object.keys(input).sort().map((key) => "node:" + name + ":" + key)),
    execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
  if (name !== "reach") return;
  pi.registerTool({ name: "probe", label: "probe", description: "Probe", parameters: { type: "object" }, execute: async () => {
    const tools = Object.fromEntries(pi.getAllTools().map((tool) => [tool.name, tool]));
    const show = (reach) => (reach === "all" ? "all" : reach.join(","));
    let failed = "returned";
    try { tools.host_fail.reachesTools({}); } catch (error) { failed = String(error.message).includes("reach exploded") ? "threw" : String(error.message); }
    const text = [show(tools.reach_finder.reachesTools({ b: 1, a: 2 })), show(tools.reach_finder.reachesTools({ all: true })), show(tools.peer_finder.reachesTools({ c: 1 })), show(tools.host_reach.reachesTools({ x: 1 })), String("reachesTools" in tools.probe), failed].join("|");
    return { content: [{ type: "text", text }] };
  } });
}
`, name))
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: "shared-ok"}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "reaches-tools-" + name
	switch language {
	case "go":
		write(filepath.Join(cfg.Source, "extension.go"), fmt.Sprintf(`package flags
import ("fmt";"sort";"strings";sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
const name=%q
func Extension()*sdk.Extension{
 e:=sdk.New(name)
 e.RegisterTool(sdk.ToolDefinition{Name:name+"_finder",Label:"finder",Description:"Finds",Parameters:sdk.Schema{"type":"object"},
  ReachesTools:func(input map[string]any)sdk.ToolReach{if input["all"]==true{return sdk.ToolReach{All:true}};words:=[]string{};for key:=range input{words=append(words,"go:"+name+":"+key)};sort.Strings(words);return sdk.ToolReach{Words:words}},
  Execute:func(sdk.Context,map[string]any)(any,error){return "ok",nil}})
 if name!="reach"{return e}
 e.Tool("probe","Probe",sdk.Schema{"type":"object"},func(ctx sdk.Context,_ map[string]any)(any,error){
  tools,err:=ctx.GetAllTools();if err!=nil{return nil,err}
  find:=func(name string)sdk.ToolInfo{for _,tool:=range tools{if tool.Name==name{return tool}};return sdk.ToolInfo{}}
  show:=func(reach sdk.ToolReach)string{if reach.All{return "all"};return strings.Join(reach.Words,",")}
  failed:=func()(out string){defer func(){if r:=recover();r!=nil{out=fmt.Sprint(r);if strings.Contains(out,"reach exploded"){out="threw"}}}();find("host_fail").ReachesTools(map[string]any{});return "returned"}()
  return strings.Join([]string{show(find("reach_finder").ReachesTools(map[string]any{"b":1,"a":2})),show(find("reach_finder").ReachesTools(map[string]any{"all":true})),show(find("peer_finder").ReachesTools(map[string]any{"c":1})),show(find("host_reach").ReachesTools(map[string]any{"x":1})),fmt.Sprint(find("probe").ReachesTools!=nil),failed},"|"),nil})
 return e
}`, name))
	case "python":
		write(filepath.Join(cfg.Source, cfg.Package+".py"), fmt.Sprintf(`import pig_sdk
NAME = %q
def reach(input):
    if input.get("all"):
        return "all"
    return ["py:" + NAME + ":" + key for key in sorted(input)]
def probe(ctx, args):
    tools = {tool["name"]: tool for tool in ctx.get_all_tools()}
    show = lambda reach: reach if reach == "all" else ",".join(reach)
    try:
        tools["host_fail"]["reachesTools"]({})
        failed = "returned"
    except pig_sdk.HostCallError as error:
        failed = "threw" if "reach exploded" in str(error) else str(error)
    return "|".join([show(tools["reach_finder"]["reachesTools"]({"b": 1, "a": 2})), show(tools["reach_finder"]["reachesTools"]({"all": True})), show(tools["peer_finder"]["reachesTools"]({"c": 1})), show(tools["host_reach"]["reachesTools"]({"x": 1})), str("reachesTools" in tools["probe"]).lower(), failed])
def new_extension():
    e = pig_sdk.Extension(NAME)
    e.register_tool(pig_sdk.ToolDefinition(name=NAME + "_finder", label="finder", description="Finds", parameters={"type": "object"}, execute=lambda ctx, args: "ok", reaches_tools=reach))
    if NAME == "reach":
        e.tool("probe", "Probe", {"type": "object"}, probe)
    return e
`, name))
	case "rust":
		write(filepath.Join(cfg.Source, "Cargo.toml"), fmt.Sprintf("[package]\nname=%q\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\nserde_json=\"1\"\n", name, filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs"))))
		write(filepath.Join(cfg.Source, "src", "lib.rs"), fmt.Sprintf(`use pig_sdk::{Extension,ToolDefinition,ToolReach,ToolResult,empty_schema};
use serde_json::json;
const NAME:&str=%q;
fn show(reach:ToolReach)->String{match reach{ToolReach::All=>"all".into(),ToolReach::Words(words)=>words.join(",")}}
pub fn new_extension()->Extension{
 let mut e=Extension::new(NAME);
 let mut finder=ToolDefinition::new(&format!("{NAME}_finder"),"finder","Finds",empty_schema(),|_,_|ToolResult::text("ok"));
 finder.reaches_tools=Some(Box::new(|input|{if input["all"]==true{return ToolReach::All} let mut words:Vec<String>=input.as_object().map(|o|o.keys().map(|key|format!("rs:{NAME}:{key}")).collect()).unwrap_or_default();words.sort();ToolReach::Words(words)}));
 e.register_tool(finder);
 if NAME=="reach"{
  e.tool("probe","Probe",empty_schema(),|ctx,_|{
   let tools=match ctx.get_all_tools(){Ok(tools)=>tools,Err(err)=>return ToolResult::Error(err.to_string())};
   let find=|name:&str|tools.iter().find(|tool|tool.name==name).and_then(|tool|tool.reaches_tools.clone());
   let ask=|name:&str,input:serde_json::Value|find(name).map(|reach|reach.call(&input).map(show).unwrap_or_else(|err|err.to_string())).unwrap_or_else(||"none".into());
   let failed=match find("host_fail").map(|reach|reach.call(&json!({}))){Some(Err(err)) if err.to_string().contains("reach exploded")=>"threw".to_string(),other=>format!("{other:?}")};
   ToolResult::text([ask("reach_finder",json!({"b":1,"a":2})),ask("reach_finder",json!({"all":true})),ask("peer_finder",json!({"c":1})),ask("host_reach",json!({"x":1})),find("probe").is_some().to_string(),failed].join("|"))
  });
 }
 e
}`, name))
	}
	return cfg
}
