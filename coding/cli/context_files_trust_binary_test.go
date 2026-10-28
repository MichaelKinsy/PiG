//go:build !pig_strip_mcp

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pig additive (D211): a Piglet with contextFiles: trusted-only withholds an untrusted project's context files and says so; stock pig, and a trusted project, load them all. The agent directory's file and the file in the user's home directory stay. The real binary, a scripted model and each mode report the first request's system prompt.
func TestTrustedOnlyPigletWithholdsProjectContextFilesInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "json", "rpc", "interactive"} {
		for _, variant := range []string{"stock", "piglet-never", "piglet-always"} {
			trust, withheld, usePiglet := "never", variant == "piglet-never", variant != "stock"
			if variant == "piglet-always" {
				trust = "always"
			}
			t.Run(mode+"/"+variant, func(t *testing.T) {
				home := t.TempDir()
				cwd := filepath.Join(home, "work", "project")
				agentDir := filepath.Join(home, "pig")
				for _, dir := range []string{cwd, agentDir} {
					if err := os.MkdirAll(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				write := func(path, content string) {
					t.Helper()
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				write(filepath.Join(agentDir, "settings.json"), `{"defaultProjectTrust":"`+trust+`"}`)
				write(filepath.Join(agentDir, "AGENTS.md"), "GLOBAL-CONTEXT-MARK")
				write(filepath.Join(home, "AGENTS.md"), "HOME-CONTEXT-MARK")
				write(filepath.Join(home, "work", "AGENTS.md"), "PARENT-CONTEXT-MARK")
				write(filepath.Join(cwd, "AGENTS.md"), "PROJECT-CONTEXT-MARK")
				chat := startScriptedChatServer(t, chatText("done"))
				models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
					"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
					"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
				}}})
				write(filepath.Join(agentDir, "models.json"), string(models))
				args := []string{"--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--session-dir", t.TempDir()}
				if usePiglet {
					pigletPath := filepath.Join(t.TempDir(), "trusted-only.yaml")
					write(pigletPath, "name: trusted-only\ncontextFiles: trusted-only\n")
					args = append(args, "--piglet", pigletPath)
				}
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
				cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PI_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
				var notices strings.Builder
				switch mode {
				case "interactive":
					driveInteractivePrompts(t, cmd, []string{"hello"}, func() int { return len(chat.recorded()) }, 1, &notices)
				case "rpc":
					stdin, err := cmd.StdinPipe()
					if err != nil {
						t.Fatal(err)
					}
					var stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = io.Discard, &stderr
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					_, _ = io.WriteString(stdin, `{"id":"run","type":"prompt","message":"hello"}`+"\n")
					for deadline := time.Now().Add(30 * time.Second); len(chat.recorded()) == 0 && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
					}
					_ = stdin.Close()
					_ = cmd.Wait()
					notices.WriteString(stderr.String())
				default:
					var stderr bytes.Buffer
					cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(""), io.Discard, &stderr
					if err := cmd.Run(); err != nil {
						t.Fatalf("%s mode: %v\n%s", mode, err, stderr.String())
					}
					notices.WriteString(stderr.String())
				}
				requests := chat.recorded()
				if len(requests) == 0 {
					t.Fatalf("no request reached the model\n%s", notices.String())
				}
				system := requests[0].System
				t.Logf("system prompt bytes for %s: %d", variant, len(system))
				for _, want := range []string{"GLOBAL-CONTEXT-MARK", "HOME-CONTEXT-MARK"} {
					if !strings.Contains(system, want) {
						t.Errorf("system prompt lacks %q, which stays loaded", want)
					}
				}
				for _, mark := range []string{"PARENT-CONTEXT-MARK", "PROJECT-CONTEXT-MARK"} {
					if got := strings.Contains(system, mark); got != !withheld {
						t.Errorf("%s: system prompt holds %q = %v", variant, mark, got)
					}
				}
				said := strings.Contains(notices.String(), "its context files were not loaded")
				if said != withheld {
					t.Errorf("%s: notice present = %v\n%s", variant, said, notices.String())
				}
			})
		}
	}
}
