package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const resumeToolsExtension = `export default function (pi) {
  pi.registerTool({ name: "opt_tool", label: "Opt", description: "Optional tool.", parameters: { type: "object", properties: {} },
    defaultActive: false, execute: async () => ({ content: [{ type: "text", text: "ok" }] }) });
  pi.registerCommand("enable-opt", { description: "enable opt_tool", handler: async () => { pi.setActiveTools([...pi.getActiveTools(), "opt_tool"]); } });
}
`

// pi-mono #10785 (Pi 6f2f164a): a tool enabled during a session (here an optional defaultActive:false tool) is still active after the session is resumed: the transcript's system message
// declares it, and the resumed session takes its tools from there instead of the registration defaults. The denylist still applies to the restored loadout, +name/-name entries
// change it, and an explicit selection (a --tools allowlist, --no-builtin-tools) replaces it. Each step continues the session the previous one left.
func TestRPCResumeKeepsDynamicallyActivatedTools(t *testing.T) {
	root := t.TempDir()
	extension := filepath.Join(root, "opt-tool.mjs")
	if err := os.WriteFile(extension, []byte(resumeToolsExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(root, "sessions")
	run := func(extra []string, steps func(p *rpcProcess)) {
		t.Helper()
		args := append([]string{"--offline", "--model", "test-faux/faux-1", "--system-prompt", "x", "--no-context-files", "--no-skills", "--session-dir", sessionDir, "-e", extension}, extra...)
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + filepath.Join(root, "home")}, args...)
		steps(p)
	}
	// loadout folds the branch's system messages, which record tool changes as toolsAdded/toolsRemoved deltas, into the tools the last request declared.
	loadout := func(p *rpcProcess) []string {
		t.Helper()
		var names []string
		p.sendJSON(map[string]any{"id": "messages", "type": "get_messages"})
		p.await("messages", func(record rpcRecord) bool {
			if record["id"] != "messages" {
				return false
			}
			data, _ := record["data"].(map[string]any)
			messages, _ := data["messages"].([]any)
			names = nil
			for _, message := range messages {
				m, _ := message.(map[string]any)
				if m["role"] != "system" {
					continue
				}
				removed, _ := m["toolsRemoved"].([]any)
				for _, tool := range removed {
					entry, _ := tool.(map[string]any)
					names = slices.DeleteFunc(names, func(name string) bool { return name == entry["name"] })
				}
				added, _ := m["toolsAdded"].([]any)
				for _, tool := range added {
					entry, _ := tool.(map[string]any)
					name, _ := entry["name"].(string)
					if !slices.Contains(names, name) {
						names = append(names, name)
					}
				}
			}
			return true
		})
		slices.Sort(names)
		return names
	}
	prompt := func(p *rpcProcess, id, text string) {
		t.Helper()
		p.sendJSON(map[string]any{"id": id, "type": "prompt", "message": text})
		p.await("agent settled "+id, func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	}
	expect := func(step string, extra []string, want ...string) {
		t.Helper()
		run(extra, func(p *rpcProcess) {
			prompt(p, step, "hello "+step)
			slices.Sort(want)
			if got := loadout(p); !slices.Equal(got, want) {
				t.Fatalf("%s %v: the request declares %v, want %v", step, extra, got, want)
			}
		})
	}
	run(nil, func(p *rpcProcess) {
		// An extension command runs without an agent turn: its prompt response is the end.
		p.sendJSON(map[string]any{"id": "enable", "type": "prompt", "message": "/enable-opt"})
		p.await("enable response", func(record rpcRecord) bool { return record["id"] == "enable" && record["type"] == "response" })
		prompt(p, "chat", "hello")
		if got, want := loadout(p), []string{"bash", "edit", "opt_tool", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("first run: the request declares %v, want %v", got, want)
		}
	})
	expect("resume", []string{"--continue"}, "bash", "edit", "opt_tool", "read", "write")
	// The denylist removes a restored tool and keeps the rest of the transcript's loadout.
	expect("denylist", []string{"--continue", "--exclude-tools", "edit"}, "bash", "opt_tool", "read", "write")
	// --no-builtin-tools starts from the empty default selection, not from the transcript's loadout, so the optional extension tool is off again.
	expect("no builtin tools", []string{"--continue", "--no-builtin-tools"})
	// An explicit allowlist replaces the transcript's loadout, including a tool the transcript never declared.
	expect("allowlist", []string{"--continue", "--tools", "read,grep"}, "grep", "read")
	expect("resume after allowlist", []string{"--continue"}, "grep", "read")
	// +name/-name entries change the transcript's loadout, not the default selection.
	expect("modifiers", []string{"--continue", "--tools", "+bash,-grep"}, "bash", "read")
	expect("resume after modifiers", []string{"--continue"}, "bash", "read")
}
