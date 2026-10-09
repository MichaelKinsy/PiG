package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Upstream package-manager.ts:1296-1299,1377-1384: `pi install <dir>` records a local Package, and a local directory with no
// filter, "pi" manifest, or conventional resource directory loads as one extension. Pi 1.1.0 probe: installing a directory
// whose only entry is index.mjs, then a print-mode turn, runs that extension's tool.
func TestInstalledLocalDirectoryWithoutPackageResourcesLoadsAsAnExtension(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	extensionDir := filepath.Join(root, "notes extension")
	for _, dir := range []string{cwd, agentDir, extensionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entry := "export default function (pi) { pi.registerCommand(\"local-dir-note\", { description: \"note\", handler: async () => {} }); }\n"
	if err := os.WriteFile(filepath.Join(extensionDir, "index.mjs"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	install := exec.Command(binary, "install", extensionDir)
	install.Dir = cwd
	install.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	if output, err := install.CombinedOutput(); err != nil {
		t.Fatalf("pig install: %v\n%s", err, output)
	}
	run := runPigStartup(t, binary, root, agentDir, cwd, "{\"id\":\"commands\",\"type\":\"get_commands\"}\n", "--mode", "rpc", "--no-session")
	if run.err != nil {
		t.Fatalf("RPC startup: %v\n%s\n%s", run.err, run.stdout, run.stderr)
	}
	var response struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
		Data    struct {
			Commands []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.stdout)), &response); err != nil || response.ID != "commands" || !response.Success {
		t.Fatalf("RPC response=%s error=%v", run.stdout, err)
	}
	for _, command := range response.Data.Commands {
		if command.Name == "local-dir-note" && command.Source == "extension" {
			return
		}
	}
	t.Fatalf("installed local directory registered no extension command local-dir-note; output=%s\nstderr=%s", run.stdout, run.stderr)
}
