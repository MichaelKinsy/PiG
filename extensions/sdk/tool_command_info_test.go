package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The host answers getAllTools and getCommands with upstream's ToolInfo and
// SlashCommandInfo objects; GetAllTools and GetCommands decode every field.
func TestGetAllToolsAndGetCommandsDecodeUpstreamInfo(t *testing.T) {
	host := newMockHost(t)
	defer host.close()
	var tools []ToolInfo
	var commands []CommandInfo
	ext := New("info-test")
	ext.Command("info", "read tool and command info", func(ctx Context, _ string) error {
		tools = ctx.GetAllTools()
		commands = ctx.GetCommands()
		return nil
	})
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	_ = host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: &readyMsg{Cwd: "/tmp", Width: 80}})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-info", Request: &requestMsg{Method: "command", Tool: "info"}})

	results := map[string]string{
		"getAllTools": `{"tools":[` +
			`{"name":"read","description":"Read a file","parameters":{"type":"object"},"promptGuidelines":["Use read."],"sourceInfo":{"path":"<builtin:read>","source":"builtin","scope":"temporary","origin":"top-level"},"source":"builtin"},` +
			`{"name":"probe","description":"Probe","parameters":{"type":"object","properties":{}},"sourceInfo":{"path":"/x/probe.go","source":"cli","scope":"temporary","origin":"top-level"},"source":"mcp:probe"}]}`,
		"getCommands": `{"commands":[` +
			`{"name":"probe","description":"Probe command","source":"extension","sourceInfo":{"path":"/x/probe.go","source":"cli","scope":"temporary","origin":"top-level"}},` +
			`{"name":"skill:review","source":"skill","sourceInfo":{"path":"/s/SKILL.md","source":"local","scope":"user","origin":"top-level","baseDir":"/s"}}]}`,
	}
	for {
		env := host.readEnvelopeRaw(t)
		if env.Type == msgCall && env.Call != nil {
			if result, ok := results[env.Call.Method]; ok {
				host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: json.RawMessage(result)}})
			}
			continue
		}
		if env.Type == msgResponse {
			break
		}
	}
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	wantTools := []ToolInfo{
		{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`), PromptGuidelines: []string{"Use read."},
			SourceInfo: SourceInfo{Path: "<builtin:read>", Source: "builtin", Scope: "temporary", Origin: "top-level"}, Source: "builtin"},
		{Name: "probe", Description: "Probe", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
			SourceInfo: SourceInfo{Path: "/x/probe.go", Source: "cli", Scope: "temporary", Origin: "top-level"}, Source: "mcp:probe"},
	}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Errorf("GetAllTools() = %+v\nwant %+v", tools, wantTools)
	}
	wantCommands := []CommandInfo{
		{Name: "probe", Description: "Probe command", Source: "extension", SourceInfo: SourceInfo{Path: "/x/probe.go", Source: "cli", Scope: "temporary", Origin: "top-level"}},
		{Name: "skill:review", Source: "skill", SourceInfo: SourceInfo{Path: "/s/SKILL.md", Source: "local", Scope: "user", Origin: "top-level", BaseDir: "/s"}},
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Errorf("GetCommands() = %+v\nwant %+v", commands, wantCommands)
	}
}
