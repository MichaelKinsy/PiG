package extensionconformance

// Pi bd3c9b8237 (#11062, #11075): agent-session.ts _restoreToolsFromTranscript. A resumed session and a /tree navigation restore the loadout the branch recorded, and activate the extension tools the whole branch never added or removed unless they are registered with defaultActive false; a tool the branch removed stays inactive. An extension observes the result through getActiveTools.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// resumeToolsActive is the command every realization registers: it fails unless the extension's getActiveTools, sorted, is the comma-separated list in its arguments.
const resumeToolsActive = "active"

func TestResumeActivatesExtensionToolsTheBranchNeverMentionedAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	for _, language := range []string{"go", "python", "rust", "node", "fused-go"} {
		for _, isolation := range []string{"strict", "shared-ok"} {
			if language == "fused-go" && isolation == "shared-ok" {
				continue
			}
			t.Run(language+"/"+isolation, func(t *testing.T) {
				t.Parallel()
				h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
				t.Cleanup(func() { h.Shutdown("test complete") })
				bridge := subprocess.NewUIBridge(func() {})
				h.SetUIBridge(bridge)
				var loaded []extension.Extension
				if language == "fused-go" {
					ext, err := h.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "resume", Enabled: true}, resumeToolsGoExtension().RunWithConn)
					if err != nil {
						t.Fatal(err)
					}
					loaded = append(loaded, *ext)
				} else {
					cfg := resumeToolsFactory(t, root, language, "resume")
					cfg.Isolation = isolation
					configs := []subprocess.ExtConfig{cfg}
					if isolation == "shared-ok" {
						configs = append(configs, resumeToolsFactory(t, root, language, "resume-sibling"))
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
				// The sibling of a packed cell registers the same names; only the first extension's view is read.
				runner := inproc.NewRunner(loaded[:1], t.TempDir())
				manager, err := coding.NewInMemorySessionManager(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				record := func(added, removed []string) {
					t.Helper()
					message := &ai.SystemMessage{Content: ai.SystemText(""), Timestamp: 1}
					for _, name := range added {
						message.ToolsAdded = append(message.ToolsAdded, ai.ToolSchema{Name: name, Description: name, Parameters: map[string]any{"type": "object", "properties": map[string]any{}}})
					}
					for _, name := range removed {
						message.ToolsRemoved = append(message.ToolsRemoved, ai.ToolReference{Name: name})
					}
					if _, err := manager.AppendMessage(agent.AgentMessage{System: message}); err != nil {
						t.Fatal(err)
					}
				}
				// The branch recorded read, stable and retired, then removed retired; it never saw fresh or quiet. A later branch leaf swaps them.
				record([]string{"read", "stable", "retired"}, nil)
				record(nil, []string{"retired"})
				target, err := manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "first"}}, Timestamp: 1}})
				if err != nil {
					t.Fatal(err)
				}
				record([]string{"retired"}, []string{"stable"})
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, SessionManager: manager})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				bridge.SetHostAction("getActiveTools", session.ActiveToolNames)
				if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{}); err != nil {
					t.Fatal(err)
				}
				check := func(stage, want string) {
					t.Helper()
					if err := loaded[0].Commands[resumeToolsActive].Handler(t.Context(), want); err != nil {
						t.Fatalf("%s: %v (host active %v)", stage, err, session.ActiveToolNames())
					}
				}
				// retired is active again on the leaf's branch, fresh was never mentioned, quiet is registered with defaultActive false, stable was removed.
				check("resume", "fresh,read,retired")
				if _, err := session.NavigateTree(t.Context(), target, coding.NavigateTreeOptions{}); err != nil {
					t.Fatal(err)
				}
				// On the target branch retired was removed and stable recorded.
				check("navigate", "fresh,read,stable")
			})
		}
	}
}

// resumeToolsGoExtension registers stable, retired, fresh and quiet (defaultActive false) at load.
func resumeToolsGoExtension() *sdk.Extension {
	e := sdk.New("resume")
	inactive := false
	for _, name := range []string{"stable", "retired", "fresh", "quiet"} {
		def := sdk.ToolDefinition{Name: name, Label: name, Description: name, Parameters: sdk.Schema{"type": "object", "properties": map[string]any{}}, Execute: func(sdk.Context, map[string]any) (any, error) { return name, nil }}
		if name == "quiet" {
			def.DefaultActive = &inactive
		}
		e.RegisterTool(def)
	}
	e.Command(resumeToolsActive, "", func(ctx sdk.Context, args string) error {
		got, err := ctx.GetActiveTools()
		if err != nil {
			return err
		}
		slices.Sort(got)
		if strings.Join(got, ",") != args {
			return fmt.Errorf("getActiveTools=%v, want %s", got, args)
		}
		return nil
	})
	return e
}

func resumeToolsFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		path := filepath.Join(t.TempDir(), name+".mjs")
		code := `export default function(pi){
for (const name of ["stable","retired","fresh","quiet"]) pi.registerTool({name,label:name,description:name,parameters:{type:"object",properties:{}},...(name==="quiet"?{defaultActive:false}:{}),execute:async()=>({content:[{type:"text",text:name}]})});
pi.registerCommand("active",{handler:async(args)=>{const got=pi.getActiveTools().slice().sort().join(",");if(got!==args)throw new Error("getActiveTools="+got+" want "+args);}});
}`
		if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		return subprocess.ExtConfig{Name: name, Source: path, Enabled: true, Isolation: "shared-ok"}
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "resume-tools-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import("fmt";"sort";"strings";sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension()*sdk.Extension{
e:=sdk.New(%q)
inactive:=false
for _,n:=range []string{"stable","retired","fresh","quiet"}{
n:=n
def:=sdk.ToolDefinition{Name:n,Label:n,Description:n,Parameters:sdk.Schema{"type":"object","properties":map[string]any{}},Execute:func(sdk.Context,map[string]any)(any,error){return n,nil}}
if n=="quiet"{def.DefaultActive=&inactive}
e.RegisterTool(def)
}
e.Command("active","",func(ctx sdk.Context,args string)error{
got,err:=ctx.GetActiveTools();if err!=nil{return err}
sort.Strings(got)
if strings.Join(got,",")!=args{return fmt.Errorf("getActiveTools=%%v, want %%s",got,args)}
return nil})
return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import pig_sdk
def new_extension():
 e=pig_sdk.Extension(%q)
 for n in ["stable","retired","fresh","quiet"]:
  e.register_tool(pig_sdk.ToolDefinition(name=n,label=n,description=n,parameters={"type":"object","properties":{}},execute=lambda ctx,args,n=n:n,default_active=False if n=="quiet" else None))
 def active(ctx,args):
  got=",".join(sorted(ctx.get_active_tools()))
  assert got==args,"getActiveTools="+got+" want "+args
 e.command("active","",active)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,ToolDefinition,ToolResult,CommandResult,Schema};
pub fn new_extension()->Extension{
 let mut e=Extension::new(%q);
 for n in ["stable","retired","fresh","quiet"]{
  let mut d=ToolDefinition::new(n,n,n,r#"{"type":"object","properties":{}}"#.parse::<Schema>().unwrap(),move|_,_|ToolResult::text(n));
  if n=="quiet"{d.default_active=Some(false);}
  e.register_tool(d);
 }
 e.command("active","",|ctx,args|{
  let mut got=ctx.get_active_tools().unwrap();got.sort();
  let got=got.join(",");
  if got!=args{return CommandResult::Error(format!("getActiveTools={} want {}",got,args))}
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
