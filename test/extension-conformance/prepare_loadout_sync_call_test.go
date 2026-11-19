package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// syncLoadoutOptions is what a fixture of TestPrepareLoadoutAnswersWhileTheExtensionWaitsOnASynchronousCallAcrossSDKs does.
type syncLoadoutOptions struct {
	// hook registers the tool `hooked` with a prepareLoadout hook that describes it with what the extension saw: "Hook saw <seen>".
	hook bool
	// late registers the tool `late` in its session_start handler, after loading, and sees "registered".
	late bool
	// active calls setActiveTools(["hooked", "other"]) in its session_start handler and then getActiveTools, and sees the names it returns joined with "+".
	active bool
}

const syncLoadoutNothingSeen = "nothing yet"

// Pi runs a tool's prepareLoadout hook inside the call that refreshes the loadout (agent-session.ts:1500-1560 _applyToolLoadout, from _refreshToolRegistry at registerTool and from setActiveToolsByName), so an extension that registers a tool after loading, or calls setActiveTools and then a synchronous API, gets its own hook, and every other extension's, run before the call returns. PiG's host asks each hook with a tool_prepare_loadout request before it answers the registerTool call, or the synchronous call that runs behind an unfinished setActiveTools, so the extension must answer a request while it waits on its own call (#516). A runtime that serves requests only between its calls, as a single-threaded one blocked in the call does, hangs the extension and the host on each other.
//
// Each realization of every SDK runs three extensions of its language: one whose hook is asked while its own registerTool waits, one whose hook is asked while its own getActiveTools waits behind setActiveTools, and (packed, where the members share a process) one whose hook is asked while a sibling's registerTool waits. The hook describes its tool with what its extension saw after the call, a value no SDK's default produces, and the next turn's declarations carry it.
func TestPrepareLoadoutAnswersWhileTheExtensionWaitsOnASynchronousCallAcrossSDKs(t *testing.T) {
	t.Parallel()
	root := findModuleRoot(t)
	type mode struct {
		name string
		// hooked and registrar are the fixtures: the extension with the hook, and the one that registers a tool after loading (the same extension when nil).
		hooked, registrar syncLoadoutOptions
		packedOnly        bool
		want              []string
	}
	modes := []mode{
		{name: "register", hooked: syncLoadoutOptions{hook: true, late: true}, want: []string{"extra: Extra.", "hooked: Hook saw registered", "late: Late.", "other: Other."}},
		{name: "active", hooked: syncLoadoutOptions{hook: true, active: true}, want: []string{"hooked: Hook saw hooked+other", "other: Other."}},
		{name: "sibling", hooked: syncLoadoutOptions{hook: true}, registrar: syncLoadoutOptions{late: true}, packedOnly: true, want: []string{"extra: Extra.", "hooked: Hook saw " + syncLoadoutNothingSeen, "late: Late.", "other: Other."}},
	}
	for _, language := range []string{"go", "python", "rust", "node", "fused-go", "lisp"} {
		for _, isolation := range []string{"strict", "shared-ok"} {
			if language == "fused-go" && isolation == "shared-ok" {
				continue
			}
			for _, m := range modes {
				if m.packedOnly && (isolation != "shared-ok") {
					continue
				}
				t.Run(language+"/"+isolation+"/"+m.name, func(t *testing.T) {
					h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
					t.Cleanup(func() { h.Shutdown("test complete") })
					bridge := subprocess.NewUIBridge(func() {})
					h.SetUIBridge(bridge)
					var loaded []extension.Extension
					// Building a Go or Rust fixture takes as long as the machine makes it; the hang this test finds is bounded below, once everything is loaded.
					loadCtx, cancelLoad := context.WithTimeout(t.Context(), 10*time.Minute)
					defer cancelLoad()
					if language == "fused-go" {
						ext, err := h.LoadInProcess(loadCtx, subprocess.ExtConfig{Name: "loadout", Enabled: true}, syncLoadoutGoExtension("loadout", m.hooked).RunWithConn)
						if err != nil {
							t.Fatal(err)
						}
						loaded = append(loaded, *ext)
					} else {
						first := syncLoadoutFactory(t, root, language, "loadout", m.name+"-hooked", m.hooked)
						first.Isolation = isolation
						configs := []subprocess.ExtConfig{first}
						if isolation == "shared-ok" || m.registrar != (syncLoadoutOptions{}) {
							second := syncLoadoutFactory(t, root, language, "sibling", m.name+"-sibling", m.registrar)
							second.Isolation = isolation
							configs = append(configs, second)
						}
						var failures []error
						loaded, failures = h.LoadAll(loadCtx, configs)
						if len(failures) != 0 || len(loaded) != len(configs) {
							t.Fatalf("load: %v", failures)
						}
						if isolation == "shared-ok" {
							h.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
							var err error
							if loaded, err = h.Reload(loadCtx); err != nil {
								t.Fatal(err)
							}
							report := h.LastReloadReport()
							if report == nil || len(report.Cells) != 1 || report.Cells[0].Strategy != subprocess.CellStrategy("packed-"+language) {
								t.Fatalf("expected packed %s realization: %+v", language, report)
							}
						}
					}
					bindCtx, cancelBind := context.WithTimeout(t.Context(), 90*time.Second)
					defer cancelBind()
					declared := syncLoadoutDeclarations(t, bindCtx, h, bridge, loaded)
					if !slices.Equal(declared, m.want) {
						t.Fatalf("declarations = %q, want %q", declared, m.want)
					}
				})
			}
		}
	}
}

// syncLoadoutSession is a session bound to the loaded extensions over a faux provider.
type syncLoadoutSession struct {
	session *coding.Session
	faux    *ai.FauxProviderHandle
	// reported lists the failures the runner reported, "event: error".
	reported func() []string
}

// newSyncLoadoutSession binds the extensions to a session. Binding is bounded by ctx: an extension that never answers the host holds session_start for good, and that fails the test, not the whole package.
func newSyncLoadoutSession(t *testing.T, ctx context.Context, h *subprocess.Host, bridge *subprocess.UIBridge, loaded []extension.Extension) *syncLoadoutSession {
	t.Helper()
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
	var mu sync.Mutex
	var failures []string
	runner.AddErrorListener(func(failure *extension.ExtensionError) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, failure.Event+": "+failure.Error)
	})
	session, err := coding.NewSession(services, coding.SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan struct{})
	eventsCh := session.Events()
	go func() {
		defer close(events)
		for event := range eventsCh {
			coding.AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-events })
	bridge.SetHostAction("refreshTools", session.RefreshTools)
	bridge.SetHostAction("setActiveTools", session.SetActiveToolsByName)
	bridge.SetHostAction("getActiveTools", session.ActiveToolNames)
	bridge.SetHostAction("getAllTools", func() []subprocess.ToolInfo {
		// The extension tools of every fixture of this file.
		names := map[string]struct{}{"hooked": {}, "other": {}, "extra": {}, "late": {}, "with_loadout": {}, "late_tool": {}, "workflow": {}, "workflow_catalog": {}, "register_now": {}, "noop": {}, "made_late": {}, "from_hook": {}}
		return codingagent.ExtensionToolInfos(runner, names, nil)
	})
	if err := session.BindExtensions(ctx, coding.ExtensionBindings{}); err != nil {
		t.Fatalf("session_start did not finish: %v", err)
	}
	return &syncLoadoutSession{session: session, faux: faux, reported: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(failures)
	}}
}

// syncLoadoutDeclarations binds the extensions to a session, runs one prompt and returns the tools its request declared, "name: description" and sorted.
func syncLoadoutDeclarations(t *testing.T, ctx context.Context, h *subprocess.Host, bridge *subprocess.UIBridge, loaded []extension.Extension) []string {
	t.Helper()
	s := newSyncLoadoutSession(t, ctx, h, bridge, loaded)
	var declared []string
	s.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxFactoryStep(func(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			for _, tool := range ai.GetCurrentTools(request.Messages()) {
				declared = append(declared, tool.Name+": "+tool.Description)
			}
			slices.Sort(declared)
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
		}),
	})
	if err := s.session.Prompt(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	return declared
}

// syncLoadoutGoExtension is the Go reference of the fixtures: the in-process form of syncLoadoutFactory.
func syncLoadoutGoExtension(name string, opts syncLoadoutOptions) *sdk.Extension {
	e := sdk.New(name)
	var mu sync.Mutex
	seen := syncLoadoutNothingSeen
	see := func(value string) { mu.Lock(); seen = value; mu.Unlock() }
	schema := sdk.Schema{"type": "object"}
	if opts.hook {
		e.RegisterTool(sdk.ToolDefinition{
			Name: "hooked", Label: "hooked", Description: "Hooked.", Parameters: schema,
			PrepareLoadout: func(sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
				mu.Lock()
				defer mu.Unlock()
				return &sdk.ToolLoadoutChanges{Descriptions: map[string]string{"hooked": "Hook saw " + seen}}
			},
			Execute: func(sdk.Context, map[string]any) (any, error) { return "hooked", nil },
		})
		e.Tool("other", "Other.", schema, func(sdk.Context, map[string]any) (any, error) { return "other", nil })
		e.Tool("extra", "Extra.", schema, func(sdk.Context, map[string]any) (any, error) { return "extra", nil })
	}
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if opts.late {
			e.RegisterTool(sdk.ToolDefinition{Name: "late", Label: "late", Description: "Late.", Parameters: schema, Execute: func(sdk.Context, map[string]any) (any, error) { return "late", nil }})
			see("registered")
		}
		if opts.active {
			ctx.SetActiveTools([]string{"hooked", "other"})
			active, err := ctx.GetActiveTools()
			if err != nil {
				return nil, err
			}
			see(strings.Join(active, "+"))
		}
		return nil, nil
	})
	return e
}

// syncLoadoutFactory writes the fixture of the language: an extension that does what opts says.
func syncLoadoutFactory(t *testing.T, root, language, name, mode string, opts syncLoadoutOptions) subprocess.ExtConfig {
	t.Helper()
	if language == "node" {
		return syncLoadoutNodeFactory(t, name, opts)
	}
	cfg := packedFlagFactory(t, root, language, name, false)
	cfg.ContentHash = "sync-loadout-" + name + "-" + mode
	var path, code string
	switch language {
	case "go":
		path = filepath.Join(cfg.Source, "extension.go")
		var body strings.Builder
		body.WriteString("package flags\nimport(\"strings\";\"sync\";sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\")\nfunc Extension()*sdk.Extension{\ne:=sdk.New(\"" + name + "\")\nvar mu sync.Mutex\nseen:=\"" + syncLoadoutNothingSeen + "\"\n_=seen\nsee:=func(v string){mu.Lock();seen=v;mu.Unlock()}\n_=see\n_=strings.Join\nschema:=sdk.Schema{\"type\":\"object\"}\n_=schema\n")
		if opts.hook {
			body.WriteString(`e.RegisterTool(sdk.ToolDefinition{Name:"hooked",Label:"hooked",Description:"Hooked.",Parameters:schema,
PrepareLoadout:func(sdk.ToolLoadout)*sdk.ToolLoadoutChanges{mu.Lock();defer mu.Unlock();return &sdk.ToolLoadoutChanges{Descriptions:map[string]string{"hooked":"Hook saw "+seen}}},
Execute:func(sdk.Context,map[string]any)(any,error){return "hooked",nil}})
e.Tool("other","Other.",schema,func(sdk.Context,map[string]any)(any,error){return "other",nil})
e.Tool("extra","Extra.",schema,func(sdk.Context,map[string]any)(any,error){return "extra",nil})
`)
		}
		body.WriteString("e.OnSessionStart(func(ctx sdk.Context,_ map[string]any)(any,error){\n")
		if opts.late {
			body.WriteString(`e.RegisterTool(sdk.ToolDefinition{Name:"late",Label:"late",Description:"Late.",Parameters:schema,Execute:func(sdk.Context,map[string]any)(any,error){return "late",nil}})
see("registered")
`)
		}
		if opts.active {
			body.WriteString("ctx.SetActiveTools([]string{\"hooked\",\"other\"})\nactive,err:=ctx.GetActiveTools();if err!=nil{return nil,err}\nsee(strings.Join(active,\"+\"))\n")
		}
		body.WriteString("return nil,nil})\nreturn e\n}")
		code = body.String()
	case "python":
		path = filepath.Join(cfg.Source, strings.ReplaceAll(name, "-", "_")+".py")
		var body strings.Builder
		body.WriteString("import pig_sdk\ndef new_extension():\n e=pig_sdk.Extension(\"" + name + "\")\n state={\"seen\":\"" + syncLoadoutNothingSeen + "\"}\n")
		if opts.hook {
			body.WriteString(` def prepare(loadout):
  return {"descriptions":{"hooked":"Hook saw "+state["seen"]}}
 e.register_tool(pig_sdk.ToolDefinition(name="hooked",label="hooked",description="Hooked.",parameters={"type":"object"},prepare_loadout=prepare,execute=lambda ctx,args:"hooked"))
 e.tool("other","Other.",{"type":"object"},lambda ctx,args:"other")
 e.tool("extra","Extra.",{"type":"object"},lambda ctx,args:"extra")
`)
		}
		body.WriteString(" def start(ctx,data):\n  pass\n")
		if opts.late {
			body.WriteString(`  e.register_tool(pig_sdk.ToolDefinition(name="late",label="late",description="Late.",parameters={"type":"object"},execute=lambda ctx,args:"late"))
  state["seen"]="registered"
`)
		}
		if opts.active {
			body.WriteString("  ctx.set_active_tools([\"hooked\",\"other\"])\n  state[\"seen\"]=\"+\".join(ctx.get_active_tools())\n")
		}
		body.WriteString(" e.on_event(\"session_start\",start)\n return e\n")
		code = body.String()
	case "lisp":
		path = filepath.Join(cfg.Source, name+".lisp")
		var body strings.Builder
		body.WriteString("(defpackage \"" + name + "\" (:use \"COMMON-LISP\"))\n(in-package \"" + name + "\")\n(defun new-extension ()\n  (let ((e (pig:make-extension \"" + name + "\")) (seen \"" + syncLoadoutNothingSeen + "\"))\n    (declare (ignorable seen))\n")
		if opts.hook {
			body.WriteString(`    (pig:register-tool e "hooked" :label "hooked" :description "Hooked." :parameters (pig:obj "type" "object")
      :prepare-loadout (lambda (loadout) (declare (ignore loadout)) (pig:obj "descriptions" (pig:obj "hooked" (concatenate 'string "Hook saw " seen))))
      :execute (lambda (ctx args) (declare (ignore ctx args)) "hooked"))
    (pig:tool e "other" "Other." (pig:obj "type" "object") (lambda (ctx args) (declare (ignore ctx args)) "other"))
    (pig:tool e "extra" "Extra." (pig:obj "type" "object") (lambda (ctx args) (declare (ignore ctx args)) "extra"))
`)
		}
		body.WriteString("    (pig:on e \"session_start\" (lambda (ctx data) (declare (ignorable ctx data))\n")
		if opts.late {
			body.WriteString(`      (pig:register-tool e "late" :label "late" :description "Late." :parameters (pig:obj "type" "object")
        :execute (lambda (ctx args) (declare (ignore ctx args)) "late"))
      (setf seen "registered")
`)
		}
		if opts.active {
			body.WriteString("      (pig:ctx-set-active-tools ctx '(\"hooked\" \"other\"))\n      (setf seen (format nil \"~{~A~^+~}\" (coerce (pig:ctx-get-active-tools ctx) 'list)))\n")
		}
		body.WriteString("      nil))\n    e))\n")
		code = body.String()
	case "rust":
		path = filepath.Join(cfg.Source, "src", "lib.rs")
		var body strings.Builder
		body.WriteString("use pig_sdk::{Extension,ToolDefinition,ToolLoadoutChanges,ToolResult,Schema};\nuse std::sync::{Arc,Mutex};\nfn schema()->Schema{\"{\\\"type\\\":\\\"object\\\"}\".parse::<Schema>().unwrap()}\npub fn new_extension()->Extension{\n let mut e=Extension::new(\"" + name + "\");\n let seen=Arc::new(Mutex::new(String::from(\"" + syncLoadoutNothingSeen + "\")));\n")
		if opts.hook {
			body.WriteString(` let read=seen.clone();
 let mut hooked=ToolDefinition::new("hooked","hooked","Hooked.",schema(),|_,_|ToolResult::text("hooked"));
 hooked.prepare_loadout=Some(Box::new(move|_|Some(ToolLoadoutChanges{descriptions:Some([("hooked".to_string(),format!("Hook saw {}",read.lock().unwrap()))].into()),hidden_declarations:None})));
 e.register_tool(hooked);
 e.tool("other","Other.",schema(),|_,_|ToolResult::text("other"));
 e.tool("extra","Extra.",schema(),|_,_|ToolResult::text("extra"));
`)
		}
		body.WriteString(" let write=seen.clone();\n e.on_event(\"session_start\",move|ctx,_|{\n  let _=&write;\n")
		if opts.late {
			body.WriteString("  ctx.register_tool(ToolDefinition::new(\"late\",\"late\",\"Late.\",schema(),|_,_|ToolResult::text(\"late\"))).unwrap();\n  *write.lock().unwrap()=String::from(\"registered\");\n")
		}
		if opts.active {
			body.WriteString("  ctx.set_active_tools(&[\"hooked\",\"other\"]);\n  *write.lock().unwrap()=ctx.get_active_tools().unwrap().join(\"+\");\n")
		}
		body.WriteString("  None\n });\n e\n}")
		code = body.String()
	default:
		t.Fatalf("no fixture for %s", language)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
