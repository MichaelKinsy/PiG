//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi 0.99.2 (#10192): with `codemode.mode: "only"` the active built-in tools are hidden declarations, and the system
// prompt's tool list leaves them out so it matches the declarations the request carries
// (.upstream/v0.99.2/packages/coding-agent/src/core/agent-session.ts:1634-1638 _rebuildSystemPrompt, 1674-1677
// _preparePromptAndToolLoadout). Every mode prompts through AgentSession.prompt, so print and interactive send the same
// prompt. `defaultTools` may name extension tools such as `codemode` (docs/settings.md:44; sdk.ts:264-269 passes the
// setting as initialActiveToolNames). Probes of real Pi 0.99.2 in print mode declare exactly `codemode` and list only
// `- codemode:` both for `defaultTools: ["+codemode"]` and for `--tools read,bash,codemode`. The real binary is driven
// against a scripted OpenAI-compatible model.
func TestAuditCodemodeOnlyPromptOmitsHiddenToolsInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name, mode, settings string
		args                 []string
	}{
		{"print defaultTools", "print", `{"defaultTools":["+codemode"],"codemode":{"mode":"only"}}`, nil},
		{"print tools flag", "print", `{"codemode":{"mode":"only"}}`, []string{"--tools", "read,bash,codemode"}},
		{"interactive tools flag", "interactive", `{"codemode":{"mode":"only"}}`, []string{"--tools", "read,bash,codemode"}},
	} {
		mode := tc.mode
		t.Run(tc.name, func(t *testing.T) {
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
			args = append(args, tc.args...)
			if mode == "print" {
				args = append(args, "--print", "hello")
			}
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
			var output strings.Builder
			if mode == "interactive" {
				driveInteractivePrompts(t, cmd, []string{"hello"}, func() int { return len(chat.recorded()) }, 1, &output)
			} else {
				cmd.Stdin = strings.NewReader("")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("print mode: %v\n%s", err, out)
				}
			}
			requests := chat.recorded()
			if len(requests) == 0 {
				t.Fatalf("no model request\n%s", output.String())
			}
			request := requests[0]
			if strings.Join(request.Tools, ",") != "codemode" {
				t.Fatalf("request declares %v, want only codemode", request.Tools)
			}
			if strings.Contains(request.System, "\n- read: ") || !strings.Contains(request.System, "\n- codemode: ") {
				t.Fatalf("%s prompt lists hidden tools or lacks codemode (declared tools %v):\n%s", mode, request.Tools, request.System)
			}
		})
	}
}
