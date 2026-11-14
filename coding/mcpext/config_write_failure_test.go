//go:build !windows

package mcpext

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A rewrite of mcp.json that fails once it has begun (a full disk, a size limit, a kill) must leave the file as it was: the in-place write truncated it first and lost every configured server.
func TestMcpConfigWriteThatFailsKeepsEveryServer(t *testing.T) {
	if path := os.Getenv("MCP_TEST_PATH"); path != "" {
		signal.Ignore(syscall.SIGXFSZ)
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64, Max: 64}); err != nil {
			t.Fatal(err)
		}
		if _, err := AddMcpServerConfigJSON(path, "added", []byte(`{"command":"npx","args":["-y","@scope/added-server"]}`)); err == nil {
			t.Fatal("a write past the file size limit succeeded")
		}
		return
	}
	scratch := t.TempDir()
	path := filepath.Join(scratch, "mcp.json")
	seed := "{\n  \"mcpServers\": {\n    \"files\": {\"command\": \"npx\", \"args\": [\"-y\", \"@scope/files\"]},\n    \"search\": {\"command\": \"npx\", \"args\": [\"-y\", \"@scope/search\"]}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	cmd.Env = []string{"MCP_TEST_PATH=" + path, "HOME=" + scratch, "PIG_HOME=" + scratch, "PI_HOME=" + scratch, "PIG_CODING_AGENT_DIR=" + scratch, "PI_CODING_AGENT_DIR=" + scratch, "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("limited-write process: %v\n%s", err, out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, []byte(seed)) {
		t.Fatalf("mcp.json after the failed write is %d bytes %q, want the %d bytes it had", len(after), after, len(seed))
	}
	entries, _ := os.ReadDir(scratch)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp") {
			t.Errorf("a temporary file is left beside mcp.json: %s", entry.Name())
		}
	}
}
