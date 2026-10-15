//go:build !pig_strip_mcp && !pig_strip_docs

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// Pi names the documentation of its installed package (system-prompt.ts:162-169), which exists wherever Pi runs. PiG writes its bundle under the config root before anything other than PiG reads it (D22), so a config root this user cannot write and that holds no current bundle has no documentation to name. The system prompt then has no docs section, as a docs-stripped Piglet's has none, and startup prints nothing: Pi needs no write to start, and what PiG could not write is reported by `pig diagnose`. A bundle written earlier, before the root became read-only, is still named. A writable root's bundle is named and written before its first reader (here `pig docs path`). The agent dir stays writable in every case: Pi refuses to start without that (auth.json), and so does PiG.
func TestReadOnlyConfigRootStartsQuietlyAndNamesOnlyDocsThatExist(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user from writing")
	}
	binary := buildPigBinaryForSignalTest(t)
	for _, test := range []struct {
		name string
		// root returns the config root for the run.
		root     func(t *testing.T) string
		wantDocs bool
	}{
		{"writable", func(t *testing.T) string { return filepath.Join(t.TempDir(), "writable") }, true},
		{"read-only and never written", func(t *testing.T) string {
			root := filepath.Join(t.TempDir(), "empty")
			makeReadOnlyTree(t, root, true)
			return root
		}, false},
		{"read-only after `pig docs sync`", func(t *testing.T) string {
			root := filepath.Join(t.TempDir(), "baked")
			runDocsPath(t, binary, root, "sync")
			makeReadOnlyTree(t, root, false)
			return root
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := test.root(t)
			system, stderr := runReadOnlyRootPrompt(t, binary, root)
			if stderr != "" {
				t.Errorf("stderr = %q, want nothing", stderr)
			}
			readme := filepath.Join(pigdocs.DocsDir(root), "README.md")
			if !test.wantDocs {
				if strings.Contains(system, "PiG documentation") || strings.Contains(system, filepath.Join(root, "docs")) {
					t.Fatalf("the system prompt names documentation the config root does not hold:\n%s", system)
				}
				return
			}
			if !strings.Contains(system, "- Main documentation: "+readme+"\n") {
				t.Fatalf("the system prompt does not name %s:\n%s", readme, system)
			}
			if got := strings.TrimSpace(runDocsPath(t, binary, root, "path")); got != filepath.Dir(readme) {
				t.Fatalf("`pig docs path` = %q, want %q", got, filepath.Dir(readme))
			}
			if _, err := os.Stat(readme); err != nil {
				t.Fatalf("the named README does not exist once `pig docs path` printed it: %v", err)
			}
		})
	}
}

// runDocsPath runs `pig docs <sub>` with root as PIG_HOME and returns its stdout; it fails the test unless the command succeeds quietly.
func runDocsPath(t *testing.T, binary, root, sub string) string {
	t.Helper()
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, "docs", sub)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "PIG_HOME="+root,
		"PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=")
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil || errOut.Len() != 0 {
		t.Fatalf("pig docs %s: %v, stderr %q", sub, err, errOut.String())
	}
	return out.String()
}

// runReadOnlyRootPrompt runs `-p "What is 20+22?"` against a scripted OpenAI-compatible model with root as PIG_HOME and a separate writable agent dir. It fails unless the run answers 42, and returns the system prompt the model received and stderr.
func runReadOnlyRootPrompt(t *testing.T, binary, root string) (system, stderr string) {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seedFirstRunDone(t, agentDir)
	chat := startScriptedChatServer(t, chatText("42"))
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
		"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
	}}})
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "--model", "fixture/test", "--no-session", "-p", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) != "42" {
		t.Fatalf("pig -p: %v, stdout %q, want 42\nstderr:\n%s", err, out.String(), errOut.String())
	}
	requests := chat.recorded()
	if len(requests) != 1 {
		t.Fatalf("the model received %d requests, want 1", len(requests))
	}
	return requests[0].System, errOut.String()
}
