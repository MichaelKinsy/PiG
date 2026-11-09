package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The child_members scenario of the Rust SDK: the Rust port of the Go fixture (testfixture/childsession/members.go) with the same observation keys, checked against childMembersWant by checkChildMembers.
const childMembersRust = `use pig_sdk::{CreateAgentSessionOptions, CustomMessage, ExecuteBashOptions, Extension, ModelMutationOptions, ResourceOptions, SendMessageOptions, SendUserMessageOptions, ToolDefinition, ToolResult};
use serde_json::{json, Value};

pub fn new_extension() -> Extension {
    let mut e = Extension::new("child-session");
    e.register_tool(ToolDefinition::new("child_members", "child_members", "child_members", json!({"type": "object"}), |ctx, args: Value| {
        let dir = args["dir"].as_str().unwrap_or("").to_string();
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
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
            custom_tools: vec![sentinel],
            ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let outcome = (|| -> std::io::Result<Value> {
            let mut out = serde_json::Map::new();
            let none = ModelMutationOptions::default();
            let echo = json!({"provider": "sentinel", "id": "echo"});

            // Reads of a fresh session.
            let id = session.session_id()?;
            out.insert("sessionId".into(), json!(!id.is_empty()));
            out.insert("sessionFile".into(), json!(session.session_file()?.unwrap_or_default()));
            out.insert("model".into(), session.model()?.map_or(Value::Null, |model| model["id"].clone()));
            out.insert("isIdle".into(), json!(session.is_idle()?));
            out.insert("isStreaming".into(), json!(session.is_streaming()?));
            out.insert("isCompacting".into(), json!(session.is_compacting()?));
            out.insert("isRetrying".into(), json!(session.is_retrying()?));
            out.insert("retryAttempt".into(), json!(session.retry_attempt()?));
            out.insert("pendingMessageCount".into(), json!(session.pending_message_count()?));
            out.insert("steeringMode".into(), json!(session.steering_mode()?));
            out.insert("followUpMode".into(), json!(session.follow_up_mode()?));
            out.insert("autoCompactionEnabled".into(), json!(session.auto_compaction_enabled()?));
            out.insert("autoRetryEnabled".into(), json!(session.auto_retry_enabled()?));
            out.insert("sessionName".into(), json!(session.session_name()?.unwrap_or_default()));
            out.insert("scopedModelsStart".into(), json!(session.scoped_models()?.len()));
            out.insert("promptTemplates".into(), json!(session.prompt_templates()?.len()));
            out.insert("hasHandlers".into(), json!(session.has_extension_handlers("agent_start")?));
            out.insert("lastTextStart".into(), json!(session.get_last_assistant_text()?.is_none()));
            out.insert("systemPromptLen".into(), json!(!session.system_prompt()?.is_empty()));

            // Changes read back: each value is one no default holds.
            session.set_steering_mode("all")?;
            session.set_follow_up_mode("all")?;
            session.set_auto_compaction_enabled(true)?;
            session.set_auto_retry_enabled(false)?;
            session.set_session_name("named-by-members")?;
            session.set_scoped_models(&[json!({"model": echo, "thinkingLevel": "medium"})])?;
            out.insert("steeringModeSet".into(), json!(session.steering_mode()?));
            out.insert("followUpModeSet".into(), json!(session.follow_up_mode()?));
            out.insert("autoCompactionSet".into(), json!(session.auto_compaction_enabled()?));
            out.insert("autoRetrySet".into(), json!(session.auto_retry_enabled()?));
            out.insert("sessionNameSet".into(), json!(session.session_name()?.unwrap_or_default()));
            let scoped = session.scoped_models()?;
            if scoped.len() == 1 {
                out.insert("scopedModelsSet".into(), json!(format!("{}:{}", scoped[0]["model"]["id"].as_str().unwrap_or(""), scoped[0]["thinkingLevel"].as_str().unwrap_or(""))));
            }
            out.insert("cycleOnlyModel".into(), json!(session.cycle_model("forward", none)?.is_none()));
            session.set_active_tools_by_name::<&str>(&[])?;
            out.insert("activeToolsEmpty".into(), json!(session.get_active_tool_names()?));
            session.set_active_tools_by_name(&["sentinel_tool"])?;
            out.insert("activeTools".into(), json!(session.get_active_tool_names()?));
            let tools: Vec<String> = session.get_all_tools()?.into_iter().map(|tool| tool.name).collect();
            out.insert("allTools".into(), json!(tools));
            if let Some(definition) = session.get_tool_definition("sentinel_tool")? {
                out.insert("toolDefinition".into(), json!(definition.description));
            }
            out.insert("toolDefinitionMissing".into(), json!(session.get_tool_definition("no_such_tool")?.is_none()));
            out.insert("setModelRefused".into(), json!(session.set_model(&json!({"provider": "nokey", "id": "locked"}), none).err().map(|error| error.to_string()).unwrap_or_default()));
            session.set_model(&echo, none)?;
            session.set_thinking_level("off", none)?;
            out.insert("thinkingLevel".into(), json!(session.thinking_level()?));
            out.insert("availableThinkingLevels".into(), json!(session.get_available_thinking_levels()?));
            session.set_cache_warming_mode("off")?;

            // A run, observed through the members.
            session.prompt("hello")?;
            if let Some(text) = session.get_last_assistant_text()? {
                out.insert("lastText".into(), json!(text));
            }
            let stats = session.get_session_stats()?;
            out.insert("stats".into(), json!({"user": stats["userMessages"], "assistant": stats["assistantMessages"], "total": stats["totalMessages"], "idMatches": stats["sessionId"].as_str() == Some(id.as_str())}));
            let forks = session.get_user_messages_for_forking()?;
            if forks.len() == 1 {
                out.insert("fork".into(), json!(forks[0].text));
            }

            // Awaited members: a user message that starts a run, the wait for it, a custom message, bash, compaction, tree navigation, exports, the bug report.
            session.send_user_message(json!("via-send"), &SendUserMessageOptions::default())?;
            session.wait_for_idle()?;
            if let Some(text) = session.get_last_assistant_text()? {
                out.insert("sentText".into(), json!(text));
            }
            session.send_custom_message(&CustomMessage { custom_type: "member-note".into(), content: json!("noted"), display: true, details: None }, &SendMessageOptions::default())?;
            let bash = session.execute_bash("echo member-bash", ExecuteBashOptions::default())?;
            out.insert("bash".into(), json!({"output": bash["output"], "exit": bash["exitCode"] == json!(0)}));
            session.record_bash_result("recorded-cmd", &json!({"output": "recorded-out", "exitCode": 0, "cancelled": false, "truncated": false}), ExecuteBashOptions::default())?;
            out.insert("bashRunning".into(), json!(session.is_bash_running()?));
            let roles: Vec<Value> = session.messages()?.iter().map(|message| message["role"].clone()).collect();
            out.insert("roles".into(), json!(roles));
            out.insert("compactRefused".into(), json!(session.compact(None).err().map(|error| error.to_string()).unwrap_or_default()));
            if forks.len() == 1 {
                let nav = session.navigate_tree(&forks[0].entry_id, Value::Null)?;
                out.insert("navigate".into(), json!({"cancelled": nav["cancelled"], "editorText": nav["editorText"].as_str().unwrap_or("")}));
            }
            let jsonl = std::path::Path::new(&dir).join("export.jsonl").to_string_lossy().into_owned();
            let path = session.export_to_jsonl(Some(&jsonl))?;
            out.insert("exportedJsonl".into(), json!(path == jsonl));
            if let Ok(data) = std::fs::read_to_string(&jsonl) {
                out.insert("jsonlHasSession".into(), json!(data.contains(&id)));
            }
            let report = session.summarize_for_bug_report(Some("member-hint"))?;
            out.insert("bugReport".into(), json!(report.starts_with("sentinel-model:echo:")));
            let cleared = session.clear_queue()?;
            out.insert("cleared".into(), json!([cleared.steering.len(), cleared.follow_up.len()]));
            out.insert("callableTools".into(), json!(session.get_callable_tool_names()?));
            out.insert("steeringMessages".into(), json!(session.get_steering_messages()?.len()));
            out.insert("followUpMessages".into(), json!(session.get_follow_up_messages()?.len()));
            out.insert("routedModel".into(), json!(session.routed_model()?.is_none()));
            out.insert("pendingBash".into(), json!(session.has_pending_bash_messages()?));
            out.insert("supportsThinking".into(), json!(session.supports_thinking()?));
            out.insert("cycledThinking".into(), json!(session.cycle_thinking_level(none)?.unwrap_or_default()));
            if let Some(status) = session.cache_warming_status()? {
                out.insert("cacheWarming".into(), status["state"].clone());
            }
            let html = std::path::Path::new(&dir).join("export.html").to_string_lossy().into_owned();
            out.insert("exportHtmlInMemory".into(), json!(session.export_to_html(Some(&html), None).err().map(|error| error.to_string()).unwrap_or_default()));
            session.reload()?;
            out.insert("afterReload".into(), json!(session.session_id()? == id));
            // A file-backed session: its sessionFile is a real path (the in-memory one above is empty, so a reader that returned the fallback would still pass there).
            let persisted = ctx.create_agent_session(CreateAgentSessionOptions {
                cwd: Some(dir.clone()),
                agent_dir: Some(std::path::Path::new(&dir).join("agent").to_string_lossy().into_owned()),
                model: Some(echo.clone()),
                tools: Some(vec![]),
                settings: Some(json!({"compaction": {"enabled": false}})),
                resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
                ..Default::default()
            })?;
            let persisted_file = persisted.session_file();
            let persisted_id = persisted.session_id();
            persisted.dispose();
            let (persisted_file, persisted_id) = (persisted_file?.unwrap_or_default(), persisted_id?);
            out.insert("fileBackedSessionFile".into(), json!(persisted_file.starts_with(&dir) && persisted_file.ends_with(".jsonl") && persisted_file.contains(&persisted_id)));
            session.abort_bash()?;
            session.abort_branch_summary()?;
            session.abort_compaction()?;
            session.abort_retry()?;
            Ok(Value::Object(out))
        })();
        session.dispose();
        match outcome {
            Ok(value) => ToolResult::text(value.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    }));

    // An awaited member must not hold back a call made from another thread: execute_bash runs on one thread, abort_bash is called on another, and the run comes back cancelled long before the command would have ended. A member sent over the synchronous childSession.op instead of childSession.opAsync queues the abort behind the command.
    e.register_tool(ToolDefinition::new("child_abort_bash", "child_abort_bash", "child_abort_bash", json!({"type": "object"}), |ctx, _args: Value| {
        let session = match ctx.create_agent_session(CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})),
            in_memory: true,
            settings: Some(json!({"compaction": {"enabled": false}})),
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
            ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let started = std::time::Instant::now();
        let outcome = std::thread::scope(|scope| -> std::io::Result<Value> {
            let running = scope.spawn(|| session.execute_bash("sleep 20", ExecuteBashOptions::default()));
            let mut seen = false;
            while !running.is_finished() && started.elapsed() < std::time::Duration::from_secs(5) {
                if session.is_bash_running()? {
                    seen = true;
                    break;
                }
                std::thread::sleep(std::time::Duration::from_millis(20));
            }
            session.abort_bash()?;
            let result = running.join().map_err(|_| std::io::Error::other("execute_bash thread panicked"))??;
            Ok(json!({"running": seen, "cancelled": result["cancelled"], "fast": started.elapsed() < std::time::Duration::from_secs(10)}))
        });
        session.dispose();
        match outcome {
            Ok(value) => ToolResult::text(value.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    }));

    // args.first names the awaited member to start on one thread; the test holds it in the host, and abort_bash is called from this thread 0.4 s later. It must not wait behind the member.
    e.register_tool(ToolDefinition::new("child_order_probe", "child_order_probe", "child_order_probe", json!({"type": "object"}), |ctx, args: Value| {
        let session = match ctx.create_agent_session(CreateAgentSessionOptions {
            model: Some(json!({"provider": "sentinel", "id": "echo"})),
            in_memory: true,
            settings: Some(json!({"compaction": {"enabled": false}})),
            resources: Some(ResourceOptions { no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default() }),
            ..Default::default()
        }) {
            Ok(session) => session,
            Err(error) => return ToolResult::Error(error.to_string()),
        };
        let first = args["first"].as_str().unwrap_or("").to_string();
        let outcome = std::thread::scope(|scope| -> std::io::Result<Value> {
            let running = scope.spawn(|| match first.as_str() {
                "cycleModel" => session.cycle_model("forward", ModelMutationOptions::default()).map(|_| ()),
                "executeBash" => session.execute_bash("echo x", ExecuteBashOptions::default()).map(|_| ()),
                "setModel" => session.set_model(&json!({"provider": "sentinel", "id": "echo"}), ModelMutationOptions::default()),
                _ => Ok(()),
            });
            std::thread::sleep(std::time::Duration::from_millis(400));
            let started = std::time::Instant::now();
            session.abort_bash()?;
            let took = started.elapsed().as_secs_f64();
            running.join().map_err(|_| std::io::Error::other("member thread panicked"))??;
            Ok(json!({"abortTook": took}))
        });
        session.dispose();
        match outcome {
            Ok(value) => ToolResult::text(value.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    }));
    e
}
`

// TestChildAgentSessionMembersRust runs the Rust child_members scenario packed, isolated and with a sibling.
func TestChildAgentSessionMembersRust(t *testing.T) {
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			checkChildMembers(t, loadChildMembersRust(t, isolation))
		})
	}
}

// TestChildAgentSessionMembersRustAbortBash is the concurrency row: execute_bash("sleep 20") blocks one thread, abort_bash is called from another, and the command comes back cancelled within 10 s. It fails for an async member sent over childSession.op (the sync call), which the golden of child_members cannot see because the host still answers correctly.
func TestChildAgentSessionMembersRustAbortBash(t *testing.T) {
	ext := loadChildMembersRust(t, "strict")
	result, err := ext.Tools["child_abort_bash"].Definition.Execute(t.Context(), "call", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
		t.Fatalf("%q: %v", result.Text(), err)
	}
	want := map[string]any{"running": true, "cancelled": true, "fast": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("abort_bash during execute_bash = %v, want %v", got, want)
	}
}

// heldChildMember is a child session whose host holds one member until released, as a member awaiting a slow auth check or model_select handler.
type heldChildMember struct {
	subprocess.ChildSession
	member  string
	release chan struct{}
}

func (h heldChildMember) Op(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	if op == h.member {
		select {
		case <-h.release:
		case <-time.After(10 * time.Second):
		}
		return nil, nil
	}
	return h.ChildSession.Op(ctx, op, args)
}

// TestChildAgentSessionMembersRustAbortNotHeld: with one awaited member held in the host for 3 s, an abort_bash called 0.4 s later from another thread returns at once. A member sent over the synchronous childSession.op holds it.
func TestChildAgentSessionMembersRustAbortNotHeld(t *testing.T) {
	for _, member := range []string{"cycleModel", "executeBash", "setModel"} {
		t.Run(member, func(t *testing.T) {
			release := make(chan struct{})
			ext := loadChildMembersRustWith(t, "strict", func(s subprocess.ChildSession) subprocess.ChildSession { return heldChildMember{s, member, release} })
			time.AfterFunc(3*time.Second, func() { close(release) })
			result, err := ext.Tools["child_order_probe"].Definition.Execute(t.Context(), "call", json.RawMessage(`{"first":"`+member+`"}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(result.Text()), &got); err != nil {
				t.Fatalf("%q: %v", result.Text(), err)
			}
			if took, ok := got["abortTook"].(float64); !ok || took > 1.5 {
				t.Fatalf("abort_bash took %v s behind the held %s", got["abortTook"], member)
			}
		})
	}
}

func loadChildMembersRust(t *testing.T, isolation string) extension.Extension {
	t.Helper()
	return loadChildMembersRustWith(t, isolation, nil)
}

func loadChildMembersRustWith(t *testing.T, isolation string, wrap func(subprocess.ChildSession) subprocess.ChildSession) extension.Extension {
	t.Helper()
	host, _ := newChildSessionHostWith(t, true, wrap)
	root := findModuleRoot(t)
	cfg := packedFlagFactory(t, root, "rust", "child-session", false)
	cfg.Isolation = isolation
	cfg.ContentHash = "rust-child-members-" + fmt.Sprint(len(childMembersRust))
	if err := os.WriteFile(filepath.Join(cfg.Source, "src", "lib.rs"), []byte(childMembersRust), 0o600); err != nil {
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
	return loaded[0]
}
