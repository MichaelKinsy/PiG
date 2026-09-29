package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi's getAllRegisteredTools preserves each extension Map's insertion order (extensions/runner.ts:587-597), and AgentSession._refreshToolRegistry uses that order for the initial loadout (:3152-3159,3225-3228).
func TestRPCInitialExtensionToolsKeepRegistrationOrder(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	var source strings.Builder
	source.WriteString("export default function(pi) {\n")
	want := []string{"read", "bash", "edit", "write"}
	for i := range 32 {
		name := fmt.Sprintf("ordered_%02d", 31-i)
		want = append(want, name)
		fmt.Fprintf(&source, "pi.registerTool({name:%q,label:%q,description:%q,parameters:{type:'object',properties:{}},async execute(){return {content:[]}}});\n", name, name, name)
	}
	source.WriteString("}\n")
	path := filepath.Join(cwd, "ordered.mjs")
	if err := os.WriteFile(path, []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, cwd, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session", "--no-extensions", "--provider", "test-faux", "--model", "faux-1", "-e", path)
	p.send(`{"id":"prompt","type":"prompt","message":"What is 20+22?"}`)
	p.await("settled prompt", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"messages","type":"get_messages"}`)
	p.await("initial ordered tool declaration", func(record rpcRecord) bool {
		if record["id"] != "messages" {
			return false
		}
		system := record["data"].(map[string]any)["messages"].([]any)[0].(map[string]any)
		var got []string
		for _, tool := range system["toolsAdded"].([]any) {
			got = append(got, tool.(map[string]any)["name"].(string))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("initial tools = %v, want registration order %v", got, want)
		}
		return true
	})
}
