package mcpext_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// Behavior probed against upstream 0.99.1 (`loadMcpConfig`, `addMcpServerConfig`,
// `updateMcpServerConfig` run from the published package): error messages of
// unreadable files and the rewrite of a config file.
func TestLoadMcpConfigReportsUnreadableFilesLikeUpstream(t *testing.T) {
	for _, tc := range []struct{ content, want string }{
		{`{"mcpServers": `, "Unexpected end of JSON input"},
		{`[]`, `expected an object with an "mcpServers" object`},
		{`{"mcpServers":[]}`, `expected an object with an "mcpServers" object`},
		{``, "Unexpected end of JSON input"},
		{`{"a":1,}`, "Expected double-quoted property name in JSON at position 7 (line 1 column 8)"},
		{`nul`, "Unexpected end of JSON input"},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(tc.content), 0o644); err != nil {
			t.Fatal(err)
		}
		loaded := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: dir, Cwd: dir, ConfigDirName: ".pi"})
		if len(loaded.Errors) != 1 || loaded.Errors[0] != filepath.Join(dir, "mcp.json")+": "+tc.want {
			t.Errorf("content %q: errors = %q, want %q", tc.content, loaded.Errors, tc.want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"mcpServers":{"a":{"command":"x"}},"autoEnableCodemode":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: dir, Cwd: dir, ConfigDirName: ".pi"})
	if len(loaded.Servers) != 1 || loaded.AutoEnableCodemode != nil || len(loaded.Errors) != 1 || !strings.HasSuffix(loaded.Errors[0], "autoEnableCodemode must be a boolean") {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestEditingAConfigFileNormalizesItLikeJSONStringify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(path, []byte("{\n    \"note\": 1.0,\n    \"s\": \"\\u00e9\",\n    \"mcpServers\": {\"a\": {\"command\": \"x\", \"n\": 2.50}}\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpext.AddMcpServerConfig(path, "b", extension.McpServerConfig{URL: "https://x.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := mcpext.UpdateMcpServerConfig(path, "a", mcpext.McpServerConfigPatch{Enabled: &disabled, Exposure: extension.McpExposureDirect}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "{\n    \"note\": 1,\n    \"s\": \"é\",\n    \"mcpServers\": {\n        \"a\": {\n            \"command\": \"x\",\n            \"n\": 2.5,\n            \"enabled\": false,\n            \"exposure\": \"direct\"\n        },\n        \"b\": {\n            \"url\": \"https://x.example/mcp\"\n        }\n    }\n}\n"
	if string(data) != want {
		t.Fatalf("file = %q\nwant %q", data, want)
	}
}
