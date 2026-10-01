//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// pi.getSettings(), ctx.tools and ctx.executeTool() (.upstream/v0.99.1/packages/coding-agent/src/core/agent-session.ts:3339, 3382-3383 bind them to every extension runner in every mode) reach a subprocess extension only through the mode's bridge wiring. These tests run the real binary in each mode with a Node and a Go extension and read what the extension saw from inside its tool: the settings value the agent directory holds, the tools the session lets a tool call, and the outcome of a nested call.

const wiredProbeNode = `import { writeFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  // The nested calls' events the session emitted to this extension, by kind (parentToolCallId names the calling tool call).
  const nestedEvents = [];
  pi.on("tool_execution_update", (event) => { if (event.parentToolCallId) nestedEvents.push("update:" + event.toolCallId); });
  pi.on("tool_execution_end", (event) => { if (event.parentToolCallId) nestedEvents.push("end:" + event.toolCallId); });
  pi.on("tool_result", (event) => { if (event.parentToolCallId) nestedEvents.push("result:" + event.toolCallId); });
  pi.registerTool({
    name: "wired_progress", label: "wired_progress", description: "Only called by echo_bridge; reports two partial results.",
    parameters: { type: "object", properties: {} },
    execute: async (_id, _params, _signal, onUpdate) => {
      onUpdate({ content: [{ type: "text", text: "one" }], details: {} });
      onUpdate({ content: [{ type: "text", text: "two" }], details: {} });
      return { content: [{ type: "text", text: "progress done" }], details: {} };
    },
  });
  pi.registerTool({
    name: "wired_helper", label: "wired_helper", description: "Only called by echo_bridge.",
    parameters: { type: "object", properties: { text: { type: "string" } } },
    execute: async (_id, params) => ({ content: [{ type: "text", text: "helper saw " + (params.text ?? "") }], details: {} }),
  });
  pi.registerTool({
    name: "echo_bridge", label: "echo_bridge", description: "Reads settings, tools and runs a nested call.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    execute: async (id, params, _signal, _onUpdate, ctx) => {
      const probe = { callerId: id, errors: [] };
      const attempt = async (name, body) => { try { await body(); } catch (error) { probe.errors.push(name + ": " + error.message); } };
      await attempt("getSettings", async () => { probe.quietStartup = pi.getSettings().quietStartup ?? null; });
      await attempt("tools", async () => { probe.tools = ctx.tools.map((tool) => tool.name); });
      await attempt("executeTool", async () => {
        const outcome = await ctx.executeTool("wired_helper", { text: params.text });
        probe.nestedId = outcome.toolCall.id;
        probe.nestedText = outcome.result.content[0].text;
        probe.nestedIsError = outcome.isError;
      });
      await attempt("onUpdate throw", async () => {
        const seen = [];
        probe.progressSeen = seen;
        try {
          await ctx.executeTool("wired_progress", {}, { onUpdate: (partial) => { seen.push(partial.content[0].text); if (seen.length === 1) throw new Error("callback boom"); } });
          probe.progressRejection = "none";
        } catch (error) {
          probe.progressRejection = error.message;
        }
        probe.nestedEvents = nestedEvents.filter((event) => event.includes(id + "/2"));
      });
      await attempt("live tools", async () => {
        const active = pi.getActiveTools();
        pi.setActiveTools(["echo_bridge"]);
        probe.toolsAfterSetActive = ctx.tools.map((tool) => tool.name);
        pi.setActiveTools(active);
      });
      writeFileSync(join(ctx.cwd, "probe.json"), JSON.stringify(probe));
      return { content: [{ type: "text", text: "echo-bridge: " + params.text }], details: {} };
    },
  });
}
`

const wiredProbeGo = `package wired

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New("wired")
	schema := sdk.Schema{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}
	var eventsMu sync.Mutex
	var nestedEvents []string
	recordNested := func(kind string) sdk.EventFunc {
		return func(_ sdk.Context, data map[string]any) (any, error) {
			if data["parentToolCallId"] != nil {
				eventsMu.Lock()
				nestedEvents = append(nestedEvents, fmt.Sprintf("%s:%v", kind, data["toolCallId"]))
				eventsMu.Unlock()
			}
			return nil, nil
		}
	}
	e.OnEvent(sdk.EventToolExecutionUpdate, recordNested("update"))
	e.OnEvent(sdk.EventToolExecutionEnd, recordNested("end"))
	e.OnEvent(sdk.EventToolResult, recordNested("result"))
	e.Tool("wired_progress", "Only called by echo_bridge; reports two partial results.", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		for _, step := range []string{"one", "two"} {
			if err := ctx.OnUpdate(step); err != nil {
				return nil, err
			}
		}
		return "progress done", nil
	})
	e.Tool("wired_helper", "Only called by echo_bridge.", schema, func(_ sdk.Context, args map[string]any) (any, error) {
		text, _ := args["text"].(string)
		return "helper saw " + text, nil
	})
	e.Tool("echo_bridge", "Reads settings, tools and runs a nested call.", schema, func(ctx sdk.Context, args map[string]any) (any, error) {
		text, _ := args["text"].(string)
		probe := map[string]any{"callerId": ctx.ToolCallID()}
		var problems []string
		if settings, err := ctx.GetSettings(); err != nil {
			problems = append(problems, "getSettings: "+err.Error())
		} else {
			probe["quietStartup"] = settings["quietStartup"]
		}
		if tools, err := ctx.Tools(); err != nil {
			problems = append(problems, "tools: "+err.Error())
		} else {
			names := []string{}
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			probe["tools"] = names
		}
		if outcome, err := ctx.ExecuteTool("wired_helper", map[string]any{"text": args["text"]}, nil); err != nil {
			problems = append(problems, "executeTool: "+err.Error())
		} else {
			probe["nestedId"] = outcome.ToolCall.ID
			probe["nestedIsError"] = outcome.IsError
			probe["nestedText"] = outcome.Result.Text()
		}
		{
			var seen []string
			_, err := ctx.ExecuteTool("wired_progress", map[string]any{}, &sdk.ExecuteToolOptions{OnUpdate: func(partial sdk.AgentToolResult) {
				seen = append(seen, partial.Text())
				if len(seen) == 1 {
					panic("callback boom")
				}
			}})
			probe["progressSeen"] = seen
			if err != nil {
				probe["progressRejection"] = err.Error()
			} else {
				probe["progressRejection"] = "none"
			}
			eventsMu.Lock()
			var nested []string
			for _, event := range nestedEvents {
				if strings.Contains(event, ctx.ToolCallID()+"/2") {
					nested = append(nested, event)
				}
			}
			eventsMu.Unlock()
			probe["nestedEvents"] = nested
		}
		active, err := ctx.GetActiveTools()
		if err != nil {
			problems = append(problems, "GetActiveTools: "+err.Error())
		}
		ctx.SetActiveTools([]string{"echo_bridge"})
		if tools, err := ctx.Tools(); err != nil {
			problems = append(problems, "tools after SetActiveTools: "+err.Error())
		} else {
			names := []string{}
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			probe["toolsAfterSetActive"] = names
		}
		ctx.SetActiveTools(active)
		probe["errors"] = problems
		data, err := json.Marshal(probe)
		if err != nil {
			return nil, err
		}
		return "echo-bridge: " + text, os.WriteFile(filepath.Join(ctx.Cwd(), "probe.json"), data, 0o600)
	})
	return e
}
`

type wiredProbe struct {
	CallerID      string   `json:"callerId"`
	QuietStartup  *bool    `json:"quietStartup"`
	Tools         []string `json:"tools"`
	NestedID      string   `json:"nestedId"`
	NestedText    string   `json:"nestedText"`
	NestedIsError bool     `json:"nestedIsError"`
	Errors        []string `json:"errors"`
	// ProgressSeen, ProgressRejection and NestedEvents are the onUpdate-throw probe: the partial results the callback saw, what the nested call settled with, and the nested call's events the session sent this extension.
	ProgressSeen        []string `json:"progressSeen"`
	ProgressRejection   string   `json:"progressRejection"`
	NestedEvents        []string `json:"nestedEvents"`
	ToolsAfterSetActive []string `json:"toolsAfterSetActive"`
}

// writeWiredExtension writes the probe extension of one SDK and returns the -e argument.
func writeWiredExtension(t *testing.T, language string) string {
	t.Helper()
	dir := t.TempDir()
	switch language {
	case "node":
		path := filepath.Join(dir, "wired-probe.mjs")
		if err := os.WriteFile(path, []byte(wiredProbeNode), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	case "go":
		root := filepath.Join(dir, "wired")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"go.mod":       "module example.test/wired\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
			"extension.go": wiredProbeGo,
		} {
			if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	t.Fatalf("unknown language %q", language)
	return ""
}

// wiredEnvironment isolates the run: the agent directory holds the settings the probe reads back.
func wiredEnvironment(t *testing.T) (env []string, cwd, sessionDir string) {
	t.Helper()
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env = []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1"}
	return env, cwd, sessionDir
}

// The scripted provider answers "Run: extension echo hello" with a call of echo_bridge.
const wiredPrompt = "Run: extension echo hello"

func readWiredProbe(t *testing.T, cwd string) wiredProbe {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cwd, "probe.json"))
	if err != nil {
		t.Fatalf("the extension's tool did not run: %v", err)
	}
	var probe wiredProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("probe %s: %v", data, err)
	}
	return probe
}

func assertWiredProbe(t *testing.T, probe wiredProbe) {
	t.Helper()
	if len(probe.Errors) != 0 {
		t.Errorf("the extension's calls failed: %v", probe.Errors)
	}
	if probe.QuietStartup == nil || !*probe.QuietStartup {
		t.Errorf("getSettings().quietStartup = %v, want true from the agent directory's settings.json", probe.QuietStartup)
	}
	for _, name := range []string{"echo_bridge", "wired_helper"} {
		if !slices.Contains(probe.Tools, name) {
			t.Errorf("ctx.tools = %v, want it to list %s", probe.Tools, name)
		}
	}
	if probe.CallerID == "" || probe.NestedID != probe.CallerID+"/1" {
		t.Errorf("nested call id = %q, want %q", probe.NestedID, probe.CallerID+"/1")
	}
	if probe.NestedText != "helper saw hello" || probe.NestedIsError {
		t.Errorf("nested outcome = %q (isError %v), want the helper's result", probe.NestedText, probe.NestedIsError)
	}
	// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: a throw from onUpdate rejects the nested call. Both partial results still reached the callback; the first one raised no tool_execution_update, and the rejected call reached neither afterToolCall (tool_result) nor tool_execution_end.
	if !slices.Equal(probe.ProgressSeen, []string{"one", "two"}) {
		t.Errorf("onUpdate saw %v, want both partial results", probe.ProgressSeen)
	}
	if probe.ProgressRejection != "callback boom" {
		t.Errorf("the nested call settled with %q, want the callback's error", probe.ProgressRejection)
	}
	if want := []string{"update:" + probe.CallerID + "/2"}; !slices.Equal(probe.NestedEvents, want) {
		t.Errorf("events of the rejected nested call = %v, want only the second update's %v", probe.NestedEvents, want)
	}
	// runner.ts:958-961: ctx.tools is live, so it reflects setActiveTools called in the same handler.
	if !slices.Equal(probe.ToolsAfterSetActive, []string{"echo_bridge"}) {
		t.Errorf("ctx.tools after setActiveTools = %v, want only the active tool", probe.ToolsAfterSetActive)
	}
}

func TestExtensionToolContextIsWiredInEveryMode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and extensions")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, language := range []string{"node", "go"} {
		for _, mode := range []string{"print", "json", "rpc", "interactive"} {
			t.Run(language+"/"+mode, func(t *testing.T) {
				extension := writeWiredExtension(t, language)
				env, cwd, sessionDir := wiredEnvironment(t)
				args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", extension}
				switch mode {
				case "rpc":
					process := startRPCProcessAt(t, cwd, env, args...)
					process.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": wiredPrompt})
					process.await("agent_end", func(r rpcRecord) bool { return r["type"] == "agent_end" })
					process.closeAndWait("wired probe")
				case "print", "json":
					if mode == "print" {
						args = append(args, "--print")
					} else {
						args = append(args, "--mode", "json")
					}
					ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
					defer cancel()
					cmd := exec.CommandContext(ctx, binary, append(args, wiredPrompt)...)
					cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
					if out, err := cmd.CombinedOutput(); err != nil || strings.Contains(string(out), "Extension error") {
						t.Fatalf("%s: %v\n%s", mode, err, out)
					}
				case "interactive":
					runWiredInteractive(t, binary, cwd, env, args)
				}
				assertWiredProbe(t, readWiredProbe(t, cwd))
			})
		}
	}
}

// runWiredInteractive types the prompt into the TUI of the real binary and waits for the extension's tool to write its probe.
func runWiredInteractive(t *testing.T, binary, cwd string, env, args []string) {
	t.Helper()
	master, slave := openPTY(t, 40, 160)
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, append(args, "--no-session", "--approve")...)
	cmd.Dir, cmd.Env = cwd, append(append(os.Environ(), env...), "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	output := &ptyOutput{}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := master.Read(buffer)
			if n > 0 {
				_, _ = output.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	done := make(chan struct{})
	go func() { defer close(done); _ = cmd.Wait() }()
	t.Cleanup(func() { cancel(); <-done; _ = master.Close() })
	output.waitQuiet(0, []byte("faux-1"), 200*time.Millisecond, testbudget.Wait(t))
	if _, err := master.Write([]byte(wiredPrompt + "\r")); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(cwd, "probe.json")
	for deadline := time.Now().Add(testbudget.Wait(t)); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(probe); err == nil {
			return
		}
		select {
		case <-done:
			t.Fatalf("pig exited before the extension's tool ran; screen:\n%s", output.since(0))
		default:
		}
	}
	t.Fatalf("the extension's tool did not run in interactive mode; screen:\n%s", output.since(0))
}
