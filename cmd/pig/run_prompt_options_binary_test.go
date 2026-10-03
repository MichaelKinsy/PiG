//go:build !pig_strip_mcp

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every mode prompts through AgentSession.prompt: .upstream/v0.99.2/packages/coding-agent/src/core/agent-session.ts:2032-2033 builds the run's prompt from the options the before_agent_start handlers shared (_preparePromptAndToolLoadout, 1669-1683), and 864-883 (_installAgentNextTurnRefresh) rebuilds each later turn from them with the base snippets and guidelines merged under the run's. A handler that replaces appendSystemPrompt, adds a context file and deletes the read snippet therefore changes the first request of the run in print and interactive mode alike; the next turn keeps the append and context edits and lists read again. The real binary loads a Node extension against a scripted OpenAI-compatible model.
func TestRunPromptOptionEditsReachEveryTurnInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			// An existing settings.json skips the first-time setup dialog (D88) in interactive mode.
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			chat := startScriptedChatServer(t, chatCall("read", map[string]any{"path": "missing.txt"}), chatText("done"))
			models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
				"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
				"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
			}}})
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
				t.Fatal(err)
			}
			ext := filepath.Join(cwd, "options.mjs")
			source := `export default function (pi) { pi.on("before_agent_start", (event) => { const o = event.systemPromptOptions; o.appendSystemPrompt = "RUN-APPEND-MARK"; o.contextFiles.push({ path: "/run/AGENTS.md", content: "RUN-CONTEXT-MARK" }); delete o.toolSnippets.read; }); }`
			if err := os.WriteFile(ext, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("BASE-CONTEXT-MARK"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--session-dir", t.TempDir(), "--append-system-prompt", "BASE-APPEND-MARK", "-e", ext}
			if mode == "print" {
				args = append(args, "--print", "hello")
			}
			cmd := exec.CommandContext(t.Context(), binary, args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
			var output strings.Builder
			if mode == "interactive" {
				driveInteractivePrompts(t, cmd, []string{"hello"}, func() int { return len(chat.recorded()) }, 2, &output)
			} else {
				cmd.Stdin = strings.NewReader("")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("print mode: %v\n%s", err, out)
				}
			}
			requests := chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), output.String())
			}
			for i, request := range requests {
				for _, want := range []string{"RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK", "\n- bash: "} {
					if !strings.Contains(request.System, want) {
						t.Errorf("request %d lacks %q:\n%s", i, want, request.System)
					}
				}
				if strings.Contains(request.System, "BASE-APPEND-MARK") {
					t.Errorf("request %d keeps the replaced append text:\n%s", i, request.System)
				}
			}
			if strings.Contains(requests[0].System, "\n- read: ") {
				t.Errorf("the first request lists the deleted read snippet:\n%s", requests[0].System)
			}
			if !strings.Contains(requests[1].System, "\n- read: ") {
				t.Errorf("the next turn does not list the base read snippet again:\n%s", requests[1].System)
			}
		})
	}
}
