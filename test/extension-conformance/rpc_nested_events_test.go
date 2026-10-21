package extensionconformance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi runs an event's handlers inside the extension API call that emits it: pi.sendMessage with triggerTurn calls
// _startRun, whose preparation calls the before_agent_start handler before its first await (PR #10739, agent-session.ts
// sendCustomMessage), and pi.setSessionName runs the session_info_changed handlers with `void runner.emit` (agent-session.ts
// setSessionName). The RPC host answers the extension's call only after the event's handler reaches its first suspension,
// and an extension that appended an entry before naming the session still has that call's answer in flight. Each SDK, in
// each placement the host gives it, must let the nested handler run and report back, or the command never answers: Node's
// runtime deadlocked on both before 773a868c84.
//
// The order is Pi's in every SDK. The nested handler's UI effects land before the command's response, because Pi applies
// them synchronously (rpc-mode.ts createExtensionUIContext). A command that returns after its send answers before the run
// emits agent_start, because Pi writes the response two microtasks after the handler returns and agent_start follows the
// run's preparation by more awaits (agent-session.ts:1978-1981, 2129, 2293; rpc-mode.ts:403-406); a command that awaits after
// its send lets the run start and settle first. A command that sends a user message and then awaits waitForIdle finds the
// Session idle and answers before agent_start, whatever the SDK; a command that starts a new Session is the only record of
// its output, because the message its session_start handler sends is emitted before Pi's writer subscribes.
//
// The row drives the pig command line in RPC mode with each SDK's extension: Node in the shared Node cell, Go, Python and
// Rust as factories packed into a runtime cell beside a peer of the same language and as standalone executables in their own
// process, and Go fused into the binary as a Piglet Binary links it.
func TestRPCNestedEventsCompleteInEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("builds pig and every SDK's extension")
	}
	for _, tc := range []struct{ sdk, placement string }{
		{"node", "packed"},
		{"go", "packed"}, {"go", "isolated"}, {"go", "fused"},
		{"python", "packed"}, {"python", "isolated"},
		{"rust", "packed"}, {"rust", "isolated"},
	} {
		t.Run(tc.sdk+"/"+tc.placement, func(t *testing.T) {
			t.Parallel()
			pig, err := nestedEventsPig()
			if err != nil {
				t.Fatal(err)
			}
			label := tc.sdk + "/" + tc.placement
			args := []string{"--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-context-files", "--no-session", "--system-prompt", "P.", "--model", "test-faux/faux-1"}
			for _, path := range nestedEventsExtensions(t, tc.sdk, tc.placement) {
				args = append(args, "-e", path)
			}
			home := t.TempDir()
			p := startNestedEventsRPC(t, label, pig, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, args)

			// A cold load builds the Go and Rust extensions, so only the command listing waits for a build.
			p.send(`{"id":"commands","type":"get_commands"}`)
			p.await("its commands after loading", 10*time.Minute, func(record map[string]any) bool {
				if record["type"] != "response" || record["id"] != "commands" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				commands, _ := data["commands"].([]any)
				return slices.ContainsFunc(commands, func(value any) bool {
					command, _ := value.(map[string]any)
					return command["name"] == "nested-trigger"
				})
			})

			p.send(`{"id":"trigger","type":"prompt","message":"/nested-trigger"}`)
			var trigger []string
			p.await("/nested-trigger's run to settle", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					trigger = append(trigger, tag)
				}
				p.progress = trigger
				return record["type"] == "agent_settled"
			})
			if want := []string{
				"ui:setStatus", "response:handled", "agent_start", "turn_start",
				"message_start:system", "message_end:system", "message_start:custom", "message_end:custom",
				"message_start:assistant", "message_end:assistant", "turn_end", "agent_end", "agent_settled",
			}; !slices.Equal(trigger, want) {
				t.Fatalf("%s: /nested-trigger records\n got: %q\nwant: %q", label, trigger, want)
			}

			p.send(`{"id":"name","type":"prompt","message":"/nested-name"}`)
			var name []string
			p.await("/nested-name to answer", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					name = append(name, tag)
				}
				p.progress = name
				return record["type"] == "response" && record["id"] == "name"
			})
			if want := []string{"entry_appended:nested-entry", "session_info_changed:nested-name", "ui:notify:name:nested-name", "response:handled"}; !slices.Equal(name, want) {
				t.Fatalf("%s: /nested-name records\n got: %q\nwant: %q", label, name, want)
			}

			// A command that awaits after its send lets the run start, as Pi's handler suspends at the await: the run settles
			// before waitForIdle returns, and the command answers after it.
			p.send(`{"id":"wait","type":"prompt","message":"/nested-wait"}`)
			var wait []string
			answered, settled := false, false
			p.await("/nested-wait to answer after its run settles", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					wait = append(wait, tag)
				}
				p.progress = wait
				answered = answered || record["type"] == "response" && record["id"] == "wait"
				settled = settled || record["type"] == "agent_settled"
				return answered && settled
			})
			if !slices.Contains(wait, "ui:setStatus") || slices.Index(wait, "ui:setStatus") > slices.Index(wait, "agent_start") ||
				slices.Index(wait, "agent_end") > slices.Index(wait, "ui:notify:idle") || slices.Index(wait, "ui:notify:idle") > slices.Index(wait, "response:handled") {
				t.Fatalf("%s: /nested-wait records %q: want setStatus, then the run through agent_end, then the idle notification, then the response", label, wait)
			}
			// pi.sendUserMessage returns before the run it starts has begun: prompt() awaits the input handlers, the compaction
			// check and before_agent_start before _startRun marks the Session busy (agent-session.ts:2064-2140). A command that then
			// awaits waitForIdle finds the Session idle (agent-session.ts:2476-2481), returns, and answers before agent_start.
			p.send(`{"id":"send","type":"prompt","message":"/nested-send-wait"}`)
			var send []string
			sendAnswered, sendSettled := false, false
			p.await("/nested-send-wait to answer and its run to settle", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					send = append(send, tag)
				}
				p.progress = send
				sendAnswered = sendAnswered || record["type"] == "response" && record["id"] == "send"
				sendSettled = sendSettled || record["type"] == "agent_settled"
				return sendAnswered && sendSettled
			})
			if answer, start := slices.Index(send, "response:handled"), slices.Index(send, "agent_start"); answer < 0 || start < 0 || answer > start {
				t.Fatalf("%s: /nested-send-wait records %q: want the response before agent_start", label, send)
			}

			// A command that starts a new Session answers with the Session's own output after it: rpc-mode.ts rebindSession
			// subscribes its stdout writer after `await session.bindExtensions()`, which runs the session_start handlers, so the
			// message the handler sends is written nowhere, and the response is the command's only record.
			p.send(`{"id":"fresh","type":"prompt","message":"/nested-fresh"}`)
			var fresh []string
			p.await("/nested-fresh to answer", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					fresh = append(fresh, tag)
				}
				p.progress = fresh
				return record["type"] == "response" && record["id"] == "fresh"
			})
			p.send(`{"id":"barrier","type":"get_state"}`)
			p.await("the state after /nested-fresh", testbudget.Wait(t), func(record map[string]any) bool {
				if tag := nestedEventsTag(record); tag != "" {
					fresh = append(fresh, tag)
				}
				p.progress = fresh
				return record["type"] == "response" && record["id"] == "barrier"
			})
			if want := []string{"response:handled", "response:<nil>"}; !slices.Equal(fresh, want) {
				t.Fatalf("%s: /nested-fresh records\n got: %q\nwant: %q", label, fresh, want)
			}
			p.closeAndWait()
		})
	}
}

// nestedEventsTag names a record by what places it in the order; message_update and ordinary RPC housekeeping are dropped.
func nestedEventsTag(record map[string]any) string {
	switch record["type"] {
	case "response":
		data, _ := record["data"].(map[string]any)
		return fmt.Sprintf("response:%v", data["disposition"])
	case "extension_ui_request":
		if record["method"] == "notify" {
			return fmt.Sprintf("ui:notify:%v", record["message"])
		}
		return fmt.Sprintf("ui:%v", record["method"])
	case "message_start", "message_end":
		message, _ := record["message"].(map[string]any)
		return fmt.Sprintf("%v:%v", record["type"], message["role"])
	case "entry_appended":
		entry, _ := record["entry"].(map[string]any)
		return fmt.Sprintf("entry_appended:%v", entry["customType"])
	case "session_info_changed":
		return fmt.Sprintf("session_info_changed:%v", record["name"])
	case "message_update":
		return ""
	}
	return fmt.Sprint(record["type"])
}

// nestedEventsPig is pig with the row's Go extension fused in under the name nested-go-fused; every other extension loads as
// it does in stock pig.
var nestedEventsPig = sync.OnceValues(func() (string, error) {
	return buildNestedEventsCommand("nested-events-pig", "pig", 10*time.Minute)
})

// nestedEventsGoStandalone is the row's Go extension as a standalone executable.
var nestedEventsGoStandalone = sync.OnceValues(func() (string, error) {
	return buildNestedEventsCommand("nested-go-isolated", "cmd", 5*time.Minute)
})

func buildNestedEventsCommand(name, dir string, timeout time.Duration) (string, error) {
	bin := filepath.Join(fixtureRoot, "nested-events", testExecutableName(name))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(fixtureSourceRoot, "test", "extension-conformance", "testfixture", "nestedevents", dir)
	// Root-module fixtures require the workspace's local SDK, not the published release. Workspace mode rejects -mod=mod.
	flags := strings.Join(slices.DeleteFunc(strings.Fields(os.Getenv("GOFLAGS")), func(f string) bool { return f == "-mod=mod" }), " ")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK="+filepath.Join(fixtureSourceRoot, "go.work"), "GOFLAGS="+flags)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w\n%s", name, err, out)
	}
	return bin, nil
}

// nestedEventsExtensions writes the SDK's extension for the placement and returns the paths pig loads with -e. A packed
// placement adds a peer factory of the same language, so the extension shares its runtime cell.
func nestedEventsExtensions(t *testing.T, sdk, placement string) []string {
	t.Helper()
	dir := t.TempDir()
	write := func(path, content string, mode os.FileMode) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	switch sdk + "/" + placement {
	case "node/packed":
		return []string{
			write(filepath.Join(dir, "nested-node.mjs"), nestedEventsNode, 0o600),
			write(filepath.Join(dir, "nested-node-peer.mjs"), "export default function (pi) {}\n", 0o600),
		}
	case "go/packed", "go/fused":
		name := "nested-go-" + placement
		paths := []string{nestedEventsGoFactory(t, write, filepath.Join(dir, name), name, nestedEventsGoBody)}
		if placement == "packed" {
			paths = append(paths, nestedEventsGoFactory(t, write, filepath.Join(dir, "nested-go-peer"), "nested-go-peer", ""))
		}
		return paths
	case "go/isolated":
		bin, err := nestedEventsGoStandalone()
		if err != nil {
			t.Fatal(err)
		}
		return []string{bin}
	case "python/packed":
		return []string{
			filepath.Dir(write(filepath.Join(dir, "nested-python-packed", "nested_python.py"), "import pig_sdk\n\ndef new_extension() -> pig_sdk.Extension:\n"+nestedEventsPythonBody("nested-python-packed")+"    return ext\n", 0o600)),
			filepath.Dir(write(filepath.Join(dir, "nested-python-peer", "nested_peer.py"), "import pig_sdk\n\ndef new_extension() -> pig_sdk.Extension:\n    return pig_sdk.Extension(\"nested-python-peer\")\n", 0o600)),
		}
	case "python/isolated":
		sdkRoot := filepath.Join(fixtureSourceRoot, "extensions", "sdk-py")
		source := fmt.Sprintf("#!/usr/bin/env python3\nimport sys\nsys.path.insert(0, %q)\nimport pig_sdk\n\ndef build():\n%s    return ext\n\nif __name__ == \"__main__\":\n    build().run()\n", sdkRoot, nestedEventsPythonBody("nested-python-isolated"))
		return []string{filepath.Dir(write(filepath.Join(dir, "nested-python-isolated", "main.py"), source, 0o755))}
	case "rust/packed":
		return []string{
			nestedEventsRustCrate(t, write, filepath.Join(dir, "nested-rust-packed"), "nested-rust-packed", `pig-sdk = "0.1.0"`, "lib.rs", "pub fn new_extension() -> Extension {\n"+nestedEventsRustBody("nested-rust-packed")+"    ext\n}\n"),
			nestedEventsRustCrate(t, write, filepath.Join(dir, "nested-rust-peer"), "nested-rust-peer", `pig-sdk = "0.1.0"`, "lib.rs", "pub fn new_extension() -> Extension {\n    Extension::new(\"nested-rust-peer\")\n}\n"),
		}
	case "rust/isolated":
		dependency := fmt.Sprintf("pig-sdk = { path = %q }", filepath.ToSlash(filepath.Join(fixtureSourceRoot, "extensions", "sdk-rs")))
		return []string{nestedEventsRustCrate(t, write, filepath.Join(dir, "nested-rust-isolated"), "nested-rust-isolated", dependency, "main.rs", "fn build() -> Extension {\n"+nestedEventsRustBody("nested-rust-isolated")+"    ext\n}\n\nfn main() {\n    build().run().expect(\"nested-rust-isolated\");\n}\n")}
	}
	t.Fatalf("no %s extension for placement %s", sdk, placement)
	return nil
}

const nestedEventsNode = `export default function (pi) {
  pi.on("before_agent_start", (_event, ctx) => { ctx.ui.setStatus("nested", "busy"); });
  pi.on("session_info_changed", (event, ctx) => { ctx.ui.notify("name:" + event.name, "info"); });
  pi.registerCommand("nested-trigger", { description: "Send a triggering message", handler: async () => {
    pi.sendMessage({ customType: "nested", content: "Say hello", display: true }, { triggerTurn: true, deliverAs: "followUp" });
  } });
  pi.registerCommand("nested-name", { description: "Append an entry, then name the session", handler: async () => {
    pi.appendEntry("nested-entry", { value: "hello" });
    pi.setSessionName("nested-name");
  } });
  pi.registerCommand("nested-wait", { description: "Send a triggering message, then wait for the run", handler: async (_args, ctx) => {
    pi.sendMessage({ customType: "nested", content: "Say hello", display: true }, { triggerTurn: true, deliverAs: "followUp" });
    await ctx.waitForIdle();
    ctx.ui.notify("idle", "info");
  } });
  pi.on("session_start", async (event) => {
    if (event.reason === "new") pi.sendMessage({ customType: "nested", content: "started", display: true });
  });
  pi.registerCommand("nested-send-wait", { description: "Send a user message, then wait for idle", handler: async (_args, ctx) => {
    pi.sendUserMessage("Say hello");
    await ctx.waitForIdle();
  } });
  pi.registerCommand("nested-fresh", { description: "Start a new Session", handler: async (_args, ctx) => {
    await ctx.newSession();
  } });
}
`

// nestedEventsGoBody is testfixture/nestedevents.Extension's registrations, for the factory module the packed and fused
// placements load (a factory module cannot import the PiG module's test fixtures).
const nestedEventsGoBody = `	ext.OnEvent("before_agent_start", func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("nested", "busy")
		return nil, nil
	})
	ext.OnEvent("session_info_changed", func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["name"].(string)
		ctx.Notify("name:"+name, "info")
		return nil, nil
	})
	ext.Command("nested-trigger", "Send a triggering message", func(ctx sdk.Context, _ string) error {
		trigger := true
		return ctx.SendMessage("nested", "Say hello", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "followUp"})
	})
	ext.Command("nested-name", "Append an entry, then name the session", func(ctx sdk.Context, _ string) error {
		if err := ctx.AppendEntry("nested-entry", map[string]any{"value": "hello"}); err != nil {
			return err
		}
		return ctx.SetSessionName("nested-name")
	})
	ext.Command("nested-wait", "Send a triggering message, then wait for the run", func(ctx sdk.Context, _ string) error {
		trigger := true
		if err := ctx.SendMessage("nested", "Say hello", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "followUp"}); err != nil {
			return err
		}
		if err := ctx.WaitForIdle(); err != nil {
			return err
		}
		ctx.Notify("idle", "info")
		return nil
	})
	ext.OnEvent("session_start", func(ctx sdk.Context, data map[string]any) (any, error) {
		if reason, _ := data["reason"].(string); reason == "new" {
			return nil, ctx.SendMessage("nested", "started", true, sdk.SendMessageOptions{})
		}
		return nil, nil
	})
	ext.Command("nested-send-wait", "Send a user message, then wait for idle", func(ctx sdk.Context, _ string) error {
		if err := ctx.SendUserMessage("Say hello", ""); err != nil {
			return err
		}
		return ctx.WaitForIdle()
	})
	ext.Command("nested-fresh", "Start a new Session", func(ctx sdk.Context, _ string) error {
		_, err := ctx.NewSession(nil)
		return err
	})
`

func nestedEventsGoFactory(t *testing.T, write func(string, string, os.FileMode) string, dir, name, body string) string {
	t.Helper()
	module := "example.com/" + strings.ReplaceAll(name, "-", "")
	write(filepath.Join(dir, "go.mod"), "module "+module+"\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n", 0o600)
	write(filepath.Join(dir, "extension.go"), fmt.Sprintf("package %s\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension {\n\text := sdk.New(%q)\n%s\treturn ext\n}\n", strings.ReplaceAll(name, "-", ""), name, body), 0o600)
	return dir
}

func nestedEventsPythonBody(name string) string {
	return fmt.Sprintf(`    ext = pig_sdk.Extension(%q)

    def before_agent_start(ctx, _data):
        ctx.set_status("nested", "busy")

    def session_info_changed(ctx, data):
        ctx.notify("name:" + str(data.get("name")), "info")

    def trigger(ctx, _args):
        ctx.send_message("nested", "Say hello", True, trigger_turn=True, deliver_as="followUp")

    def name(ctx, _args):
        ctx.append_entry("nested-entry", {"value": "hello"})
        ctx.set_session_name("nested-name")

    def wait(ctx, _args):
        ctx.send_message("nested", "Say hello", True, trigger_turn=True, deliver_as="followUp")
        ctx.wait_for_idle()
        ctx.notify("idle", "info")

    def session_start(ctx, data):
        if data.get("reason") == "new":
            ctx.send_message("nested", "started", True)

    def send_wait(ctx, _args):
        ctx.send_user_message("Say hello")
        ctx.wait_for_idle()

    def fresh(ctx, _args):
        ctx.new_session()

    ext.on_event("session_start", session_start)
    ext.command("nested-send-wait", "Send a user message, then wait for idle", send_wait)
    ext.command("nested-fresh", "Start a new Session", fresh)
    ext.on_event("before_agent_start", before_agent_start)
    ext.on_event("session_info_changed", session_info_changed)
    ext.command("nested-trigger", "Send a triggering message", trigger)
    ext.command("nested-name", "Append an entry, then name the session", name)
    ext.command("nested-wait", "Send a triggering message, then wait for the run", wait)
`, name)
}

func nestedEventsRustBody(name string) string {
	return fmt.Sprintf(`    let mut ext = Extension::new(%q);
    ext.on_before_agent_start(|ctx, _event| {
        ctx.set_status("nested", "busy");
        Ok(None)
    });
    ext.on_event("session_info_changed", |ctx, data| {
        ctx.notify(&format!("name:{}", data["name"].as_str().unwrap_or("")), "info");
        None
    });
    ext.command("nested-trigger", "Send a triggering message", |ctx, _args| match ctx.send_message("nested", "Say hello", true, Some(true), Some("followUp")) {
        Ok(()) => CommandResult::Ok,
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command("nested-name", "Append an entry, then name the session", |ctx, _args| {
        if let Err(err) = ctx.append_entry("nested-entry", serde_json::json!({"value": "hello"})) {
            return CommandResult::Error(err.to_string());
        }
        match ctx.set_session_name("nested-name") {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("nested-wait", "Send a triggering message, then wait for the run", |ctx, _args| {
        if let Err(err) = ctx.send_message("nested", "Say hello", true, Some(true), Some("followUp")) {
            return CommandResult::Error(err.to_string());
        }
        if let Err(err) = ctx.wait_for_idle() {
            return CommandResult::Error(err.to_string());
        }
        ctx.notify("idle", "info");
        CommandResult::Ok
    });
    ext.on_event("session_start", |ctx, data| {
        if data["reason"].as_str() == Some("new") {
            let _ = ctx.send_message("nested", "started", true, None, None);
        }
        None
    });
    ext.command("nested-send-wait", "Send a user message, then wait for idle", |ctx, _args| {
        if let Err(err) = ctx.send_user_message("Say hello", "") {
            return CommandResult::Error(err.to_string());
        }
        match ctx.wait_for_idle() {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("nested-fresh", "Start a new Session", |ctx, _args| match ctx.new_session(serde_json::json!({})) {
        Ok(_) => CommandResult::Ok,
        Err(err) => CommandResult::Error(err.to_string()),
    });
`, name)
}

func nestedEventsRustCrate(t *testing.T, write func(string, string, os.FileMode) string, dir, name, dependency, file, body string) string {
	t.Helper()
	write(filepath.Join(dir, "Cargo.toml"), fmt.Sprintf("[package]\nname = %q\nversion = \"0.1.0\"\nedition = \"2024\"\npublish = false\n\n[dependencies]\n%s\nserde_json = \"1\"\n", name, dependency), 0o600)
	write(filepath.Join(dir, "src", file), "#![allow(unused_imports)]\nuse pig_sdk::{CommandResult, Extension};\n\n"+body, 0o600)
	return dir
}

// nestedEventsStderr collects pig's stderr for failure reports while the process writes it.
type nestedEventsStderr struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *nestedEventsStderr) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *nestedEventsStderr) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// nestedEventsRPC is one pig RPC process; label names the SDK and placement in every failure.
type nestedEventsRPC struct {
	t       *testing.T
	label   string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderr  *nestedEventsStderr
	records chan map[string]any
	// progress is what the current await has collected, reported when it times out.
	progress []string
}

func startNestedEventsRPC(t *testing.T, label, pig string, env, args []string) *nestedEventsRPC {
	t.Helper()
	cmd := exec.Command(pig, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), env...)
	p := &nestedEventsRPC{t: t, label: label, cmd: cmd, stderr: &nestedEventsStderr{}, records: make(chan map[string]any, 64)}
	cmd.Stderr = p.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.stdin = stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(p.records)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				continue
			}
			select {
			case p.records <- record:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(done)
		if cmd.ProcessState == nil {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return p
}

func (p *nestedEventsRPC) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatalf("%s: write %s: %v", p.label, line, err)
	}
}

// await reads records until done reports true. A timeout names the SDK and placement that hung and what had arrived.
func (p *nestedEventsRPC) await(what string, budget time.Duration, done func(map[string]any) bool) {
	p.t.Helper()
	p.progress = nil
	timer := time.NewTimer(budget)
	defer timer.Stop()
	for {
		select {
		case record, ok := <-p.records:
			if !ok {
				p.t.Fatalf("%s: pig's output ended while waiting for %s; records %q\nstderr:\n%s", p.label, what, p.progress, p.stderr.String())
			}
			if done(record) {
				return
			}
		case <-timer.C:
			p.t.Fatalf("%s hung: no %s within %s; records %q\nstderr:\n%s", p.label, what, budget, p.progress, p.stderr.String())
		}
	}
}

// closeAndWait ends pig's input and requires a clean exit.
func (p *nestedEventsRPC) closeAndWait() {
	p.t.Helper()
	_ = p.stdin.Close()
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			p.t.Fatalf("%s: pig exited: %v\nstderr:\n%s", p.label, err, p.stderr.String())
		}
	case <-time.After(testbudget.Wait(p.t)):
		_ = p.cmd.Process.Kill()
		<-waited
		p.t.Fatalf("%s hung: pig did not exit after its input ended\nstderr:\n%s", p.label, p.stderr.String())
	}
}
