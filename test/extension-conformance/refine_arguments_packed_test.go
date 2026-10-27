package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_PackedRefinedToolArgumentsAcrossSDKs: two extensions of one language share one packed cell, each registering the tool "refine-packed" with a refined property. The
// host asks each extension (not its neighbor) for the predicate calls of its own tool, and the answer and the order of the calls are Pi's (the "packed:" cases of
// test/parity/sdk-vectors/pi-ai/validateToolArguments.json).
func TestConformance_PackedRefinedToolArgumentsAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	raw, err := os.ReadFile(filepath.Join("..", "parity", "sdk-vectors", "pi-ai", "validateToolArguments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []refineVectorCase }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	var cases []refineVectorCase
	for _, c := range file.Cases {
		if strings.HasPrefix(c.Name, "packed: ") {
			cases = append(cases, c)
		}
	}
	if len(cases) == 0 {
		t.Fatal("no packed cases in validateToolArguments.json")
	}
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			b := subprocess.NewUIBridge(func() {})
			b.SetUIContext(newRecordingUI(new([]string), new([]string)))
			messages := make(chan string, 8)
			b.SetNotifyFunc(func(message, _ string) { messages <- message })
			h.SetUIBridge(b)
			configs := []subprocess.ExtConfig{packedRefineFactory(t, root, language, "first"), packedRefineFactory(t, root, language, "second")}
			loaded, failures := h.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("loaded=%d errors=%v", len(loaded), failures)
			}
			for _, ext := range loaded {
				tool, ok := ext.Tools["refine-packed"]
				if !ok || len(tool.Definition.RefinementSites) == 0 || tool.Definition.RefineArguments == nil {
					t.Fatalf("%s: the host does not know the refinements of refine-packed", ext.Name)
				}
				schema := tool.Definition.ValidationParameters
				if len(schema) == 0 {
					schema = tool.Definition.Parameters
				}
				readLog := func() []string {
					if err := ext.Commands["refine_log"].Handler(t.Context(), ""); err != nil {
						t.Fatal(err)
					}
					select {
					case message := <-messages:
						rest, _ := strings.CutPrefix(message, ext.Name+":")
						var calls []string
						if err := json.Unmarshal([]byte(rest), &calls); err != nil {
							t.Fatalf("log %q: %v", message, err)
						}
						if calls == nil {
							calls = []string{}
						}
						return calls
					case <-time.After(30 * time.Second):
						t.Fatal("refine_log: no report")
					}
					return nil
				}
				readLog()
				for _, c := range cases {
					var toolCall struct {
						Arguments json.RawMessage `json:"arguments"`
					}
					if err := json.Unmarshal(c.Args[1], &toolCall); err != nil {
						t.Fatal(err)
					}
					validated, err := ai.ValidateToolArgumentsRefined(t.Context(), "t", schema, toolCall.Arguments, toolRefiner{tool.Definition})
					calls := readLog()
					want := c.Result.Calls
					if want == nil {
						want = []string{}
					}
					switch {
					case c.Result.Error != nil:
						if err == nil || err.Error() != *c.Result.Error {
							t.Errorf("%s %s: error %v\n want %q", ext.Name, c.Name, err, *c.Result.Error)
						}
					case err != nil:
						t.Errorf("%s %s: error %v, Pi accepts", ext.Name, c.Name, err)
					default:
						var got, wantArgs any
						_ = json.Unmarshal(validated, &got)
						_ = json.Unmarshal(c.Result.Args, &wantArgs)
						if !reflect.DeepEqual(got, wantArgs) {
							t.Errorf("%s %s: arguments %s, Pi %s", ext.Name, c.Name, validated, c.Result.Args)
						}
					}
					if !reflect.DeepEqual(calls, want) {
						t.Errorf("%s %s: calls %v\n  Pi %v", ext.Name, c.Name, calls, want)
					}
				}
			}
		})
	}
}

func packedRefineFactory(t *testing.T, root, language, name string) subprocess.ExtConfig {
	t.Helper()
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "refine-" + name
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		code = fmt.Sprintf(`package flags
import ("encoding/json"; "strings"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {
 e:=sdk.New(%q)
 var log []string
 refined:=sdk.Type.Refine(sdk.Type.String(),func(v any)(bool,error){log=append(log,"check:p");s,_:=v.(string);return len(s)>=3,nil},func(any)(string,error){log=append(log,"error:pm");return "a is too short",nil})
 e.Tool("refine-packed","",sdk.Type.Object(sdk.NewOrderedObject("a",refined)).Schema(),func(sdk.Context,map[string]any)(any,error){return "ok",nil})
 e.Command("refine_log","",func(ctx sdk.Context,_ string)error{wire,_:=json.Marshal(append([]string{},log...));log=nil;ctx.Notify(e.Name()+":"+strings.TrimSpace(string(wire)),"info");return nil})
 return e
}`, name)
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		code = fmt.Sprintf(`import json
import pig_sdk
def new_extension():
 e = pig_sdk.Extension(%q)
 log = []
 def check(v):
  log.append("check:p")
  return len(v) >= 3
 def worded(v):
  log.append("error:pm")
  return "a is too short"
 Type = pig_sdk.Type
 e.tool("refine-packed", "", Type.object({"a": Type.refine(Type.string(), check, worded)}), lambda ctx,args: "ok")
 def report(ctx, args):
  calls = list(log)
  log.clear()
  ctx.notify(e.name + ":" + json.dumps(calls), "info")
 e.command("refine_log", "", report)
 return e
`, name)
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		code = fmt.Sprintf(`use pig_sdk::{Extension,Schema,ToolResult,CommandResult,Type};
use std::sync::{Arc,Mutex};
pub fn new_extension()->Extension {
 let mut e=Extension::new(%q);
 let log:Arc<Mutex<Vec<String>>>=Arc::new(Mutex::new(Vec::new()));
 let (check_log,error_log)=(log.clone(),log.clone());
 let refined=Type::refine(Type::string(None),move|v:&Schema|{check_log.lock().unwrap().push("check:p".to_string());Ok(v.as_str().is_some_and(|s|s.len()>=3))},Some(Box::new(move|_:&Schema|{error_log.lock().unwrap().push("error:pm".to_string());Ok("a is too short".to_string())})));
 e.tool_typed("refine-packed","",&Type::object([("a",refined)],None),|_,_|ToolResult::text("ok"));
 let reported=log.clone();
 e.command("refine_log","",move|ctx,_|{let calls:Vec<String>=std::mem::take(&mut *reported.lock().unwrap());let wire=format!("[{}]",calls.iter().map(|c|format!("\"{}\"",c)).collect::<Vec<_>>().join(","));ctx.notify(&format!("{}:{}",%q,wire),"info");CommandResult::Ok});
 e
}`, name, name)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
