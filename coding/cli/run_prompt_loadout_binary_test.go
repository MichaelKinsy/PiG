//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi builds the run's prompt once, from the options the before_agent_start handlers shared, after it applied the live loadout to them (.upstream/v1.5.2/packages/coding-agent/src/core/agent-session.ts:2005-2043 and 1828-1840; runner.ts:1434-1470 hands every handler one options object and stores a returned systemPrompt as forceSystemPrompt). A handler that also narrows the loadout to bash therefore still sees its option edits in the request, and the skills section names bash as the reader (system-prompt.ts:177, skills.ts:368-370). The expected values are derived from those lines, not from a Pi run. The real binary loads a Node extension and a user skill against a scripted OpenAI-compatible model.
func TestRunPromptOptionEditsSurviveALoadoutChangeInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	const narrow = `pi.setActiveTools(["bash"]);`
	const edit = `const o = event.systemPromptOptions; o.appendSystemPrompt = "RUN-APPEND-MARK"; o.contextFiles.push({ path: "/run/AGENTS.md", content: "RUN-CONTEXT-MARK" });`
	shapes := []struct {
		name, source string
		args         []string
		want, absent []string
		exact        string
	}{
		{
			name:   "options",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` }); }`,
			want:   []string{"RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "custom prompt option",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + ` event.systemPromptOptions.customPrompt = "RUN-CUSTOM-MARK"; }); }`,
			want:   []string{"RUN-CUSTOM-MARK", "BASE-APPEND-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "custom base prompt",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` }); }`,
			args:   []string{"--system-prompt", "CUSTOM-BASE-MARK"},
			want:   []string{"CUSTOM-BASE-MARK", "RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "two handlers",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + ` event.systemPromptOptions.appendSystemPrompt = "FIRST-MARK"; }); pi.on("before_agent_start", (event) => { event.systemPromptOptions.appendSystemPrompt += " SECOND-MARK"; }); }`,
			want:   []string{"FIRST-MARK SECOND-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "replaced prompt",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` return { systemPrompt: "FORCED-PROMPT-MARK" }; }); }`,
			exact:  "FORCED-PROMPT-MARK",
		},
	}
	for _, shape := range shapes {
		for _, mode := range []string{"print", "interactive"} {
			t.Run(shape.name+"/"+mode, func(t *testing.T) {
				home, cwd := t.TempDir(), t.TempDir()
				agentDir := filepath.Join(home, "pig")
				skillDir := filepath.Join(agentDir, "skills", "demo")
				if err := os.MkdirAll(skillDir, 0o700); err != nil {
					t.Fatal(err)
				}
				// An existing settings.json skips the first-time setup dialog (D88) in interactive mode.
				if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: DEMO-SKILL-MARK\n---\nbody\n"), 0o600); err != nil {
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
				ext := filepath.Join(cwd, "options.mjs")
				if err := os.WriteFile(ext, []byte(shape.source), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("BASE-CONTEXT-MARK"), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"--model", "fixture/test", "--no-prompt-templates", "--session-dir", t.TempDir(), "--append-system-prompt", "BASE-APPEND-MARK", "-e", ext}
				args = append(args, shape.args...)
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
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("print mode: %v\n%s", err, out)
					}
				}
				requests := chat.recorded()
				if len(requests) != 1 {
					t.Fatalf("requests = %d, want 1\n%s", len(requests), output.String())
				}
				system := requests[0].System
				if shape.exact != "" {
					if system != shape.exact {
						t.Fatalf("system prompt = %q, want %q", system, shape.exact)
					}
					return
				}
				want := append([]string{"DEMO-SKILL-MARK", "Use bash to load a skill's file"}, shape.want...)
				for _, mark := range want {
					if !strings.Contains(system, mark) {
						t.Errorf("the request lacks %q:\n%s", mark, system)
					}
				}
				absent := []string{"Use the read tool to load a skill's file"}
				if !slices.Contains(shape.want, "BASE-APPEND-MARK") {
					absent = append(absent, "BASE-APPEND-MARK")
				}
				for _, mark := range absent {
					if strings.Contains(system, mark) {
						t.Errorf("the request keeps %q:\n%s", mark, system)
					}
				}
			})
		}
	}
}
