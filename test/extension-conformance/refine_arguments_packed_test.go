package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_PackedRefinedToolArgumentsAcrossSDKs: two extensions of one language share one packed cell, each registering the tool "refine-packed" with a refined property.
// The argument a refinement would reject validates unchanged and neither extension is asked to run a predicate (its call log stays empty).
func TestConformance_PackedRefinedToolArgumentsAcrossSDKs(t *testing.T) {
	root := findModuleRoot(t)
	for _, language := range []string{"go", "python", "rust", "lisp"} {
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
				if !ok {
					t.Fatalf("%s: refine-packed is not registered", ext.Name)
				}
				assertNoRefinementProtocol(t, tool.Definition)
				// The call goes through the host as the agent loop issues it (validation, hooks, execution of the tool), so a host that ran a refinement or failed the call fails here.
				tools, errs := coding.BridgeNewRunnerTools([]extension.RegisteredTool{tool})
				if len(errs) != 0 {
					t.Fatal(errs)
				}
				provider := ai.NewFauxProvider(ai.FauxConfig{})
				provider.SetResponses([]ai.FauxResponseStep{
					ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("refine-packed", map[string]any{"a": "x"}, &ai.FauxToolCallOptions{ID: "refine-packed-0"})}, StopReason: "toolUse"}),
					ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
				})
				a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
				sent, err := a.Send(t.Context(), "call the tool")
				if err != nil {
					t.Fatal(err)
				}
				answered := false
				for _, message := range sent {
					if message.ToolResult == nil {
						continue
					}
					answered = true
					if message.ToolResult.IsError || message.ToolResult.Text() != "ok" {
						t.Errorf("%s: the tool answered %q (error=%v), want \"ok\": Pi accepts the arguments, it does not run refinements", ext.Name, message.ToolResult.Text(), message.ToolResult.IsError)
					}
				}
				if !answered {
					t.Errorf("%s: the tool call produced no result", ext.Name)
				}
				if err := ext.Commands["refine_log"].Handler(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
				select {
				case message := <-messages:
					rest, _ := strings.CutPrefix(message, ext.Name+":")
					if rest != "[]" {
						t.Errorf("%s ran refinements: %q", ext.Name, rest)
					}
				case <-time.After(30 * time.Second):
					t.Fatal("refine_log: no report")
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
	case "lisp":
		path = filepath.Join(cfg.Source, name+".lisp")
		code = fmt.Sprintf(`(defpackage %q (:use "COMMON-LISP"))
(in-package %q)
(defun new-extension ()
  (let ((e (pig:make-extension %q)) (log '()))
    (pig:tool e "refine-packed" ""
              (pig:type-object (pig:obj "a" (pig:type-refine (pig:type-string)
                                                             (lambda (v) (push "check:p" log) (and (stringp v) (>= (length v) 3)))
                                                             (lambda (v) (declare (ignore v)) (push "error:pm" log) "a is too short"))))
              (lambda (ctx args) (declare (ignore ctx args)) (pig:tool-result "ok")))
    (pig:command e "refine_log" "" (lambda (ctx args) (declare (ignore args))
                                      (let ((calls (coerce (reverse log) 'simple-vector)))
                                        (setf log '())
                                        (pig:ctx-notify ctx (format nil "~A:~A" %q (pig:to-json calls)) "info"))))
    e))
`, name, name, name, name)
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
