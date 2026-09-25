package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding"
)

func TestRPCInitialActiveToolsPreserveFullRegistry(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	names := func(tools []agent.AgentTool) []string {
		out := make([]string, 0, len(tools))
		for _, tool := range tools {
			out = append(out, tool.Name())
		}
		return out
	}
	registered := names(session.Tools())
	defaults := []string{"read", "bash", "edit", "write"}
	rpcSetInitialActiveTools(session, defaults)
	if got := names(session.Agent().Tools()); !slices.Equal(got, defaults) {
		t.Fatalf("active %v", got)
	}
	if got := names(session.Tools()); !slices.Equal(got, registered) || !slices.Contains(got, "grep") {
		t.Fatalf("registry shrunk: %v", got)
	}
	rpcSetInitialActiveTools(session, []string{"grep", "find", "ls"})
	if got := names(session.Agent().Tools()); !slices.Equal(got, []string{"grep", "find", "ls"}) {
		t.Fatalf("registered tools unavailable: %v", got)
	}
	if len(session.Messages()) != 0 {
		t.Fatal("startup tool selection persisted a system message")
	}
}

func TestRPCFreshPromptDeclaresStructuredSystemOnce(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session", "--provider", "test-faux", "--model", "faux-1")
	p.send(`{"id":"fresh","type":"get_messages"}`)
	p.await("fresh empty transcript", func(record rpcRecord) bool {
		if record["id"] != "fresh" {
			return false
		}
		data := record["data"].(map[string]any)
		if messages, ok := data["messages"].([]any); !ok || len(messages) != 0 {
			t.Fatalf("fresh transcript %#v", data)
		}
		return true
	})
	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	starts, ends := 0, 0
	p.await("settled prompt", func(record rpcRecord) bool {
		if record["type"] == "message_start" || record["type"] == "message_end" {
			if message, ok := record["message"].(map[string]any); ok && message["role"] == "system" {
				if record["type"] == "message_start" {
					starts++
				} else {
					ends++
				}
			}
		}
		return record["type"] == "agent_settled"
	})
	if starts != 1 || ends != 1 {
		t.Fatalf("system events start=%d end=%d", starts, ends)
	}
	p.send(`{"id":"sent","type":"get_messages"}`)
	p.await("structured transcript", func(record rpcRecord) bool {
		if record["id"] != "sent" {
			return false
		}
		messages := record["data"].(map[string]any)["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("messages %#v", messages)
		}
		system := messages[0].(map[string]any)
		sections, ok := system["sections"].(map[string]any)
		if system["role"] != "system" || system["content"] != "" || !ok {
			t.Fatalf("system %#v", system)
		}
		expected := []string{"preamble", "tools", "rules", "docs", "cwd"}
		if len(sections) != len(expected) {
			t.Fatalf("sections %#v", sections)
		}
		for _, name := range expected {
			if _, ok := sections[name]; !ok {
				t.Fatalf("missing %s section", name)
			}
		}
		added := system["toolsAdded"].([]any)
		names := make([]string, 0, len(added))
		for _, tool := range added {
			names = append(names, tool.(map[string]any)["name"].(string))
		}
		if !slices.Equal(names, []string{"read", "bash", "edit", "write"}) {
			t.Fatalf("declared tools %v", names)
		}
		return true
	})
}

// TestRPCDefaultToolsSettingSelectsInitialTools mirrors upstream sdk.ts:
// settings.defaultTools replaces the default read/bash/edit/write selection
// (CFG-08).
func TestRPCDefaultToolsSettingSelectsInitialTools(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["read","grep"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1"}, "--no-session", "--provider", "test-faux", "--model", "faux-1")
	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	p.await("settled prompt", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"sent","type":"get_messages"}`)
	p.await("tools section", func(record rpcRecord) bool {
		if record["id"] != "sent" {
			return false
		}
		system := record["data"].(map[string]any)["messages"].([]any)[0].(map[string]any)
		tools, _ := system["sections"].(map[string]any)["tools"].(string)
		if !strings.Contains(tools, "grep") || strings.Contains(tools, "- edit") || strings.Contains(tools, "- bash") {
			t.Fatalf("tools section does not follow defaultTools:\n%s", tools)
		}
		return true
	})
}
