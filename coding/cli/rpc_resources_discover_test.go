package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi's AgentSession.bindExtensions emits session_start and then extends the resource loader once with what resources_discover returns (agent-session.ts:3207, _extendResourcesFromExtensions). RPC startup asks the extension once, with reason "startup", and get_commands lists the discovered template with the extension's provenance; the label keeps the .mjs suffix, since getExtensionSourceLabel strips only .ts and .js (agent-session.ts:3327-3333).
func TestRPCStartupDiscoversExtensionResourcesOnce(t *testing.T) {
	extension, err := filepath.Abs(filepath.Join("testdata", "discover-once", "discover-once.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	log := filepath.Join(home, "discover.log")
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"HOME=" + home, "USERPROFILE=" + home, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "PIG_TEST_DISCOVER_LOG=" + log,
	}, "--no-session", "--model", "test-faux/faux-1", "-e", extension)
	p.send(`{"id":"commands","type":"get_commands"}`)
	p.await("the command catalog", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "commands" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, value := range commands {
			command, _ := value.(map[string]any)
			if command["name"] != "discovered-once" {
				continue
			}
			info, _ := command["sourceInfo"].(map[string]any)
			if info["source"] != "extension:discover-once.mjs" || info["scope"] != "temporary" {
				t.Fatalf("discovered template sourceInfo = %v", info)
			}
			return true
		}
		t.Fatalf("get_commands lacks the discovered template: %v", commands)
		return true
	})
	p.closeAndWait("the catalog was read")
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(data)); len(got) != 1 || got[0] != "startup" {
		t.Fatalf("resources_discover calls = %q, want one with reason startup", got)
	}
}
