package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// fixturePath is the stdio MCP server of the tests (Pi's test/fixtures/mcp-server.mjs, verbatim) and node, which runs it
// (Pi's tests run it with `process.execPath`).
func fixture(t testing.TB) (node, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed: the MCP fixture server is a Node program")
	}
	script, err = filepath.Abs("testdata/mcp-server.mjs")
	if err != nil {
		t.Fatal(err)
	}
	return node, script
}

// stdioEntry is the fixture server as a stdio entry of mcp.json; every start appends its pid to log.
func stdioEntry(t testing.TB, log string, extra map[string]any) map[string]any {
	t.Helper()
	node, script := fixture(t)
	entry := map[string]any{"command": node, "args": []string{script}, "env": map[string]string{"MCP_FIXTURE_LOG": log}}
	for key, value := range extra {
		entry[key] = value
	}
	return entry
}

// mcpJSON is `{ mcpServers }` as text.
func mcpJSON(t testing.TB, servers map[string]any) string {
	t.Helper()
	text, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// pidsOf is the pids the fixture appended to log (one per start).
func pidsOf(log string) []string {
	text, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	var pids []string
	for _, line := range strings.Split(strings.TrimSpace(string(text)), "\n") {
		if line != "" {
			pids = append(pids, line)
		}
	}
	return pids
}
