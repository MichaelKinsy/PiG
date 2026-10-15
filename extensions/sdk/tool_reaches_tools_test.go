package sdk

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi 1.2.0 types.ts:2122-2130 picks reachesTools into ToolInfo and agent-session.ts:1507 copies the definition's hook, which an
// extension's tool_call handler calls with a call's input. The host marks such a tool `"reachesTools": true`; GetAllTools turns the
// mark into a ReachesTools that runs the hook with the tool.reachesTools call, a tool without the mark has none, and the host's
// error is the hook's throw (a panic, as a definition's hook throws).
func TestGetAllToolsReachesToolsRunsTheHookThroughTheHost(t *testing.T) {
	ext := New("info")
	got := make(chan []ToolInfo, 1)
	reaches := make(chan []ToolReach, 1)
	ext.Command("list", "", func(ctx Context, _ string) error {
		tools, err := ctx.GetAllTools()
		got <- tools
		if err != nil || len(tools) != 2 || tools[0].ReachesTools == nil {
			return err
		}
		reaches <- []ToolReach{tools[0].ReachesTools(map[string]any{"q": 1}), tools[0].ReachesTools(map[string]any{"all": true}), tools[0].ReachesTools(nil)}
		return nil
	})
	ext.Command("fail", "", func(ctx Context, _ string) error {
		tools, err := ctx.GetAllTools()
		if err != nil {
			return err
		}
		tools[0].ReachesTools(map[string]any{})
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	listed := `{"tools":[{"name":"finder","description":"d","parameters":{},"exposure":"direct","reachesTools":true,"sourceInfo":{"path":"/f","source":"s","scope":"user","origin":"top-level"}},{"name":"plain","description":"d","parameters":{},"exposure":"direct","sourceInfo":{"path":"/p","source":"s","scope":"user","origin":"top-level"}}]}`
	calls, resp := runSurfaceCommand(t, host, "list", func(call *callMsg) *callResultMsg {
		switch call.Method {
		case "getAllTools":
			return &callResultMsg{Result: json.RawMessage(listed)}
		case "tool.reachesTools":
			var args struct {
				Input map[string]any `json:"input"`
			}
			_ = json.Unmarshal(call.Args, &args)
			if args.Input["all"] == true {
				return &callResultMsg{Result: json.RawMessage(`"all"`)}
			}
			return &callResultMsg{Result: json.RawMessage(`["docs","search"]`)}
		}
		return &callResultMsg{Error: &errorInfo{Message: "unexpected " + call.Method}}
	})
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	tools := recv(t, got)
	if tools[1].ReachesTools != nil {
		t.Error("plain has a ReachesTools")
	}
	if reached, want := recv(t, reaches), []ToolReach{{Words: []string{"docs", "search"}}, {All: true}, {Words: []string{"docs", "search"}}}; !reflect.DeepEqual(reached, want) {
		t.Errorf("reaches = %+v, want %+v", reached, want)
	}
	var asked []string
	for _, call := range calls[1:] {
		asked = append(asked, call.Method+" "+string(call.Args))
	}
	if want := []string{`tool.reachesTools {"input":{"q":1},"tool":"finder"}`, `tool.reachesTools {"input":{"all":true},"tool":"finder"}`, `tool.reachesTools {"input":{},"tool":"finder"}`}; !reflect.DeepEqual(asked, want) {
		t.Errorf("host calls = %q, want %q", asked, want)
	}
	_, resp = runSurfaceCommand(t, host, "fail", func(call *callMsg) *callResultMsg {
		if call.Method == "getAllTools" {
			return &callResultMsg{Result: json.RawMessage(listed)}
		}
		return &callResultMsg{Error: &errorInfo{Message: "reach exploded"}}
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "reach exploded") {
		t.Errorf("failing hook response = %+v, want the host's error", resp.Error)
	}
}

// Pi 24382bed types.ts reachesTools: the host asks the extension that defines the hook with tool_reaches_tools and the answer is the string
// "all" or the array of words, a tool without the hook does not declare reaches_tools, and a namespace keeps its aliases in the declaration.
func TestToolReachesToolsRunsInTheExtension(t *testing.T) {
	ext := New("reach")
	var seen []map[string]any
	ext.RegisterTool(ToolDefinition{
		Name: "finder", Label: "finder", Description: "Finds", Parameters: Schema{"type": "object"},
		Namespace: &ToolNamespace{Name: "mcp__docs", Aliases: NewToolNamespaceAliases("docs")},
		ReachesTools: func(input map[string]any) ToolReach {
			seen = append(seen, input)
			switch {
			case input["fail"] == true:
				panic("reach exploded")
			case input["all"] == true:
				return ToolReach{All: true}
			case input["none"] == true:
				return ToolReach{}
			}
			return ToolReach{Words: []string{"docs", "search"}}
		},
		Execute: func(Context, map[string]any) (any, error) { return "ok", nil },
	})
	ext.RegisterTool(ToolDefinition{Name: "plain", Label: "plain", Description: "Plain", Parameters: Schema{"type": "object"}, Execute: func(Context, map[string]any) (any, error) { return "ok", nil }})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)

	var tools []map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, reg.Tools)), &tools); err != nil {
		t.Fatal(err)
	}
	if tools[0]["reaches_tools"] != true {
		t.Fatalf("finder declaration = %#v, want reaches_tools", tools[0])
	}
	if _, present := tools[1]["reaches_tools"]; present {
		t.Fatalf("plain declaration = %#v, want no reaches_tools", tools[1])
	}
	if got := tools[0]["namespace"]; !reflect.DeepEqual(got, map[string]any{"name": "mcp__docs", "aliases": []any{"docs"}}) {
		t.Fatalf("namespace = %#v", got)
	}

	for _, tc := range []struct{ id, args, want string }{
		{"reach-1", `{"q":"x"}`, `["docs","search"]`},
		{"reach-2", `{"all":true}`, `"all"`},
		{"reach-3", `{"none":true}`, `[]`},
	} {
		_, resp := runSurfaceRequest(t, host, tc.id, &requestMsg{Method: "tool_reaches_tools", Tool: "finder", Args: json.RawMessage(tc.args)}, nil)
		if resp.Error != nil || string(resp.Result) != tc.want {
			t.Errorf("%s: answer = %s, %+v; want %s", tc.id, resp.Result, resp.Error, tc.want)
		}
	}
	_, resp := runSurfaceRequest(t, host, "reach-4", &requestMsg{Method: "tool_reaches_tools", Tool: "finder", Args: json.RawMessage(`{"fail":true}`)}, nil)
	if resp.Error == nil || resp.Error.Message != "reach exploded" {
		t.Errorf("error = %+v, want the hook's \"reach exploded\"", resp.Error)
	}
	for i, tool := range []string{"plain", "missing"} {
		if _, resp := runSurfaceRequest(t, host, fmt.Sprintf("reach-%d", 5+i), &requestMsg{Method: "tool_reaches_tools", Tool: tool, Args: json.RawMessage(`{}`)}, nil); resp.Error == nil {
			t.Errorf("a tool_reaches_tools request for %s succeeded", tool)
		}
	}
	if len(seen) != 4 || seen[0]["q"] != "x" {
		t.Errorf("hook inputs = %v", seen)
	}
}
