//go:build !pig_strip_mcp

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Every session mode starts the scheduled extension cache collection once it is running. The model answers only after the
// collection has written its marker, so a mode that never starts the collection fails here: loading extensions only
// schedules it, and nothing else would run it.
func TestEverySessionModeStartsTheAutomaticExtensionCacheCollection(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "json", "rpc", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			pigHome, agentDir := filepath.Join(home, ".pig"), filepath.Join(home, "pig")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(pigHome, "cache", ".last-auto-gc")
			collected := make(chan bool, 1)
			chat := startScriptedChatServer(t, func(chatRequest) chatReply {
				deadline := time.Now().Add(testbudget.Wait(t))
				for time.Now().Before(deadline) {
					if _, err := os.Stat(marker); err == nil {
						collected <- true
						return chatReply{Text: "done"}
					}
					time.Sleep(10 * time.Millisecond)
				}
				collected <- false
				return chatReply{Text: "done"}
			})
			models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
				"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
				"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
			}}})
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
				t.Fatal(err)
			}
			extensionPath := filepath.Join(cwd, "noop.mjs")
			if err := os.WriteFile(extensionPath, []byte("export default function (pi) {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--extension", extensionPath, "--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--session-dir", t.TempDir()}
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
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+pigHome, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PI_HOME="+pigHome, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
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
				output.Write(out)
			}
			select {
			case ok := <-collected:
				if !ok {
					t.Fatalf("%s mode never ran the cache collection: no %s\n%s", mode, marker, output.String())
				}
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("%s mode sent no model request\n%s", mode, output.String())
			}
			if data, err := os.ReadFile(filepath.Join(pigHome, "cache", ".auto-gc.error")); err == nil {
				t.Fatalf("%s mode recorded a collection failure: %s", mode, data)
			}
		})
	}
}
