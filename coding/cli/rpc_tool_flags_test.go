package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const rpcToolFlagsProbe = `export default function(pi) {
  for (const name of ["ext_a", "ext_b"]) {
    pi.registerTool({name, label: name, description: name, promptSnippet: "snippet for " + name, parameters: {type: "object", properties: {}}, async execute() { return {content: [{type: "text", text: "ran " + name}]} }});
  }
  pi.registerCommand("dump", {description: "dump", handler: async (_args, ctx) => {
    ctx.ui.notify("ACTIVE:" + JSON.stringify(pi.getActiveTools()) + "\nALL:" + JSON.stringify(pi.getAllTools().map((tool) => tool.name)));
  }});
  pi.registerCommand("enable", {description: "enable", handler: async (_args, ctx) => {
    pi.setActiveTools(["bash", "read", "ext_a", "ext_b"]);
    ctx.ui.notify("ACTIVE:" + JSON.stringify(pi.getActiveTools()) + "\nALL:" + JSON.stringify(pi.getAllTools().map((tool) => tool.name)));
  }});
}
`

func rpcToolFlagsProcess(t *testing.T, args ...string) *rpcProcess {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	probe := filepath.Join(cwd, "probe.mjs")
	if err := os.WriteFile(probe, []byte(rpcToolFlagsProbe), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"--no-session", "--no-extensions", "--no-context-files", "--provider", "test-faux", "--model", "faux-1", "-e", probe}
	return startRPCProcessAt(t, cwd, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, append(base, args...)...)
}

// The expected loadouts were measured against the exact Pi 0.87.1 oracle in `--mode rpc` with a probe extension registering ext_a and ext_b and calling pi.getActiveTools()/pi.getAllTools() from a command. Pi's main.ts:532-543 forwards --no-tools, --no-builtin-tools, --tools and --exclude-tools to createAgentSessionFromServices for every mode (main.ts:822-830), and sdk.ts:258-265 derives the allowlist, denylist and initial active names from them.
func TestRPCToolFlagsSelectSameToolsAsPi(t *testing.T) {
	t.Parallel()
	// command is what the probe runs: dump reports the startup selection, enable first asks the extension API to activate bash, read, ext_a and ext_b, which the session registry must still refuse for an excluded or unlisted tool.
	defaults := []string{"read", "bash", "edit", "write", "ext_a", "ext_b"}
	cases := []struct {
		name        string
		command     string
		args        []string
		active, all []string
	}{
		{name: "default", active: defaults, all: []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls", "ext_a", "ext_b"}},
		{name: "exclude builtin and extension", args: []string{"--exclude-tools", "bash,ext_a"}, active: []string{"read", "edit", "write", "ext_b"}, all: []string{"read", "powershell", "edit", "write", "grep", "find", "ls", "ext_b"}},
		{name: "short exclude flag", args: []string{"-xt", "read"}, active: []string{"bash", "edit", "write", "ext_a", "ext_b"}, all: []string{"bash", "powershell", "edit", "write", "grep", "find", "ls", "ext_a", "ext_b"}},
		{name: "activation cannot revive an excluded tool", command: "enable", args: []string{"--exclude-tools", "bash,ext_a"}, active: []string{"read", "ext_b"}, all: []string{"read", "powershell", "edit", "write", "grep", "find", "ls", "ext_b"}},
		{name: "activation stays inside the allowlist", command: "enable", args: []string{"--tools", "read"}, active: []string{"read"}, all: []string{"read"}},
		{name: "allowlist", args: []string{"--tools", "read,ext_a"}, active: []string{"read", "ext_a"}, all: []string{"read", "ext_a"}},
		{name: "allowlist minus excluded", args: []string{"--tools", "read,ext_a", "--exclude-tools", "ext_a"}, active: []string{"read"}, all: []string{"read"}},
		{name: "allowlist entirely excluded", args: []string{"--tools", "read", "--exclude-tools", "read"}, active: []string{}, all: []string{}},
		{name: "no tools", args: []string{"--no-tools"}, active: []string{}, all: []string{}},
		{name: "no tools with exclusion", args: []string{"--no-tools", "--exclude-tools", "bash"}, active: []string{}, all: []string{}},
		{name: "allowlist overrides no tools", args: []string{"--no-tools", "--tools", "bash"}, active: []string{"bash"}, all: []string{"bash"}},
		{name: "no builtin tools", args: []string{"--no-builtin-tools"}, active: []string{"ext_a", "ext_b"}, all: []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls", "ext_a", "ext_b"}},
		{name: "allowlist overrides no builtin tools", args: []string{"--no-builtin-tools", "--tools", "read"}, active: []string{"read"}, all: []string{"read"}},
		{name: "allowlist extension overrides no builtin tools", args: []string{"--no-builtin-tools", "--tools", "ext_b"}, active: []string{"ext_b"}, all: []string{"ext_b"}},
		// args.ts:147-156 assigns a new, possibly empty, list on every --tools and --exclude-tools; main.ts:538-543 forwards any parsed list, so an empty --tools is an empty allowlist and the last occurrence wins.
		{name: "empty allowlist", args: []string{"--tools", ""}, active: []string{}, all: []string{}},
		{name: "allowlist of separators only", args: []string{"--tools", " , "}, active: []string{}, all: []string{}},
		{name: "empty allowlist overrides no builtin tools", args: []string{"--no-builtin-tools", "--tools", ""}, active: []string{}, all: []string{}},
		{name: "last allowlist wins", args: []string{"--tools", "read", "--tools", "ext_a"}, active: []string{"ext_a"}, all: []string{"ext_a"}},
		{name: "last exclusion wins", args: []string{"--exclude-tools", "read", "-xt", "bash"}, active: []string{"read", "edit", "write", "ext_a", "ext_b"}, all: []string{"read", "powershell", "edit", "write", "grep", "find", "ls", "ext_a", "ext_b"}},
		{name: "empty last exclusion clears exclusion", args: []string{"--tools", "read,bash", "--exclude-tools", "read", "--exclude-tools", ""}, active: []string{"read", "bash"}, all: []string{"read", "bash"}},
		{name: "allowlist names are ECMAScript-trimmed", args: []string{"--tools", "\ufeffread\ufeff,\u00a0ext_a"}, active: []string{"read", "ext_a"}, all: []string{"read", "ext_a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := rpcToolFlagsProcess(t, tc.args...)
			command := tc.command
			if command == "" {
				command = "dump"
			}
			p.send(`{"id":"dump","type":"prompt","message":"/` + command + `"}`)
			var message string
			p.await("dump notification", func(record rpcRecord) bool {
				if record["type"] != "extension_ui_request" || record["method"] != "notify" {
					return false
				}
				message, _ = record["message"].(string)
				return strings.HasPrefix(message, "ACTIVE:")
			})
			activeLine, allLine, _ := strings.Cut(message, "\nALL:")
			var active, all []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(activeLine, "ACTIVE:")), &active); err != nil {
				t.Fatalf("active %q: %v", activeLine, err)
			}
			if err := json.Unmarshal([]byte(allLine), &all); err != nil {
				t.Fatalf("all %q: %v", allLine, err)
			}
			if !slices.Equal(active, tc.active) {
				t.Errorf("getActiveTools = %v, want %v", active, tc.active)
			}
			if !slices.Equal(all, tc.all) {
				t.Errorf("getAllTools = %v, want %v", all, tc.all)
			}
		})
	}
}

// The system prompt and the first turn's tool declarations must describe the same tools the session executes: a tool excluded on the command line is neither declared nor callable (agent-session.ts:3147-3150 isAllowedTool).
func TestRPCExcludedToolIsNotDeclaredNorCallable(t *testing.T) {
	p := rpcToolFlagsProcess(t, "--exclude-tools", "bash")
	p.send(`{"id":"run","type":"prompt","message":"Run: expr 20 + 22"}`)
	var result string
	p.await("bash tool result", func(record rpcRecord) bool {
		if record["type"] == "tool_execution_end" && record["toolName"] == "bash" {
			content := record["result"].(map[string]any)["content"].([]any)
			result = content[0].(map[string]any)["text"].(string)
			if record["isError"] != true {
				t.Errorf("excluded bash executed: %v", record)
			}
		}
		return record["type"] == "agent_settled"
	})
	if result != "Tool bash not found" {
		t.Fatalf("bash result %q, want Pi's unknown-tool error", result)
	}
	p.send(`{"id":"sent","type":"get_messages"}`)
	p.await("declared tools", func(record rpcRecord) bool {
		if record["id"] != "sent" {
			return false
		}
		system := record["data"].(map[string]any)["messages"].([]any)[0].(map[string]any)
		var declared []string
		for _, tool := range system["toolsAdded"].([]any) {
			declared = append(declared, tool.(map[string]any)["name"].(string))
		}
		if want := []string{"read", "edit", "write", "ext_a", "ext_b"}; !slices.Equal(declared, want) {
			t.Fatalf("declared tools %v, want %v", declared, want)
		}
		tools, _ := system["sections"].(map[string]any)["tools"].(string)
		if strings.Contains(tools, "- bash") {
			t.Fatalf("system prompt still lists bash:\n%s", tools)
		}
		if !strings.Contains(tools, "- ext_a: snippet for ext_a") || !strings.Contains(tools, "- ext_b: snippet for ext_b") {
			t.Fatalf("system prompt lost extension tools:\n%s", tools)
		}
		return true
	})
}

// An excluded extension tool is left out of the system prompt's tool list as well.
func TestRPCExcludedExtensionToolIsNotInPrompt(t *testing.T) {
	p := rpcToolFlagsProcess(t, "--exclude-tools", "ext_a", "--tools", "read,ext_a,ext_b")
	p.send(`{"id":"run","type":"prompt","message":"hello"}`)
	p.await("settled prompt", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"sent","type":"get_messages"}`)
	p.await("declared tools", func(record rpcRecord) bool {
		if record["id"] != "sent" {
			return false
		}
		system := record["data"].(map[string]any)["messages"].([]any)[0].(map[string]any)
		tools, _ := system["sections"].(map[string]any)["tools"].(string)
		if strings.Contains(tools, "ext_a") || !strings.Contains(tools, "- ext_b: snippet for ext_b") {
			t.Fatalf("system prompt tools:\n%s", tools)
		}
		return true
	})
}

// A replacement Session is built by Pi's createRuntime closure from the same CLI options (main.ts:822-830), so the command-line tool selection survives new_session. Measured against the Pi 0.87.1 oracle.
func TestRPCToolFlagsSurviveSessionReplacement(t *testing.T) {
	p := rpcToolFlagsProcess(t, "--exclude-tools", "bash,ext_a", "--no-builtin-tools", "--tools", "read,ext_b")
	p.send(`{"id":"new","type":"new_session"}`)
	p.await("new session", func(record rpcRecord) bool { return record["id"] == "new" && record["type"] == "response" })
	p.send(`{"id":"dump","type":"prompt","message":"/dump"}`)
	var message string
	p.await("dump notification", func(record rpcRecord) bool {
		if record["type"] != "extension_ui_request" || record["method"] != "notify" {
			return false
		}
		message, _ = record["message"].(string)
		return strings.HasPrefix(message, "ACTIVE:")
	})
	if want := "ACTIVE:[\"read\",\"ext_b\"]\nALL:[\"read\",\"ext_b\"]"; message != want {
		t.Fatalf("replacement Session tools %q, want %q", message, want)
	}
}
