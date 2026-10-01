package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// `--no-extensions` also disables the built-in extensions, and `-e builtin:<name>` still loads one, as upstream does (probed against 0.99.1: `pi --mode rpc get_commands` lists llama and mcp by default, nothing with --no-extensions, and only llama with --no-extensions -e builtin:llama.cpp). The parity runner relies on it to give both binaries the same baseline.
// upstream: main.ts:569 (extensionFactories = builtInExtensions), resource-loader.ts:716 (`builtin:<name>` resolution), package-manager.ts:997 (`-e builtin:<name>`).
func TestNoExtensionsSkipsBuiltInExtensionsButExplicitBuiltinStillLoads(t *testing.T) {
	for _, tt := range []struct {
		name  string
		flags []string
		llama bool
	}{
		{"default loads the built-in llama.cpp extension", nil, true},
		{"--no-extensions skips it", []string{"--no-extensions"}, false},
		{"--no-extensions -e builtin:llama.cpp loads it", []string{"--no-extensions", "-e", "builtin:llama.cpp"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			p := startRPCProcessAt(t, t.TempDir(), []string{
				"HOME=" + home, "USERPROFILE=" + home, "PIG_HOME=" + home,
				"PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1", "PIG_OFFLINE=1",
			}, append([]string{"--no-session"}, tt.flags...)...)
			p.send(`{"id":"commands","type":"get_commands"}`)
			p.await("the command catalog", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "commands" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				commands, _ := data["commands"].([]any)
				var builtin []string
				for _, value := range commands {
					command, _ := value.(map[string]any)
					if info, _ := command["sourceInfo"].(map[string]any); info["source"] == "builtin" {
						builtin = append(builtin, command["name"].(string))
					}
				}
				if got := slices.Contains(builtin, "llama"); got != tt.llama {
					t.Fatalf("built-in commands = %v; llama loaded = %v, want %v", builtin, got, tt.llama)
				}
				return true
			})
			p.closeAndWait("the catalog was read")
		})
	}
}
