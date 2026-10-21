package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// childSentinelStream is the sentinel model: a prompt "tool:<x>" makes it call sentinel_tool with {"x": "<x>"}, and the tool's result makes it answer "sentinel-final:<result>"; any other prompt is answered "sentinel-model:<id>:<prompt>".
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
	dir := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := services.Registry().RegisterExtensionProvider(childSentinelProvider, extension.ProviderConfig{
		API: ai.APIOpenAICompletions, BaseURL: "https://sentinel.invalid", APIKey: "sentinel-key",
		Models: []extension.ProviderModelConfig{{ID: "echo", Name: "Echo"}},
		StreamSimple: func(model extension.Model, in extension.AIContext, options extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
			return childSentinelStream(model, in, options)
		},
	}); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHostWithConfigRoot(dir, t.TempDir())
	t.Cleanup(func() { host.Shutdown("test complete") })
	bridge := subprocess.NewUIBridge(func() {})
	host.SetUIBridge(bridge)
	cli.BindChildSessions(bridge, services)
	return host, services
}

// TestChildAgentSessionGoPlacements runs the Go fixture isolated and packed with a sibling; the fused realization is TestChildAgentSessionGoFused.
func TestChildAgentSessionGoPlacements(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
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
			checkChildSession(t, loaded[0])
		})
	}
}

// childSessionWant is the observation every SDK must produce: the events in the order the session emitted them (message events carry their role), the transcript's roles, and the text only the extension's tool can have produced. The Go host's golden is what PiG's Session emits; the Node row (Pi's own createAgentSession) must produce the same lists, which is the Pi evidence (see the PR body).
var childSessionWant = map[string]any{
	"plain-events": []any{"agent_start", "turn_start", "message_start:system", "message_end:system", "message_start:user", "message_end:user", "message_start:assistant", "message_end:assistant", "turn_end", "agent_end", "agent_settled"},
	"tool-events": []any{"agent_start", "turn_start", "message_start:system", "message_end:system", "message_start:user", "message_end:user", "message_start:assistant", "message_end:assistant", "tool_execution_start", "tool_execution_end",
		"message_start:toolResult", "message_end:toolResult", "turn_end", "turn_start", "message_start:assistant", "message_end:assistant", "turn_end", "agent_end", "agent_settled"},
}

// checkChildSession runs the fixture's child_run tool for a plain prompt and for a prompt that makes the sentinel model call the extension's tool.
func checkChildSession(t *testing.T, ext extension.Extension) {
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
	if !reflect.DeepEqual(plain["events"], childSessionWant["plain-events"]) {
		t.Fatalf("plain events = %v, want %v", plain["events"], childSessionWant["plain-events"])
	}
	tool := run("tool:abc")
	if tool["final"] != "sentinel-final:ABC!" || !reflect.DeepEqual(tool["roles"], []any{"system", "user", "assistant", "toolResult", "assistant"}) {
		t.Fatalf("the final text did not come from the extension's tool: %v", tool)
	}
	if !reflect.DeepEqual(tool["events"], childSessionWant["tool-events"]) {
		t.Fatalf("tool events = %v, want %v", tool["events"], childSessionWant["tool-events"])
	}
}

func TestChildAgentSessionGoFused(t *testing.T) {
	host, _ := newChildSessionHost(t)
	ext, err := host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: childsession.Name, Path: "/ext/child-session.go", Enabled: true}, childsession.Extension().RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	checkChildSession(t, *ext)
}

const childSessionPython = `import json
import pig_sdk


def new_extension():
    ext = pig_sdk.Extension("child-session")

    def child_run(ctx, args):
        seen = []
        sentinel = pig_sdk.ToolDefinition(
            name="sentinel_tool", label="sentinel", description="uppercases x",
            parameters={"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]},
            execute=lambda c, params: params["x"].upper() + "!",
        )
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(
            model="sentinel/echo", tools=["sentinel_tool"], in_memory=True, settings={"compaction": {"enabled": False}},
            resources=pig_sdk.ResourceOptions(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True, append_system_prompt=["Child appendix"]),
            custom_tools=[sentinel],
        ))
        try:
            session.subscribe(lambda event: seen.append(event["type"] + (":" + event["message"]["role"] if event["type"].startswith("message_") else "")))
            session.prompt(args["prompt"])
            roles = [message["role"] for message in session.messages()]
            state = session.state()
            return json.dumps({"events": seen, "roles": roles, "final": state.get("lastAssistantText", ""), "model": state["model"]["id"],
                               "activeTools": state["activeTools"], "file": state.get("sessionFile", ""), "appendix": "Child appendix" in state["systemPrompt"]})
        finally:
            session.dispose()

    ext.register_tool(pig_sdk.ToolDefinition(name="child_run", label="child_run", description="child_run", parameters={"type": "object"}, execute=child_run))
    return ext
`

func TestChildAgentSessionPython(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
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
			checkChildSession(t, loaded[0])
		})
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const childSessionRust = `use pig_sdk::{CreateAgentSessionOptions, Extension, ResourceOptions, ToolDefinition, ToolResult};
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
            model: Some(json!("sentinel/echo")),
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
        let _subscription = session.subscribe(move |event| {
            let mut name = event["type"].as_str().unwrap_or("").to_string();
            if name.starts_with("message_") {
                name = format!("{}:{}", name, event["message"]["role"].as_str().unwrap_or(""));
            }
            sink.lock().unwrap().push(name);
        });
        let outcome = (|| -> std::io::Result<Value> {
            session.prompt(args["prompt"].as_str().unwrap_or(""))?;
            let roles: Vec<Value> = session.messages()?.iter().map(|m| m["role"].clone()).collect();
            let state = session.state()?;
            Ok(json!({
                "events": *seen.lock().unwrap(), "roles": roles, "final": state["lastAssistantText"].as_str().unwrap_or(""),
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
    e
}
`

func TestChildAgentSessionRust(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			host, _ := newChildSessionHost(t)
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
			checkChildSession(t, loaded[0])
		})
	}
}
