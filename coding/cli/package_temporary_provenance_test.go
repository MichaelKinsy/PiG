package cli

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

// upstream: packages/coding-agent/src/core/resource-loader.ts:409-410,435-439,466 — resolved temporary -e sources retain CLI provenance when commands are exposed over RPC.
func TestTemporaryGitCLIProvenanceReachesRPCCommands(t *testing.T) {
	digest := sha256.Sum256([]byte("git-github.com-test/extension"))
	testTemporaryCLIProvenance(t, "git:github.com/test/extension", filepath.Join("git-github.com", fmt.Sprintf("%x", digest)[:8], "test", "extension"))
}

func TestTemporaryNpmCLIProvenanceReachesRPCCommands(t *testing.T) {
	digest := sha256.Sum256([]byte("npm-"))
	testTemporaryCLIProvenance(t, "npm:provenance-fixture@1.2.3", filepath.Join("npm", fmt.Sprintf("%x", digest)[:8], "node_modules", "provenance-fixture"))
}

func testTemporaryCLIProvenance(t *testing.T, source, relativeCache string) {
	t.Helper()
	cwd, home := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cached := filepath.Join(agentDir, "tmp", "extensions", relativeCache)
	file := filepath.Join(cached, "index.mjs")
	writePackageResource(t, filepath.Join(cached, "package.json"), `{"name":"provenance-fixture","version":"1.2.3","pi":{"extensions":["index.mjs"]}}`)
	writePackageResource(t, file, `export default function(pi) { pi.registerCommand("provenance-probe", {description:"CLI provenance", handler: async () => {}}); }`)

	process := startRPCProcessAt(t, cwd, []string{
		"PIG_HOME=" + home,
		"PIG_CODING_AGENT_DIR=" + agentDir,
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "PI_OFFLINE=1",
	}, "--model", "test-faux/echo", "--no-session", "--no-extensions", "-e", source)
	process.send(`{"id":"commands","type":"get_commands"}`)
	process.await("temporary CLI command provenance", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "commands" {
			return false
		}
		if record["success"] != true {
			t.Fatalf("get_commands failed: %v", record)
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, value := range commands {
			command, _ := value.(map[string]any)
			if command["name"] != "provenance-probe" {
				continue
			}
			want := map[string]any{"path": file, "source": "cli", "scope": "temporary", "origin": "top-level"}
			if got := command["sourceInfo"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("RPC sourceInfo=%v, want %v", got, want)
			}
			if command["source"] != "extension" {
				t.Fatalf("command source=%v", command["source"])
			}
			return true
		}
		t.Fatalf("temporary extension command missing: %v\n%s", record, process.stderr.String())
		return false
	})
}
