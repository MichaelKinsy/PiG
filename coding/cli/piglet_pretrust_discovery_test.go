package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The pre-trust pass of a project that needs a trust decision loads the user's extensions as Pi does, unless the Piglet's discovery
// leaves the user scope out: then no ambient extension factory runs before the decision either. Real binary, untrusted project
// (its only config is mcp.json), a user extension that marks when its factory runs.
func TestPreTrustPassFollowsPigletDiscovery(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name    string
		piglet  string
		runsExt bool
	}{
		{"stock pig", "", true},
		{"Piglet discovering user extensions", "name: probe\ndiscovery:\n  extensions: [user]\n  skills: []\n", true},
		{"Piglet discovering no extensions", "name: probe\ndiscovery:\n  extensions: []\n  skills: []\n", false},
		{"Piglet discovering workspace extensions only", "name: probe\ndiscovery:\n  extensions: [workspace]\n  skills: []\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			project := t.TempDir()
			marker := filepath.Join(home, "factory-ran")
			write := func(path, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(agentDir, "extensions", "ambient.ts"), `import {writeFileSync} from "node:fs"; export default function(){writeFileSync(`+strconv.Quote(marker)+`,"x");}`)
			write(filepath.Join(agentDir, "models.json"), `{"providers":{"test-faux":{"baseUrl":"http://localhost:0","api":"test-faux","authHeader":false,"models":[{"id":"faux-1","name":"Test Faux","api":"test-faux","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}}}`)
			write(filepath.Join(project, codingagent.CONFIG_DIR_NAME, "mcp.json"), `{"mcpServers":{}}`)
			args := []string{"--model", "test-faux/faux-1", "--no-session", "--print", "What is 20+22?"}
			if tc.piglet != "" {
				path := filepath.Join(home, "piglet.yaml")
				write(path, tc.piglet)
				args = append(args, "--piglet", path)
			}
			command := exec.CommandContext(testbudget.Context(t), binary, args...)
			command.Dir = project
			command.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("pig: %v\n%s", err, output)
			}
			_, statErr := os.Stat(marker)
			if ran := statErr == nil; ran != tc.runsExt {
				t.Fatalf("user extension factory ran = %v, want %v", ran, tc.runsExt)
			}
		})
	}
}
