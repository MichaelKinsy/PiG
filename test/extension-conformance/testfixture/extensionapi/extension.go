// Package extensionapi is the Go SDK extension of the Pi 0.99.1 extension API conformance rows. Fused, isolated and packed realizations run this one factory.
package extensionapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Name is the extension's registered name.
const Name = "extension-api"

// Extension returns the fixture extension. Load-time registrations follow upstream's agent-session-tool-orchestration.test.ts orchestrator (echo, helper, run_tools) and add the MCP server, virtual model, provider and event handlers of the same API.
func Extension() *sdk.Extension {
	e := sdk.New(Name)
	var mu sync.Mutex
	var mcpChanges, streamEvents, toolEvents []string

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	// Registered while the factory runs: the host applies them when the extension loads.
	must(e.RegisterMcpServer("docs", sdk.McpServerConfig{
		URL:          "http://docs.invalid/mcp",
		Exposure:     sdk.McpExposureDeferred,
		Description:  "Docs search",
		OAuth:        &sdk.McpOAuthConfig{ClientName: "Conformance Client"},
		ToolExposure: sdk.NewOrderedExposures("search_*", "direct", "*", "hidden"),
		Headers:      sdk.NewOrderedStrings("X-Zed", "z", "X-Alpha", "a"),
	}))
	must(e.RegisterVirtualModel(sdk.VirtualModel{
		Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []string{"off", "high"}, ContextWindow: 200000, MaxTokens: 8192,
		Route: func(_ sdk.Context, request sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
			var prior struct{ Turns int }
			if len(request.State) > 0 {
				if err := json.Unmarshal(request.State, &prior); err != nil {
					return sdk.ModelRoute{}, err
				}
			}
			previous := ""
			if request.Previous != nil {
				previous = fmt.Sprint(request.Previous.Model["id"])
			}
			model := map[string]any{"provider": "anthropic", "id": "opus"}
			level := "high"
			if request.Reason == sdk.ModelRouteReasonRetry {
				model, level = map[string]any{"provider": "anthropic", "id": "haiku"}, "off"
			}
			return sdk.ModelRoute{Model: model, ThinkingLevel: level, State: map[string]any{
				"turns": prior.Turns + 1, "reason": request.Reason, "messages": len(request.Messages), "previous": previous, "selected": request.Model["id"], "level": request.ThinkingLevel,
			}}, nil
		},
	}))
	// An identity router returns the state it was given (agent-session.ts:788 keeps the state then); the fixture's other router always computes a new one.
	must(e.RegisterVirtualModel(sdk.VirtualModel{
		Provider: "router", ID: "identity", Name: "Identity",
		Route: func(_ sdk.Context, request sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
			// Go has no object identity: a router hands back the value it decoded from the request's state, and its encoder's bytes are its own.
			var state any
			if len(request.State) > 0 {
				if err := json.Unmarshal(request.State, &state); err != nil {
					return sdk.ModelRoute{}, err
				}
			}
			return sdk.ModelRoute{Model: map[string]any{"provider": "anthropic", "id": "opus"}, ThinkingLevel: "off", State: state}, nil
		},
	}))
	// A virtual model an earlier-loaded extension queued is removed by this unregistration before the runner binds (loader.ts:228-232).
	e.UnregisterVirtualModel("router", "victim")
	e.RegisterProvider("multi", sdk.ProviderConfig{
		"baseUrl": "http://multi.invalid", "apiKey": "key", "api": "openai-completions",
		"models": []any{
			map[string]any{"id": "chat-1", "name": "Chat", "reasoning": true, "input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096},
			map[string]any{"id": "img-1", "name": "Image", "type": "image", "api": "openrouter-images", "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "output": []string{"image", "text"}},
			map[string]any{"id": "cls-1", "name": "Classifier", "type": "classifier", "api": "llama-cpp-classify", "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 8192},
		},
	})

	object := sdk.Schema{"type": "object"}
	text := sdk.Schema{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}
	e.RegisterTool(sdk.ToolDefinition{
		Name: "echo", Label: "echo", Description: "Echo text.", Parameters: text,
		Execute: func(_ sdk.Context, params map[string]any) (any, error) {
			return "echo: " + fmt.Sprint(params["text"]), nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "helper", Label: "helper", Description: "Only reachable from other tools.", Parameters: object,
		Exposure:     sdk.ToolExposureCodemode,
		OutputSchema: sdk.Schema{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}},
		Namespace:    &sdk.ToolNamespace{Name: "helpers", Description: "Helper tools", Instructions: "Call helper first."},
		Annotations:  &sdk.ToolAnnotations{ReadOnlyHint: sdk.Bool(true), OpenWorldHint: sdk.Bool(false)},
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return sdk.ToolResult{Content: "helped", StructuredContent: map[string]any{"n": 3}, Details: map[string]any{"source": "helper"}}, nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "soft_fail", Label: "soft_fail", Description: "Reports a failure without throwing.", Parameters: object,
		Exposure: sdk.ToolExposureCodemode, DefaultActive: sdk.Bool(false),
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return sdk.ToolResult{Content: "soft failure", IsError: true, Details: map[string]any{"kept": true}, StructuredContent: map[string]any{"n": 0}}, nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "run_tools", Label: "run_tools", Description: "Runs tools.", Parameters: object, Exposure: sdk.ToolExposureModelOnly,
		PrepareLoadout: func(loadout sdk.ToolLoadout) *sdk.ToolLoadoutChanges {
			names := make([]string, len(loadout.Callable))
			for i, tool := range loadout.Callable {
				names[i] = tool.Name
			}
			return &sdk.ToolLoadoutChanges{
				Descriptions:       map[string]string{"run_tools": "Runs tools: " + strings.Join(names, ", "), "echo": "Echo text (also callable from run_tools)."},
				HiddenDeclarations: []string{"echo"},
			}
		},
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			helper, err := ctx.ExecuteTool("helper", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			echo, err := ctx.ExecuteTool("echo", map[string]any{"text": "hi"}, nil)
			if err != nil {
				return nil, err
			}
			self, err := ctx.ExecuteTool("run_tools", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			joined := make([]string, 0, 3)
			for _, outcome := range []sdk.AgentToolCallOutcome{helper, echo, self} {
				joined = append(joined, outcome.Result.Text())
			}
			return strings.Join(joined, " | "), nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "nested_updates", Label: "nested_updates", Description: "Runs a slow nested tool and reports its partial results.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			var partials []string
			outcome, err := ctx.ExecuteTool("slow", nil, &sdk.ExecuteToolOptions{OnUpdate: func(partial sdk.AgentToolResult) { partials = append(partials, partial.Text()) }})
			if err != nil {
				return nil, err
			}
			return strings.Join(append(partials, outcome.Result.Text()), ","), nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "nested_cancel", Label: "nested_cancel", Description: "Waits for a nested call the calling request cancels.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			outcome, err := ctx.ExecuteTool("hang", nil, nil)
			if err != nil {
				return "cancelled: " + err.Error(), nil
			}
			return "outcome: " + outcome.Result.Text(), nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "nested_own_signal", Label: "nested_own_signal", Description: "Runs a nested call under its own signal, which the calling request's cancellation does not reach.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			own, stop := context.WithCancel(context.Background())
			defer stop()
			outcome, err := ctx.ExecuteTool("wait_release", nil, &sdk.ExecuteToolOptions{Signal: own})
			if err != nil {
				return "cancelled: " + err.Error(), nil
			}
			return "outcome: " + outcome.Result.Text(), nil
		},
	})

	e.OnMcpServersChange(func(_ sdk.Context, data map[string]any) (any, error) {
		names := []string{}
		for _, server := range data["servers"].([]any) {
			names = append(names, server.(map[string]any)["name"].(string))
		}
		mu.Lock()
		mcpChanges = append(mcpChanges, strings.Join(names, "+"))
		mu.Unlock()
		return nil, nil
	})
	// tool_call and tool_result events of a nested call carry parentToolCallId (types.ts:1044-1219), and tool_result carries structuredContent.
	e.OnEvent(sdk.EventToolCall, func(_ sdk.Context, data map[string]any) (any, error) {
		mu.Lock()
		toolEvents = append(toolEvents, fmt.Sprintf("tool_call %v %v<-%v", data["toolName"], data["toolCallId"], data["parentToolCallId"]))
		mu.Unlock()
		return nil, nil
	})
	e.OnToolResult(func(_ sdk.Context, data map[string]any) (any, error) {
		structured, _ := json.Marshal(data["structuredContent"])
		mu.Lock()
		toolEvents = append(toolEvents, fmt.Sprintf("tool_result %v<-%v %s", data["toolCallId"], data["parentToolCallId"], structured))
		mu.Unlock()
		if data["parentToolCallId"] == nil {
			return nil, nil
		}
		return map[string]any{"structuredContent": map[string]any{"n": 4}}, nil
	})
	e.OnProviderStreamEvent(func(_ sdk.Context, data map[string]any) (any, error) {
		mu.Lock()
		streamEvents = append(streamEvents, fmt.Sprintf("%v/%v/%v:%v", data["provider"], data["api"], data["model"], data["data"].(map[string]any)["type"]))
		mu.Unlock()
		return nil, nil
	})

	e.RegisterTool(sdk.ToolDefinition{
		Name: "probe_state", Label: "probe_state", Description: "Reports what the extension observes of the host.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			settings, settingsErr := ctx.GetSettings()
			servers, serversErr := ctx.GetMcpServers()
			tools, toolsErr := ctx.Tools()
			report := map[string]any{"settings": settings}
			names := []string{}
			for _, server := range servers {
				names = append(names, server.Name+"@"+server.ExtensionPath)
			}
			callable := []string{}
			for _, tool := range tools {
				callable = append(callable, tool.Name)
			}
			mu.Lock()
			report["mcpServers"], report["callableTools"] = names, callable
			report["mcpChanges"], report["streamEvents"], report["toolEvents"] = append([]string{}, mcpChanges...), append([]string{}, streamEvents...), append([]string{}, toolEvents...)
			mu.Unlock()
			for name, err := range map[string]error{"settings": settingsErr, "mcpServers": serversErr, "callableTools": toolsErr} {
				if err != nil {
					report[name+"Error"] = err.Error()
				}
			}
			encoded, err := json.Marshal(report)
			return string(encoded), err
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "live_tools", Label: "live_tools", Description: "Reads ctx.tools around SetActiveTools.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			names := func() (string, error) {
				tools, err := ctx.Tools()
				if err != nil {
					return "", err
				}
				listed := make([]string, len(tools))
				for i, tool := range tools {
					listed[i] = tool.Name
				}
				return strings.Join(listed, ","), nil
			}
			before, err := names()
			if err != nil {
				return nil, err
			}
			ctx.SetActiveTools([]string{"echo"})
			after, err := names()
			if err != nil {
				return nil, err
			}
			return before + "|" + after, nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "nested_throw", Label: "nested_throw", Description: "Panics in the onUpdate callback of a nested call.", Parameters: object,
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			var seen []string
			_, err := ctx.ExecuteTool("slow", nil, &sdk.ExecuteToolOptions{OnUpdate: func(partial sdk.AgentToolResult) {
				seen = append(seen, partial.Text())
				if len(seen) == 1 {
					panic("callback boom")
				}
			}})
			if err != nil {
				return "rejected: " + err.Error() + " after " + strings.Join(seen, ","), nil
			}
			return "no rejection after " + strings.Join(seen, ","), nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "detached_call", Label: "detached_call", Description: "Starts a nested call on a goroutine and returns once the host reports it pending; params.dir names the directory the test signals through.", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"dir": map[string]any{"type": "string"}}},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			dir, _ := params["dir"].(string)
			go func() {
				outcome, err := ctx.ExecuteTool("wait_release", nil, nil)
				record := "ok:" + outcome.Result.Text()
				if err != nil {
					record = "err:" + err.Error()
				}
				_ = os.WriteFile(filepath.Join(dir, "out"), []byte(record), 0o600)
			}()
			// The call is pending when the test has seen it start and writes started.
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
					return "returned", nil
				}
			}
			return nil, fmt.Errorf("the nested call never started")
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "register_late", Label: "register_late", Description: "Registers an MCP server and a virtual model after load; params.mcp names the server.", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"mcp": map[string]any{"type": "string"}}},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			name, _ := params["mcp"].(string)
			// codemode-deferred is the alias Pi 0.99.2 resolves before it stores the registration (mcp-servers.ts:151-166,192-196; loader.ts:479).
			if err := ctx.RegisterMcpServer(name, sdk.McpServerConfig{Command: "late-server", Args: []string{"--stdio"}, Env: sdk.NewOrderedStrings("B", "1", "A", "2"), Exposure: "codemode-deferred", ToolExposure: sdk.NewOrderedExposures("t*", "codemode-deferred")}); err != nil {
				return "register failed: " + err.Error(), nil
			}
			if err := ctx.RegisterVirtualModel(sdk.VirtualModel{Provider: "router", ID: "late", Name: "Late", Route: func(sdk.Context, sdk.ModelRouteRequest) (sdk.ModelRoute, error) {
				return sdk.ModelRoute{Model: map[string]any{"provider": "anthropic", "id": "sonnet"}, ThinkingLevel: "off"}, nil
			}}); err != nil {
				return "virtual model failed: " + err.Error(), nil
			}
			// The reply of the registration call refreshed the replicated list: this handler sees its own change before the host pushes another state.
			servers, err := ctx.GetMcpServers()
			if err != nil {
				return nil, err
			}
			names := make([]string, len(servers))
			exposure := ""
			for i, server := range servers {
				names[i] = server.Name
				if server.Name == name {
					toolExposure, _ := server.Config.ToolExposure.Get("t*")
					exposure = " exposed " + string(server.Config.Exposure) + "/" + string(toolExposure)
				}
			}
			return "registered " + name + " sees " + strings.Join(names, "+") + exposure, nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "unregister_late", Label: "unregister_late", Description: "Removes what register_late registered.", Parameters: sdk.Schema{"type": "object", "properties": map[string]any{"mcp": map[string]any{"type": "string"}}},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			name, _ := params["mcp"].(string)
			ctx.UnregisterMcpServer(name)
			ctx.UnregisterVirtualModel("router", "late")
			return "unregistered " + name, nil
		},
	})
	return e
}
