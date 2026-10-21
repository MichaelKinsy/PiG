package subprocess

import (
	"context"
	stdjson "encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func builtinDefinition(t *testing.T, args string) BuiltinToolDefinitionResult {
	t.Helper()
	reply, err := BuiltinToolDefinitionJSON([]byte(args))
	if err != nil {
		t.Fatal(err)
	}
	var out BuiltinToolDefinitionResult
	if err := stdjson.Unmarshal(reply, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// create<Tool>ToolDefinition(cwd, options): each built-in answers with the name, prompt snippet and schemas Pi's definition carries; edit draws its own
// shell; the session guideline of a shell tool follows exposeSessionEnvironment, the one option that changes a definition.
func TestBuiltinToolDefinitionJSONIsPisDefinition(t *testing.T) {
	for _, tool := range []struct{ name, snippet string }{
		{"read", "Read file contents"}, {"bash", "Execute bash commands (ls, grep, find, etc.)"}, {"edit", "Make precise file edits with exact text replacement, including multiple disjoint edits in one call"},
		{"write", "Create or overwrite files"}, {"grep", "Search file contents for patterns (respects .gitignore)"}, {"find", "Find files by glob pattern (respects .gitignore)"},
		{"ls", "List directory contents"}, {"powershell", "Execute PowerShell commands"},
	} {
		def := builtinDefinition(t, `{"name":"`+tool.name+`","cwd":"/unused"}`)
		if def.Name != tool.name || def.Label != tool.name || def.PromptSnippet != tool.snippet || len(def.Parameters) == 0 || def.Description == "" || def.ExecutionMode != "" {
			t.Errorf("%s definition = %+v", tool.name, def)
		}
		if got := def.RenderShell == "self"; got != (tool.name == "edit") {
			t.Errorf("%s renderShell = %q", tool.name, def.RenderShell)
		}
	}
	if def := builtinDefinition(t, `{"name":"bash","cwd":"/c"}`); len(def.PromptGuidelines) != 1 || len(def.OutputSchema) == 0 {
		t.Errorf("bash definition = %+v, want its session guideline and structured output schema", def)
	}
	if def := builtinDefinition(t, `{"name":"bash","cwd":"/c","options":{"exposeSessionEnvironment":false,"operations":["exec"],"spawnHook":true}}`); len(def.PromptGuidelines) != 0 {
		t.Errorf("bash without the session environment keeps guidelines %v", def.PromptGuidelines)
	}
	if _, err := BuiltinToolDefinitionJSON([]byte(`{"name":"nope","cwd":"/c"}`)); err == nil || err.Error() != "Unknown tool name: nope" {
		t.Errorf("unknown tool error = %v, want Pi's message", err)
	}
}

// ctx.cwd, ctx.model, ctx.sessionManager and ctx.thinkingLevel reach a built-in tool as Pi's `ctx` does: from the session the host's callbacks describe.
func TestBuiltinToolContextCarriesTheSession(t *testing.T) {
	h := NewHostWithConfigRoot("/session/cwd", t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetActions(&HostCallbacks{
		GetSessionID:     func() string { return "sid" },
		GetSessionFile:   func() string { return "/s/file.jsonl" },
		GetThinkingLevel: func() string { return "high" },
		GetModelInfo: func() stdjson.RawMessage {
			return stdjson.RawMessage(`{"provider":"prov","id":"mod","input":["text"],"inputLimits":{"images":{"resize":{"maxWidth":10}}}}`)
		},
	})
	h.SetUIBridge(bridge)
	ctx := h.builtinToolContext(context.Background())
	env, ok := agent.ToolEnvironmentFrom(ctx)
	if !ok || env.SessionID != "sid" || env.SessionFile != "/s/file.jsonl" || env.ThinkingLevel != "high" || env.Provider != "prov" || env.Model != "mod" {
		t.Fatalf("environment = %+v", env)
	}
	if env.SupportsImages == nil || *env.SupportsImages || env.InputLimits == nil || env.InputLimits.Images == nil || env.InputLimits.Images.Resize.MaxWidth != 10 {
		t.Fatalf("model profile = %+v", env)
	}
	cwd, err := extension.FromContext(ctx).CWD()
	if err != nil || cwd != "/session/cwd" {
		t.Fatalf("cwd = %q, %v", cwd, err)
	}
}

// BuiltinToolDefinitionResult drops an empty promptGuidelines: Pi's built-in definitions carry none or a non-empty list, so nothing is lost.
func TestBuiltinToolDefinitionsHaveNoEmptyPromptGuidelines(t *testing.T) {
	for _, name := range []string{"read", "write", "edit", "grep", "find", "ls", "bash", "powershell"} {
		for _, exposeEnvironment := range []bool{false, true} {
			args, err := stdjson.Marshal(BuiltinToolDefinitionArgs{Name: name, Cwd: t.TempDir(), Options: &BuiltinToolOptions{ExposeSessionEnvironment: &exposeEnvironment}})
			if err != nil {
				t.Fatal(err)
			}
			reply, err := BuiltinToolDefinitionJSON(args)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var definition struct {
				PromptGuidelines []string `json:"promptGuidelines"`
			}
			if err := stdjson.Unmarshal(reply, &definition); err != nil {
				t.Fatal(err)
			}
			var members map[string]stdjson.RawMessage
			if err := stdjson.Unmarshal(reply, &members); err != nil {
				t.Fatal(err)
			}
			if _, present := members["promptGuidelines"]; !present {
				continue
			}
			if len(definition.PromptGuidelines) == 0 {
				t.Errorf("%s (exposeSessionEnvironment %v): promptGuidelines is present and empty", name, exposeEnvironment)
			}
		}
	}
}

// An operations object with no member is not an absent one: the host builds the tool's callbacks for it.
func TestBuiltinToolOptionsKeepAnEmptyOperationsList(t *testing.T) {
	encoded, err := stdjson.Marshal(BuiltinToolOptions{Operations: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded BuiltinToolOptions
	if err := stdjson.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Operations == nil {
		t.Fatalf("an empty operations list decoded as absent: %s", encoded)
	}
}
