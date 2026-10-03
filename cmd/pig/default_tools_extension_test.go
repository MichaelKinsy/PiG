//go:build !pig_strip_mcp

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// `defaultTools` names the tools that start active, and the names are not limited to built-ins: Pi passes the resolved
// setting to the AgentSession as initialActiveToolNames (.upstream/v0.99.2/packages/coding-agent/src/core/sdk.ts:264-269),
// and the constructor activates every registered tool the list names, extension tools included
// (core/agent-session.ts:3487-3506 _refreshToolRegistry; docs/settings.md:44). The codemode built-in extension registers
// `codemode` inactive (extensions/codemode/index.ts:6-7, defaultActive: false), so `{"defaultTools":["+codemode"]}` is how a
// user starts with it. Probed on Pi 0.99.2 in print mode: `+codemode` declares read, bash, edit, write, codemode; a plain
// `["codemode"]` declares only codemode. Every mode builds its Session from the same tool selection (main.ts:822-830).
func TestDefaultToolsActivatesExtensionToolsAtStartup(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name, settings string
		want           []string
	}{
		{"modifier adds to the built-ins", `{"defaultTools":["+codemode"]}`, []string{"read", "bash", "edit", "write", "codemode"}},
		{"plain name replaces the built-ins", `{"defaultTools":["codemode"]}`, []string{"codemode"}},
		{"unknown names are ignored", `{"defaultTools":["+nonexistent_tool"]}`, []string{"read", "bash", "edit", "write"}},
		// Probed on Pi 0.99.2 in print, JSON and RPC mode: the request and the prompt keep the list order (agent-session.ts:1508-1511).
		{"list order is kept", `{"defaultTools":["codemode","read"]}`, []string{"codemode", "read"}},
		{"built-in list order is kept", `{"defaultTools":["bash","read"]}`, []string{"bash", "read"}},
	} {
		for _, mode := range []string{"print", "json", "rpc", "interactive"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				home, cwd := t.TempDir(), t.TempDir()
				agentDir := filepath.Join(home, "pig")
				if err := os.MkdirAll(agentDir, 0o700); err != nil {
					t.Fatal(err)
				}
				chat := startScriptedChatServer(t, chatText("done"))
				models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
					"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
					"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
				}}})
				if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.settings), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--session-dir", t.TempDir()}
				switch mode {
				case "print":
					args = append(args, "--print", "hello")
				case "json":
					args = append(args, "--mode", "json", "hello")
				case "rpc":
					args = append(args, "--mode", "rpc")
				}
				cmd := exec.CommandContext(t.Context(), binary, args...)
				cmd.Dir = cwd
				cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
				var output strings.Builder
				switch mode {
				case "interactive":
					driveInteractivePrompts(t, cmd, []string{"hello"}, func() int { return len(chat.recorded()) }, 1, &output)
				case "rpc":
					driveRPCPrompts(t, cmd, []string{"hello"}, &output)
				default:
					cmd.Stdin = strings.NewReader("")
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s mode: %v\n%s", mode, err, out)
					}
				}
				requests := chat.recorded()
				if len(requests) == 0 {
					t.Fatalf("no model request\n%s", output.String())
				}
				if !slices.Equal(requests[0].Tools, tc.want) {
					t.Fatalf("%s mode declares %v, want %v", mode, requests[0].Tools, tc.want)
				}
				last := -1
				for _, name := range tc.want {
					at := strings.Index(requests[0].System, "\n- "+name+": ")
					if at < 0 {
						t.Fatalf("%s mode prompt does not list %s:\n%s", mode, name, requests[0].System)
					}
					if at < last {
						t.Fatalf("%s mode prompt lists %s out of order %v:\n%s", mode, name, tc.want, requests[0].System)
					}
					last = at
				}
			})
		}
	}
}

// A reload activates the extension tools its settings newly add to `defaultTools`, in print, JSON and RPC mode: reload() diffs the
// resolved list before and after the settings reload and passes the additions to _buildRuntime as active tool names
// (core/agent-session.ts:3587-3609, #10245); print-mode.ts:97-99 and rpc-mode.ts:341-343 bind ctx.reload() to it. `codemode` is
// registered inactive, so it joins the active tools only through the added name. `--no-extensions` also drops the built-in extensions (resource-loader.ts:569-578), so the run keeps them. The old extension instance is stale after the
// reload, so the new instance reports the active tools from its session_start (reason "reload").
func TestReloadActivatesExtensionToolsAddedToDefaultTools(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	source := `import { writeFileSync, appendFileSync } from "node:fs";
import { join } from "node:path";
export default function (pi) {
  pi.on("session_start", async (event) => {
    if (event.reason === "reload") appendFileSync(process.env.AUDIT_OUT, "after=" + pi.getActiveTools().join(",") + "\n");
  });
  pi.registerCommand("addcodemode", { description: "x", handler: async (_args, ctx) => {
    appendFileSync(process.env.AUDIT_OUT, "before=" + pi.getActiveTools().join(",") + "\n");
    writeFileSync(join(process.env.PIG_CODING_AGENT_DIR, "settings.json"), JSON.stringify({ defaultTools: ["+codemode"] }));
    await ctx.reload();
  } });
}
`
	extension := filepath.Join(t.TempDir(), "add-codemode.mjs")
	if err := os.WriteFile(extension, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"print", "json", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
				"api": "openai-completions", "baseUrl": "http://127.0.0.1:9/v1", "apiKey": "fixture",
				"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
			}}})
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "out.log")
			env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "AUDIT_OUT=" + out}
			args := []string{"-e", extension, "--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--session-dir", t.TempDir()}
			if mode == "rpc" {
				process := startRPCProcessAt(t, cwd, env, args...)
				process.sendJSON(map[string]any{"id": "cmd", "type": "prompt", "message": "/addcodemode"})
				process.await("command response", func(r rpcRecord) bool { return isSuccessResponse(r, "cmd") })
				process.sendJSON(map[string]any{"id": "settled", "type": "get_state"})
				process.await("settled", func(r rpcRecord) bool { return isSuccessResponse(r, "settled") })
				process.closeAndWait("reload activates defaultTools")
			} else {
				flag := []string{"--print"}
				if mode == "json" {
					flag = []string{"--mode", "json"}
				}
				cmd := exec.CommandContext(t.Context(), binary, append(append(args, flag...), "/addcodemode")...)
				cmd.Dir, cmd.Env = cwd, append(os.Environ(), env...)
				cmd.Stdin = strings.NewReader("")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s mode: %v\n%s", mode, err, output)
				}
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want := "before=read,bash,edit,write\nafter=read,bash,edit,write,codemode\n"
			if string(data) != want {
				t.Fatalf("active tools around the reload = %q, want %q", data, want)
			}
		})
	}
}
