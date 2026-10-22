package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/cli"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

// Pi core/sdk.ts createAgentSession as a Go extension calls it: a child session over the host's model runtime, prompted, observed through subscribe, served a custom tool by the extension, aborted and disposed. The sentinel provider answers from the transcript, so only a session that really ran the extension's tool and returned its result reaches the final text.
//
// The provider is registered on the host's own model runtime, not by the extension: a child that built a registry of its own would not find it (C2c).

const childSentinelProvider = "sentinel"

// childSentinelStream is the sentinel model: a prompt "tool:<x>" makes it call sentinel_tool with {"x": "<x>"}, and the tool's result makes it answer "sentinel-final:<result>"; a prompt "stream:<n>" is answered by n streamed deltas "chunk-000 chunk-001 ..."; any other prompt is answered "sentinel-model:<id>:<prompt>".
func childSentinelStream(model extension.Model, in extension.AIContext, _ extension.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	var prompt, toolResult string
	for _, message := range in.Messages() {
		switch m := message.(type) {
		case ai.UserMessage:
			switch content := m.Content.(type) {
			case ai.UserText:
				prompt = string(content)
			case ai.UserContentBlocks:
				prompt = ai.ContentText(content)
			}
		case ai.ToolResultMessage:
			toolResult = ai.ContentText(m.Content)
		}
	}
	stream := ai.NewAssistantMessageEventStream()
	reply := &ai.AssistantMessage{StopReason: ai.StopReasonStop}
	if count, ok := strings.CutPrefix(prompt, "stream:"); ok && toolResult == "" {
		n, _ := strconv.Atoi(count)
		partial := func(text string) *ai.AssistantMessage {
			return &ai.AssistantMessage{StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}
		}
		_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{StopReason: ai.StopReasonStop}})
		_ = stream.Push(ai.TextStartEvent{ContentIndex: 0, Partial: partial("")})
		var text strings.Builder
		for i := range n {
			delta := fmt.Sprintf("chunk-%03d ", i)
			text.WriteString(delta)
			_ = stream.Push(ai.TextDeltaEvent{ContentIndex: 0, Delta: delta, Partial: partial(text.String())})
		}
		_ = stream.Push(ai.TextEndEvent{ContentIndex: 0, Content: text.String(), Partial: partial(text.String())})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: partial(text.String())})
		return stream
	}
	switch {
	case toolResult != "":
		reply.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "sentinel-final:" + toolResult}}
	case strings.HasPrefix(prompt, "tool:"):
		reply.StopReason = ai.StopReasonToolUse
		reply.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "sentinel_tool", Arguments: ai.JsonObject{"x": strings.TrimPrefix(prompt, "tool:")}}}
	default:
		reply.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "sentinel-model:" + model.ID + ":" + prompt}}
	}
	_ = stream.Push(ai.DoneEvent{Reason: reply.StopReason, Message: reply})
	return stream
}

func newChildSessionHost(t *testing.T) (*subprocess.Host, *coding.AgentSessionServices) {
	t.Helper()
	return newChildSessionHostWith(t, true, nil)
}

// newChildSessionHostWith is the host of the child session rows. sentinel registers the sentinel provider on the host's model runtime; a provider "nokey" with a model and no credentials is always there. wrap, when set, wraps every child session the host builds.
func newChildSessionHostWith(t *testing.T, sentinel bool, wrap func(subprocess.ChildSession) subprocess.ChildSession) (*subprocess.Host, *coding.AgentSessionServices) {
	t.Helper()
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if sentinel {
		if err := services.Registry().RegisterExtensionProvider(childSentinelProvider, extension.ProviderConfig{
			API: ai.APIOpenAICompletions, BaseURL: "https://sentinel.invalid", APIKey: "sentinel-key",
			Models:       []extension.ProviderModelConfig{{ID: "echo", Name: "Echo"}},
			StreamSimple: childSentinelStream,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := services.Registry().RegisterExtensionProvider("nokey", extension.ProviderConfig{
		API: ai.APIOpenAICompletions, BaseURL: "https://nokey.invalid",
		Models: []extension.ProviderModelConfig{{ID: "locked", Name: "Locked"}},
		StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
			panic("a model without credentials is never streamed")
		},
	}); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHostWithConfigRoot(dir, t.TempDir())
	t.Cleanup(func() { host.Shutdown("test complete") })
	host.SetSessionGraph(coding.NewSessionGraph())
	bridge := subprocess.NewUIBridge(func() {})
	host.SetUIBridge(bridge)
	if wrap == nil {
		cli.BindChildSessions(bridge, services)
	} else {
		factory := cli.ChildSessionFactory(services)
		bridge.SetHostAction("createChildSession", subprocess.ChildSessionFactory(func(ctx context.Context, request subprocess.ChildSessionRequest) (subprocess.ChildSession, error) {
			session, err := factory(ctx, request)
			if err != nil {
				return nil, err
			}
			return wrap(session), nil
		}))
	}
	return host, services
}

// TestChildAgentSessionGoPlacements runs the Go fixture isolated and packed with a sibling; the fused realization is TestChildAgentSessionGoFused.
func TestChildAgentSessionGoPlacements(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, services := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := subprocess.ExtConfig{
				Name: childsession.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/child-session-go"), Enabled: true, Isolation: isolation,
				RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
				SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/child-session-go", Package: "example.com/child-session-go",
			}
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				configs = append(configs, providerPeer(t, root, cfg))
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildSession(t, services.CWD(), loaded[0])
		})
	}
}

// childSessionWant is read on first use, not at package init: a test binary that runs as a helper process (TestExecAbortHelperProcess) starts in another directory. It is the observation every SDK must produce: the events in the order the session emitted them (message events carry their role), the transcript's roles, and the text only the extension's tool can have produced. It is Pi's own observation: TestNodeCreateAgentSessionSentinelScenarioMatchesPi runs Pi's createAgentSession through the same scenario and asserts these lists, so the Node row is that test and every other row asserts the same golden.
var childSessionWant = sync.OnceValue(func() map[string]any {
	raw, err := os.ReadFile("testdata/child-session-want.json")
	if err != nil {
		panic(err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		panic(err)
	}
	return want
})

// checkChildSession runs the fixture's child_run tool for a plain prompt and for a prompt that makes the sentinel model call the extension's tool. scratch is the directory of the host the extension was loaded on (checkChildExtensionEvents).
func checkChildSession(t *testing.T, scratch string, ext extension.Extension) {
	t.Helper()
	run := func(prompt string) map[string]any {
		result, err := ext.Tools["child_run"].Definition.Execute(t.Context(), "call", json.RawMessage(`{"prompt":`+quoteJSON(prompt)+`}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		var observed map[string]any
		if err := json.Unmarshal([]byte(result.Text()), &observed); err != nil {
			t.Fatalf("%q: %v", result.Text(), err)
		}
		return observed
	}
	plain := run("hello")
	if plain["final"] != "sentinel-model:echo:hello" || plain["file"] != "" || plain["appendix"] != true || plain["model"] != "echo" {
		t.Fatalf("plain run: %v", plain)
	}
	if !reflect.DeepEqual(plain["events"], childSessionWant()["plain-events"]) {
		t.Fatalf("plain events = %v, want %v", plain["events"], childSessionWant()["plain-events"])
	}
	streamed := run("stream:40")
	wantStreamed := ""
	for i := range 40 {
		wantStreamed += fmt.Sprintf("chunk-%03d ", i)
	}
	if streamed["final"] != strings.TrimSpace(wantStreamed) || streamed["streamed"] != wantStreamed || streamed["partial"] != wantStreamed || streamed["updates"] != childSessionWant()["stream-updates"] {
		t.Fatalf("streamed run: final %q streamed %q partial %q updates %v, want the %d-chunk text and %v updates", streamed["final"], streamed["streamed"], streamed["partial"], streamed["updates"], 40, childSessionWant()["stream-updates"])
	}
	for _, observed := range []map[string]any{plain, streamed} {
		if !reflect.DeepEqual(observed["system"], childSessionWant()["system"]) {
			t.Fatalf("the transcript's system message = %v, want Pi's %v", observed["system"], childSessionWant()["system"])
		}
	}
	tool := run("tool:abc")
	if tool["final"] != "sentinel-final:ABC!" || !reflect.DeepEqual(tool["roles"], childSessionWant()["tool-roles"]) {
		t.Fatalf("the final text did not come from the extension's tool: %v", tool)
	}
	if !reflect.DeepEqual(tool["events"], childSessionWant()["tool-events"]) {
		t.Fatalf("tool events = %v, want %v", tool["events"], childSessionWant()["tool-events"])
	}
	checkChildExtensionEvents(t, scratch, ext)
}

func TestChildAgentSessionGoFused(t *testing.T) {
	host, services := newChildSessionHost(t)
	ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	checkChildSession(t, services.CWD(), *ext)
}

const childSessionPython = `import json
import re
import threading
import time
import pig_sdk


def new_extension():
    ext = pig_sdk.Extension("child-session")

    def child_run(ctx, args):
        seen = []
        update = {"count": 0, "streamed": "", "partial": ""}

        def text_of(message):
            content = (message or {}).get("content") or []
            return content[0].get("text", "") if content else ""

        def observe(event):
            seen.append(event["type"] + (":" + event["message"]["role"] if event["type"].startswith("message_") else ""))
            if event["type"] == "message_update":
                update["count"] += 1
                update["streamed"] = text_of(event["message"])
                update["partial"] = text_of(event["assistantMessageEvent"].get("partial"))

        sentinel = pig_sdk.ToolDefinition(
            name="sentinel_tool", label="sentinel", description="uppercases x",
            parameters={"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]},
            execute=lambda c, params: params["x"].upper() + "!",
        )
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model={"provider": "sentinel", "id": "echo"}, tools=["sentinel_tool"], in_memory=True, settings={"compaction": {"enabled": False}},
            resources=pig_sdk.ResourceOptions(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True, append_system_prompt=["Child appendix"]),
            custom_tools=[sentinel],
        ))
        try:
            session.subscribe(observe)
            session.prompt(args["prompt"])
            messages = session.messages()
            roles = [message["role"] for message in messages]
            first = messages[0] if messages else {}
            system = {"keys": sorted(first), "role": first.get("role"), "content": first.get("content"), "sections": sorted(first.get("sections") or {}), "toolsAdded": first.get("toolsAdded")}
            state = session.state()
            return json.dumps({"events": seen, "updates": update["count"], "streamed": update["streamed"], "partial": update["partial"], "roles": roles, "system": system, "final": state.get("lastAssistantText", ""), "model": state["model"]["id"],
                               "activeTools": state["activeTools"], "file": state.get("sessionFile", ""), "appendix": "Child appendix" in state["systemPrompt"]})
        finally:
            session.dispose()

    ext.register_tool(pig_sdk.ToolDefinition(name="child_run", label="child_run", description="child_run", parameters={"type": "object"}, execute=child_run))

    quiet = dict(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True)

    def lifecycle(ctx, args):
        started = threading.Event()
        saw = {"abort": False}

        def hang(c, params):
            started.set()
            while not c.is_cancelled():
                time.sleep(0.005)
            saw["abort"] = True
            raise RuntimeError("aborted")

        tool = pig_sdk.ToolDefinition(name="sentinel_tool", label="sentinel", description="waits for its abort",
                                      parameters={"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}, execute=hang)
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model={"provider": "sentinel", "id": "echo"}, tools=["sentinel_tool"], in_memory=True, settings={"compaction": {"enabled": False}},
            resources=pig_sdk.ResourceOptions(**quiet), custom_tools=[tool]))
        try:
            events = []
            session.subscribe(lambda e: events.append(e["type"] + (":aborted=" + str(e["aborted"]).lower() if e["type"] == "agent_settled" else "")))
            running = threading.Thread(target=lambda: session.prompt("tool:hang"))
            running.start()
            started.wait(10)
            out = {"concurrentPrompt": "resolved"}
            try:
                session.prompt("again")
            except Exception as error:  # noqa: BLE001
                out["concurrentPrompt"] = str(error)
            session.abort()
            running.join(10)
            messages = session.messages()
            out.update({"toolSawAbort": saw["abort"], "idleAfterAbort": not session.state()["isStreaming"], "tail": events[-4:], "lastRole": messages[-1]["role"]})
            session.dispose()
            out["afterDispose"] = "resolved"
            try:
                session.prompt("after dispose")
            except Exception as error:  # noqa: BLE001
                out["afterDispose"] = str(error)
            return json.dumps(out)
        finally:
            session.dispose()

    def hold(ctx, args):
        started = threading.Event()

        def hang(c, params):
            started.set()
            while not c.is_cancelled():
                time.sleep(0.005)
            raise RuntimeError("aborted")

        tool = pig_sdk.ToolDefinition(name="sentinel_tool", label="sentinel", description="waits for its abort",
                                      parameters={"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}, execute=hang)
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model={"provider": "sentinel", "id": "echo"}, tools=["sentinel_tool"], in_memory=True, resources=pig_sdk.ResourceOptions(**quiet), custom_tools=[tool]))
        threading.Thread(target=lambda: session.prompt("tool:hang"), daemon=True).start()
        started.wait(10)
        return json.dumps({"held": True})

    def errors(ctx, args):
        def norm(message):
            if not message:
                return None
            lines = message.split("\n")
            return {"first": lines[0], "docs": [re.split(r"[\\/]", line.strip())[-1] for line in lines if line.strip().endswith(".md")]}

        out = {}
        for name, model in (("no-key", {"provider": "nokey", "id": "locked"}), ("no-model", None)):
            session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(model=model, in_memory=True, settings={"compaction": {"enabled": False}}, resources=pig_sdk.ResourceOptions(**quiet)))
            prompt_error = ""
            try:
                session.prompt("hi")
            except Exception as error:  # noqa: BLE001
                prompt_error = str(error)
            state = session.state()
            session.dispose()
            out[name] = {"fallback": norm(session.model_fallback_message), "model": (state.get("model") or {}).get("id"), "promptError": norm(prompt_error)}
        return json.dumps(out)

    def events(ctx, args):
        scope = [{"model": {"provider": "sentinel", "id": "echo"}, "thinkingLevel": "high"}, {"model": {"provider": "nokey", "id": "locked"}, "thinkingLevel": "low"}]
        probe = dict(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True)
        options = dict(model={"provider": "sentinel", "id": "echo"}, in_memory=True, settings={"compaction": {"enabled": False}}, scoped_models=scope)
        if args.get("via") == "loader":
            loader = pig_sdk.DefaultResourceLoader(ctx, cwd=args["cwd"], agent_dir=args["agentDir"], additional_extension_paths=[args["probe"]], **probe)
            loader.reload()
            options["resource_loader"] = loader
        else:
            options["resources"] = pig_sdk.ResourceOptions(additional_extension_paths=[args["probe"]], **probe)
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(**options))
        try:
            session.bind_extensions()
            session.prompt("stream:3")
        finally:
            session.dispose()
        return "done"

    for name, fn in (("child_lifecycle", lifecycle), ("child_hold", hold), ("child_errors", errors), ("child_events", events)):
        ext.register_tool(pig_sdk.ToolDefinition(name=name, label=name, description=name, parameters={"type": "object"}, execute=fn))
    return ext
`

func TestChildAgentSessionPython(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, services := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := packedFlagFactory(t, root, "python", "child-session", false)
			cfg.Isolation = isolation
			cfg.ContentHash = "python-child-session-" + fmt.Sprint(len(childSessionPython))
			if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(childSessionPython), 0o600); err != nil {
				t.Fatal(err)
			}
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				peer := packedFlagFactory(t, root, "python", "child-session-peer", false)
				peer.Isolation = isolation
				configs = append(configs, peer)
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildSession(t, services.CWD(), loaded[0])
		})
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const childSessionRust = `use pig_sdk::{CreateAgentSessionOptions, DefaultResourceLoader, DefaultResourceLoaderOptions, Extension, ResourceOptions, ToolDefinition, ToolResult};
use serde_json::{json, Value};
use std::sync::{Arc, Mutex};

pub fn new_extension() -> Extension {
    let mut e = Extension::new("child-session");
    e.register_tool(ToolDefinition::new("child_run", "child_run", "child_run", json!({"type": "object"}), |ctx, args: Value| {
        let seen: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
        let sentinel = ToolDefinition::new(
            "sentinel_tool", "sentinel", "uppercases x",
            json!({"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}),
            |_ctx, params: Value| ToolResult::text(format!("{}!", params["x"].as_str().unwrap_or("").to_uppercase())),
        );
        let session = match ctx.create_agent_session(CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})),
            tools: Some(vec!["sentinel_tool".to_string()]),
            in_memory: true,
            settings: Some(json!({"compaction": {"enabled": false}})),
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, append_system_prompt: Some(vec!["Child appendix".to_string()]), ..Default::default() }),
            custom_tools: vec![sentinel],
            ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let sink = seen.clone();
        let update: Arc<Mutex<(u64, String, String)>> = Arc::new(Mutex::new((0, String::new(), String::new())));
        let update_sink = update.clone();
        let _subscription = session.subscribe(move |event| {
            let mut name = event["type"].as_str().unwrap_or("").to_string();
            if name.starts_with("message_") {
                name = format!("{}:{}", name, event["message"]["role"].as_str().unwrap_or(""));
            }
            if event["type"].as_str() == Some("message_update") {
                let mut guard = update_sink.lock().unwrap();
                guard.0 += 1;
                guard.1 = event["message"]["content"][0]["text"].as_str().unwrap_or("").to_string();
                guard.2 = event["assistantMessageEvent"]["partial"]["content"][0]["text"].as_str().unwrap_or("").to_string();
            }
            sink.lock().unwrap().push(name);
        });
        let outcome = (|| -> std::io::Result<Value> {
            session.prompt(args["prompt"].as_str().unwrap_or(""))?;
            let messages = session.messages()?;
            let roles: Vec<Value> = messages.iter().map(|m| m["role"].clone()).collect();
            let first = messages.first().cloned().unwrap_or(Value::Null);
            let sorted_keys = |value: &Value| -> Vec<String> {
                let mut keys: Vec<String> = value.as_object().map(|members| members.keys().cloned().collect()).unwrap_or_default();
                keys.sort();
                keys
            };
            let system = json!({"keys": sorted_keys(&first), "role": first["role"], "content": first["content"], "sections": sorted_keys(&first["sections"]), "toolsAdded": first["toolsAdded"]});
            let state = session.state()?;
            Ok(json!({
                "events": *seen.lock().unwrap(), "updates": update.lock().unwrap().0, "streamed": update.lock().unwrap().1.clone(), "partial": update.lock().unwrap().2.clone(), "roles": roles, "system": system, "final": state["lastAssistantText"].as_str().unwrap_or(""),
                "model": state["model"]["id"], "activeTools": state["activeTools"], "file": state["sessionFile"].as_str().unwrap_or(""),
                "appendix": state["systemPrompt"].as_str().unwrap_or("").contains("Child appendix"),
            }))
        })();
        session.dispose();
        match outcome {
            Ok(value) => ToolResult::text(value.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    }));

    e.register_tool(ToolDefinition::new("child_lifecycle", "child_lifecycle", "child_lifecycle", json!({"type": "object"}), |ctx, _args: Value| {
        let started = Arc::new((Mutex::new(false), std::sync::Condvar::new()));
        let saw_abort = Arc::new(Mutex::new(false));
        let (started_tool, saw_tool) = (started.clone(), saw_abort.clone());
        let hang = ToolDefinition::new(
            "sentinel_tool", "sentinel", "waits for its abort",
            json!({"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}),
            move |ctx, _params: Value| {
                *started_tool.0.lock().unwrap() = true;
                started_tool.1.notify_all();
                while !ctx.is_cancelled() {
                    std::thread::sleep(std::time::Duration::from_millis(5));
                }
                *saw_tool.lock().unwrap() = true;
                ToolResult::Error("aborted".to_string())
            },
        );
        let session = match ctx.create_agent_session(CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})), tools: Some(vec!["sentinel_tool".to_string()]), in_memory: true,
            settings: Some(json!({"compaction": {"enabled": false}})),
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
            custom_tools: vec![hang], ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let events: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
        let sink = events.clone();
        let _subscription = session.subscribe(move |event| {
            let mut name = event["type"].as_str().unwrap_or("").to_string();
            if name == "agent_settled" {
                name = format!("{}:aborted={}", name, event["aborted"].as_bool().unwrap_or(false));
            }
            sink.lock().unwrap().push(name);
        });
        let outcome = std::thread::scope(|scope| -> std::io::Result<Value> {
            let running = scope.spawn(|| session.prompt("tool:hang"));
            {
                let guard = started.0.lock().unwrap();
                let _ = started.1.wait_timeout_while(guard, std::time::Duration::from_secs(10), |began| !*began).unwrap();
            }
            let concurrent = match session.prompt("again") { Ok(()) => "resolved".to_string(), Err(error) => error.to_string() };
            session.abort()?;
            running.join().unwrap()?;
            let messages = session.messages()?;
            let state = session.state()?;
            let all = events.lock().unwrap().clone();
            let tail: Vec<String> = all[all.len().saturating_sub(4)..].to_vec();
            session.dispose();
            let after_dispose = match session.prompt("after dispose") { Ok(()) => "resolved".to_string(), Err(error) => error.to_string() };
            Ok(json!({"concurrentPrompt": concurrent, "toolSawAbort": *saw_abort.lock().unwrap(), "idleAfterAbort": !state["isStreaming"].as_bool().unwrap_or(true), "tail": tail, "lastRole": messages.last().map(|m| m["role"].clone()).unwrap_or(Value::Null), "afterDispose": after_dispose}))
        });
        session.dispose();
        match outcome { Ok(value) => ToolResult::text(value.to_string()), Err(error) => ToolResult::Error(error.to_string()) }
    }));

    e.register_tool(ToolDefinition::new("child_hold", "child_hold", "child_hold", json!({"type": "object"}), |ctx, _args: Value| {
        let started = Arc::new((Mutex::new(false), std::sync::Condvar::new()));
        let started_tool = started.clone();
        let hang = ToolDefinition::new(
            "sentinel_tool", "sentinel", "waits for its abort",
            json!({"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}),
            move |ctx, _params: Value| {
                *started_tool.0.lock().unwrap() = true;
                started_tool.1.notify_all();
                while !ctx.is_cancelled() {
                    std::thread::sleep(std::time::Duration::from_millis(5));
                }
                ToolResult::Error("aborted".to_string())
            },
        );
        let session = match ctx.create_agent_session(CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})), tools: Some(vec!["sentinel_tool".to_string()]), in_memory: true,
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
            custom_tools: vec![hang], ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        std::thread::spawn(move || { let _ = session.prompt("tool:hang"); });
        let guard = started.0.lock().unwrap();
        let _ = started.1.wait_timeout_while(guard, std::time::Duration::from_secs(10), |began| !*began).unwrap();
        ToolResult::text(json!({"held": true}).to_string())
    }));

    e.register_tool(ToolDefinition::new("child_events", "child_events", "child_events", json!({"type": "object"}), |ctx, args: Value| {
        let probe = args["probe"].as_str().unwrap_or("").to_string();
        let scope = json!([{"model": {"provider": "sentinel", "id": "echo"}, "thinkingLevel": "high"}, {"model": {"provider": "nokey", "id": "locked"}, "thinkingLevel": "low"}]);
        let mut options = CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})), in_memory: true, settings: Some(json!({"compaction": {"enabled": false}})), scoped_models: Some(scope), ..Default::default()
        };
        let loader;
        if args["via"].as_str() == Some("loader") {
            loader = match DefaultResourceLoader::new(ctx, DefaultResourceLoaderOptions {
                cwd: args["cwd"].as_str().unwrap_or("").to_string(), agent_dir: args["agentDir"].as_str().unwrap_or("").to_string(),
                no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, additional_extension_paths: vec![probe], ..Default::default()
            }) {
                Ok(loader) => loader,
                Err(error) => return ToolResult::Error(error.to_string()),
            };
            if let Err(error) = loader.reload(None) {
                return ToolResult::Error(error.to_string());
            }
            options.resource_loader = Some(loader.handle().to_string());
        } else {
            options.resources = Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, additional_extension_paths: Some(vec![probe]), ..Default::default() });
        }
        let session = match ctx.create_agent_session(options) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let outcome = session.bind_extensions().and_then(|()| session.prompt("stream:3"));
        session.dispose();
        match outcome { Ok(()) => ToolResult::text("done".to_string()), Err(error) => ToolResult::Error(error.to_string()) }
    }));

    e.register_tool(ToolDefinition::new("child_errors", "child_errors", "child_errors", json!({"type": "object"}), |ctx, _args: Value| {
        let norm = |message: &str| -> Value {
            if message.is_empty() { return Value::Null; }
            let lines: Vec<&str> = message.split('\n').collect();
            let docs: Vec<String> = lines.iter().map(|line| line.trim()).filter(|line| line.ends_with(".md")).map(|line| line.rsplit(['/', '\\']).next().unwrap_or("").to_string()).collect();
            json!({"first": lines[0], "docs": docs})
        };
        let mut out = serde_json::Map::new();
        for (name, model) in [("no-key", Some(json!({"provider": "nokey", "id": "locked"}))), ("no-model", None)] {
            let session = match ctx.create_agent_session(CreateAgentSessionOptions {
                model, in_memory: true, settings: Some(json!({"compaction": {"enabled": false}})),
                resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
                ..Default::default()
            }) {
                Ok(session) => session,
                Err(error) => return ToolResult::Error(format!("{name}: {error}")),
            };
            let prompt_error = session.prompt("hi").err().map(|error| error.to_string()).unwrap_or_default();
            let state = session.state().unwrap_or(Value::Null);
            let fallback = session.model_fallback_message.clone().unwrap_or_default();
            session.dispose();
            out.insert(name.to_string(), json!({"fallback": norm(&fallback), "model": state["model"]["id"].clone(), "promptError": norm(&prompt_error)}));
        }
        ToolResult::text(Value::Object(out).to_string())
    }));
    e
}
`

func TestChildAgentSessionRust(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, services := newChildSessionHost(t)
			root := findModuleRoot(t)
			cfg := packedFlagFactory(t, root, "rust", "child-session", false)
			cfg.Isolation = isolation
			cfg.ContentHash = "rust-child-session-" + fmt.Sprint(len(childSessionRust))
			if err := os.WriteFile(filepath.Join(cfg.Source, "src", "lib.rs"), []byte(childSessionRust), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("[package]\nname=\"child-session\"\nversion=\"0.0.0\"\nedition=\"2024\"\n[dependencies]\npig-sdk={path=%q}\nserde_json={version=\"1\",features=[\"preserve_order\"]}\n", filepath.ToSlash(filepath.Join(root, "extensions", "sdk-rs")))
			if err := os.WriteFile(filepath.Join(cfg.Source, "Cargo.toml"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			configs := []subprocess.ExtConfig{cfg}
			if isolation == "shared-ok" {
				peer := packedFlagFactory(t, root, "rust", "child-session-peer", false)
				peer.Isolation = isolation
				configs = append(configs, peer)
			}
			loaded, failures := host.LoadAll(t.Context(), configs)
			if len(failures) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %v", failures)
			}
			checkChildSession(t, services.CWD(), loaded[0])
		})
	}
}
