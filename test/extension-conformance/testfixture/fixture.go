// Command sdk-fixture is a minimal SDK-based extension for integration
// testing. Unlike testdata/fixture-ext/ (raw protocol), this uses the Go
// SDK to verify the SDK→Host wire format round-trip end-to-end.
//
// The wire format mismatch bug (schema vs parameters, []string vs
// []HandlerDecl) was only caught in production because no integration
// test exercised the real SDK path. This fixture exists to close that gap.
package testfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"

	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

type focusedList struct {
	items    []string
	selected int
	disposed bool
}

func (l *focusedList) Render(width int) []string {
	lines := []string{fmt.Sprintf("focused width=%d", width)}
	for i, item := range l.items {
		prefix := "  "
		if i == l.selected {
			prefix = "> "
		}
		lines = append(lines, prefix+item)
	}
	return lines
}

func (l *focusedList) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	if data == "j" {
		return sdk.RemoteComponentResult{Done: true, Value: make(chan int)}, nil
	}
	if data == "e" {
		return sdk.RemoteComponentResult{}, errors.New("focused input failed")
	}
	switch data {
	case "\x1b[A":
		l.selected = (l.selected - 1 + len(l.items)) % len(l.items)
	case "\x1b[B":
		l.selected = (l.selected + 1) % len(l.items)
	case "\x1b[6~":
		l.selected = min(l.selected+2, len(l.items)-1)
	case "\r", "\n":
		return sdk.RemoteComponentResult{Done: true, Value: l.items[l.selected]}, nil
	case "\x1b":
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (l *focusedList) Dispose() { l.disposed = true }

type timerFocused struct {
	mu         sync.Mutex
	frame      int
	invalidate func()
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	disposed   bool
}

func newTimerFocused() *timerFocused {
	component := &timerFocused{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(component.done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				component.mu.Lock()
				component.frame++
				invalidate := component.invalidate
				component.mu.Unlock()
				if invalidate != nil {
					invalidate()
				}
			case <-component.stop:
				return
			}
		}
	}()
	return component
}

func (c *timerFocused) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []string{fmt.Sprintf("timer frame=%d width=%d", c.frame, width)}
}

func (c *timerFocused) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if data == "\r" {
		return sdk.RemoteComponentResult{Done: true, Value: c.frame}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func (c *timerFocused) SetInvalidate(invalidate func()) {
	c.mu.Lock()
	c.invalidate = invalidate
	c.mu.Unlock()
}

func (c *timerFocused) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
	c.mu.Lock()
	c.disposed = true
	c.mu.Unlock()
}

func (c *timerFocused) Disposed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disposed
}

func (c *timerFocused) Detached() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidate == nil
}

func modelConformanceContext() map[string]any {
	return map[string]any{
		"systemPrompt": "conformance-system",
		"messages": []any{
			map[string]any{
				"role":      "system",
				"content":   []any{map[string]any{"type": "text", "text": "signed system", "textSignature": "system-signature"}},
				"sections":  json.RawMessage(`{"zeta":"last-first","alpha":null,"middle":"middle"}`),
				"timestamp": 41,
			},
			map[string]any{"role": "user", "content": "hello", "timestamp": 42},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "prior", "textSignature": "signed"}}, "api": "openai-responses", "provider": "prior-provider", "model": "prior-model", "usage": map[string]any{"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "totalTokens": 10, "cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "stop", "timestamp": 43},
		},
		"tools": []any{map[string]any{
			"name": "lookup", "description": "lookup", "parameters": map[string]any{"type": "object"},
			"constrainedSampling": map[string]any{"type": "grammar", "variants": map[string]any{"openai_lark": "start: NUMBER"}},
		}},
	}
}

func modelConformanceOptions() map[string]any {
	return map[string]any{
		"timeoutMs": 0, "websocketConnectTimeoutMs": 1234, "maxRetries": 2, "maxRetryDelayMs": 3000,
		"maxTokens": 321, "temperature": 0.65, "samplingParams": map[string]any{"topP": 0.8},
		"thinkingBudgets": map[string]any{"minimal": 11, "low": 22, "medium": 33, "high": 44}, "reasoning": "high", "isReasoning": true,
		"env": map[string]any{"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"}, "headers": map[string]any{"X-Wire": "yes", "X-Remove": nil}, "sessionId": "conformance-session", "transport": "sse",
	}
}

// Extension returns the canonical Go SDK fixture shared by subprocess and
// fused conformance harnesses.
func Extension() *sdk.Extension {
	ext := sdk.New("sdk-fixture")
	registerAutocompleteFixture(ext)
	schemaRejected := RejectInvalidToolSchema(ext)
	ext.Command("schema-probe", "Report schema rejection", func(ctx sdk.Context, _ string) error {
		ctx.Notify(fmt.Sprintf("schema-rejected:%t", schemaRejected), "info")
		return nil
	})
	ext.Flag("flag-true", sdk.FlagOptions{Type: sdk.FlagBoolean, Default: true})
	ext.Flag("flag-false", sdk.FlagOptions{Type: sdk.FlagBoolean, Default: false})
	ext.Flag("flag-string", sdk.FlagOptions{Type: sdk.FlagString, Default: "default"})
	ext.Flag("flag-empty", sdk.FlagOptions{Type: sdk.FlagString, Default: ""})
	ext.Flag("flag-unset", sdk.FlagOptions{Type: sdk.FlagString})
	ext.Command("flag-probe", "Report registered flag values", func(ctx sdk.Context, _ string) error {
		values := []any{}
		for _, name := range []string{"flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"} {
			value, err := ctx.GetFlag(name)
			if err != nil {
				return err
			}
			values = append(values, value)
		}
		wire, err := json.Marshal(values)
		if err != nil {
			return err
		}
		ctx.Notify(string(wire), "info")
		return nil
	})
	ext.Command("timeout-probe", "Send JavaScript-number timeouts", func(ctx sdk.Context, _ string) error {
		for _, ms := range []float64{0.5, 4294967296.5, 1e21} {
			if _, err := ctx.ExecWithOptions("timeout-command", nil, sdk.ExecOptions{Timeout: ms}); err != nil {
				return err
			}
		}
		_, _, err := ctx.SelectWithOptions("timeout", []string{"a"}, sdk.DialogOptions{Timeout: 1500.5})
		return err
	})
	ext.Command("exec-reject-probe", "Report exec outcomes", func(ctx sdk.Context, args string) error {
		var commands []string
		if err := json.Unmarshal([]byte(args), &commands); err != nil {
			return err
		}
		for _, command := range commands {
			result, err := ctx.Exec(command, nil)
			switch {
			case err != nil:
				ctx.Notify("rejected:"+err.Error(), "info")
			case result == nil:
				ctx.Notify("resolved:none", "info")
			default:
				ctx.Notify("resolved:"+strconv.Itoa(result.ExitCode), "info")
			}
		}
		return nil
	})
	ext.Command("session-identity", "Read context identity accessors", func(ctx sdk.Context, _ string) error {
		id, err := ctx.GetSessionID()
		if err != nil {
			return err
		}
		file, err := ctx.GetSessionFile()
		if err != nil {
			return err
		}
		leaf, err := ctx.GetLeafID()
		if err != nil {
			return err
		}
		name, err := ctx.GetSessionName()
		if err != nil {
			return err
		}
		data, err := json.Marshal([]*string{&id, file, leaf, name})
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("registry-session", "Read registry and session facades", registrySessionProbe)
	ext.MessageRenderer("conformance-message", func(_ sdk.Context, message map[string]any, options sdk.MessageRenderOptions, width int) ([]string, error) {
		if message["content"] == "padding-options" {
			wire, err := json.Marshal(options)
			return []string{string(wire)}, err
		}
		return []string{fmt.Sprintf("renderer:%v:expanded=%t:width=%d", message["content"], options.Expanded, width)}, nil
	})
	ext.EntryRenderer("conformance-entry", func(_ sdk.Context, entry map[string]any, options sdk.EntryRenderOptions, width int) ([]string, error) {
		return []string{fmt.Sprintf("entryrenderer:%v:expanded=%t:width=%d", entry["data"], options.Expanded, width)}, nil
	})
	ext.MarkdownTransformer(func(markdown string, context sdk.MarkdownTransformContext) string {
		return fmt.Sprintf("md:%s:%s:streaming=%t:width=%d", markdown, context.MessageType, context.IsStreaming, context.AvailableWidth)
	})
	ext.Tool("render_probe", "Render its own tool card", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(sdk.Context, map[string]any) (any, error) {
		return "render ok", nil
	})
	ext.SetToolRenderers("render_probe", sdk.ToolRenderers{
		Shell: sdk.ToolRenderShellSelf,
		Call: func(_ sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			calls, _ := render.State["calls"].(int)
			render.State["calls"] = calls + 1
			return []string{fmt.Sprintf("toolrender:call:%v:partial=%t:calls=%d:width=%d", args["topic"], render.IsPartial, calls+1, width)}, nil
		},
		Result: func(_ sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			details, _ := result.Details.(map[string]any)
			return []string{fmt.Sprintf("toolrender:result:%v:%v:expanded=%t:calls=%v:width=%d", result.Content[0]["text"], details["k"], options.Expanded, render.State["calls"], width)}, nil
		},
	})

	ext.Tool("echo", "Echo back the input", sdk.Schema{
		"type":     "object",
		"required": []string{"text"},
		"properties": map[string]any{
			"offset": map[string]any{"type": "number"},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to echo",
			},
		},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		text, _ := params["text"].(string)
		return map[string]string{"content": "echo: " + text}, nil
	})

	ext.Tool("update_tool", "Stream two partial results", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate("step 1"); err != nil {
			return nil, err
		}
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "step 2"}); err != nil {
			return nil, err
		}
		return "done", nil
	})

	ext.Tool("ordered_details", "Return details whose members are not in alphabetical order", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		details := json.RawMessage(`{"zeta":1,"alpha":{"yy":2,"bb":3},"mid":[{"qq":1,"aa":2}]}`)
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "partial", Details: details}); err != nil {
			return nil, err
		}
		return sdk.ToolResult{Content: "done", Details: details}, nil
	})

	// The Go SDK writes a result from a struct, which has one member order (content, details, isError): not a row of the member-order test, but the same tool set as every other fixture.
	ext.Tool("ordered_result", "Return a result whose members are not in the declared order", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate(sdk.ToolResult{Content: "partial", Details: map[string]any{"k": 1}}); err != nil {
			return nil, err
		}
		return sdk.ToolResult{Content: "done", Details: map[string]any{"k": 1}, IsError: true}, nil
	})

	var abortObserved atomic.Bool
	ext.Tool("abort_tool", "Wait for the abort signal", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.OnUpdate("waiting"); err != nil {
			return nil, err
		}
		<-ctx.Done()
		abortObserved.Store(true)
		return "aborted", nil
	})
	ext.Command("abort_probe", "Report whether abort_tool saw its abort signal", func(ctx sdk.Context, args string) error {
		ctx.Notify(fmt.Sprintf("abort:%t", abortObserved.Load()), "info")
		return nil
	})

	ext.Tool("rich_tool", "Return text, image, and terminate", sdk.Schema{"type": "object", "properties": map[string]any{}}, func(sdk.Context, map[string]any) (any, error) {
		return map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "  padded  "},
				{"type": "image", "data": "aW1n", "mimeType": "image/png"},
				{"type": "text", "text": "tail\n"},
			},
			"terminate": true,
		}, nil
	})

	ext.ToolWithPrepareArguments("prepared_tool", "Transform legacy arguments before execution", sdk.Schema{
		"type": "object", "required": []string{"text"}, "properties": map[string]any{"text": map[string]any{"type": "string"}},
	}, func(params map[string]any) (map[string]any, error) {
		return map[string]any{"text": params["legacy"]}, nil
	}, func(_ sdk.Context, params map[string]any) (any, error) {
		text, _ := params["text"].(string)
		return map[string]string{"content": "prepared:" + text}, nil
	})

	// Reports the order in which calls start: the number of calls that started before it, plus its own argument.
	var startedCalls atomic.Int64
	ext.Tool("start_order", "Report the order in which calls start", sdk.Schema{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}}, func(_ sdk.Context, params map[string]any) (any, error) {
		number := startedCalls.Add(1)
		return map[string]string{"content": fmt.Sprintf("start#%d n=%v", number, params["n"])}, nil
	})

	ext.Tool("tool_error", "Return a thrown tool error", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return nil, fmt.Errorf("tool exploded")
	})

	ext.Tool("tool_is_error", "Return a structured tool error result", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "soft tool error", "is_error": true}, nil
	})

	ext.ToolWithGuidelines("guided_tool", "Tool with prompt guidelines", sdk.Schema{"type": "object"},
		[]string{"Use guided_tool when the user asks for guided behavior."},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "guided"}, nil
		})

	ext.ToolPromptSnippet("guided_tool", " \ufeffGuided\r\n tool\t summary ")

	ext.ToolWithSource("sourced_tool", "Tool with explicit source", sdk.Schema{"type": "object"},
		"mcp:test-server",
		[]string{"Use sourced_tool to test per-tool source attribution."},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "sourced"}, nil
		})

	ext.RegisterTool(sdk.ToolDefinition{Name: "sampling_disabled", Label: "sampling_disabled", Description: "Disable constrained sampling", Parameters: sdk.Schema{"type": "object"}, ConstrainedSampling: sdk.DisabledConstrainedSampling{}, Execute: func(sdk.Context, map[string]any) (any, error) { return "disabled", nil }})
	ext.ToolWithConstrainedSampling("grammar_tool", "Tool with a grammar constrained sampling request", sdk.Schema{"type": "object"},
		sdk.ConstrainedSampling{Type: "grammar", Variants: map[string]string{"openai_lark": "start: NUMBER"}},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "grammar"}, nil
		})

	ext.RegisterCommand("complete_probe", sdk.CommandOptions{
		Description: "Complete its arguments",
		GetArgumentCompletions: func(prefix string) ([]sdk.AutocompleteItem, error) {
			var items []sdk.AutocompleteItem
			for _, item := range []sdk.AutocompleteItem{{Value: "alpha", Label: "alpha — first"}, {Value: "apple", Description: "fruit"}, {Value: "beta"}} {
				if strings.HasPrefix(item.Value, strings.TrimSpace(prefix)) {
					items = append(items, item)
				}
			}
			return items, nil
		},
		Handler: func(sdk.Context, string) error { return nil },
	})
	ext.Command("ping", "Respond with pong", func(ctx sdk.Context, args string) error {
		ctx.Notify("pong", "info")
		return nil
	})

	ext.Command("liveness_host_call", "Exercise an awaited host call", func(ctx sdk.Context, _ string) error {
		return ctx.WaitForIdle()
	})
	ext.Command("liveness_user_call", "Exercise an interactive host call", func(ctx sdk.Context, _ string) error {
		_, _, err := ctx.Input("Question", "Answer")
		return err
	})
	ext.Command("liveness_fire_call", "Exercise a no-result UI host call", func(ctx sdk.Context, _ string) error {
		ctx.SetTitle("Conformance title")
		return nil
	})

	ext.Command("command_error", "Return a command error", func(ctx sdk.Context, args string) error {
		return fmt.Errorf("command exploded")
	})

	ext.Command("command_awaited_error", "Return an error after awaited work", func(ctx sdk.Context, args string) error {
		time.Sleep(150 * time.Millisecond)
		return fmt.Errorf("awaited command exploded")
	})

	var termUnsub func()
	ext.Command("term_subscribe", "Subscribe to raw terminal input", func(ctx sdk.Context, args string) error {
		unsub, err := ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
			switch data {
			case "\xed\xa0\xbd", "\xed\xb8\x80", "😀":
				rewritten := "seen:" + data
				return sdk.TerminalInputResult{Data: &rewritten}
			case "\x1b[96~":
				text, textErr := ctx.GetEditorText()
				expanded, expandedErr := ctx.GetToolsExpanded()
				if textErr != nil || expandedErr != nil {
					text, expanded = "error", false
				}
				encoded, _ := jsjson.Marshal([]any{text, expanded})
				state := string(encoded)
				return sdk.TerminalInputResult{Data: &state}
			case "\x1b[98~":
				rewritten := "rewritten"
				return sdk.TerminalInputResult{Data: &rewritten}
			case "\x1b[97~":
				time.Sleep(200 * time.Millisecond)
				return sdk.TerminalInputResult{Consume: true}
			}
			return sdk.TerminalInputResult{Consume: data == "\x1b[99~"}
		})
		if err != nil {
			return err
		}
		termUnsub = unsub
		return nil
	})
	ext.Command("term_unsubscribe", "Release the raw input subscription", func(ctx sdk.Context, args string) error {
		if termUnsub != nil {
			termUnsub()
			termUnsub = nil
		}
		return nil
	})
	ext.Command("report_geometry", "Report observed terminal geometry", func(ctx sdk.Context, args string) error {
		ctx.Notify(fmt.Sprintf("geometry:%dx%d", ctx.Width(), ctx.Height()), "info")
		return nil
	})
	// A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
	var widthProbeUnsub func()
	ext.Command("arm_width_probe", "Notify from a width handler", func(ctx sdk.Context, args string) error {
		if widthProbeUnsub != nil {
			return nil
		}
		unsub, err := ctx.OnWidthChange(func(c sdk.Context, width int) {
			c.Notify(fmt.Sprintf("width-probe:%d", width), "info")
			c.Notify(fmt.Sprintf("width-probe-returned:%d", width), "info")
		})
		widthProbeUnsub = unsub
		return err
	})
	ext.Command("surface_footer", "Install a footer renderer", func(ctx sdk.Context, args string) error {
		return ctx.SetFooterRenderer(func(width int) []string { return []string{fmt.Sprintf("footer@%d", width)} })
	})
	ext.Command("surface_header", "Install a header renderer", func(ctx sdk.Context, args string) error {
		return ctx.SetHeaderRenderer(func(width int) []string { return []string{fmt.Sprintf("header@%d", width)} })
	})
	ext.Command("surface_static_footer", "Push static footer rows", func(ctx sdk.Context, args string) error {
		return ctx.SetFooter([]string{fmt.Sprintf("static@%d", ctx.Width())})
	})
	ext.Command("surface_widget", "Set a string list widget wider than the pane", func(ctx sdk.Context, args string) error {
		return ctx.SetWidget("wide", []string{strings.Repeat("A", 60) + " tail", "short"})
	})
	ext.Command("status", "Set a status entry", func(ctx sdk.Context, args string) error {
		ctx.SetStatus("conformance", "ok")
		return nil
	})

	ext.Command("status_burst", "Set one status repeatedly without awaiting", func(ctx sdk.Context, args string) error {
		for i := range 200 {
			ctx.SetStatus("burst", strconv.Itoa(i))
		}
		return nil
	})

	ext.Command("send_message", "Send a custom message", func(ctx sdk.Context, args string) error {
		trigger := true
		return ctx.SendMessage("notice", "hello-custom", true, sdk.SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "steer"})
	})
	ext.Command("send_message_default", "Send a custom message with default options", func(ctx sdk.Context, args string) error {
		return ctx.SendMessage("notice", "default", true, sdk.SendMessageOptions{})
	})
	ext.Command("send_message_no_turn", "Send a custom message that never starts a turn", func(ctx sdk.Context, args string) error {
		trigger := false
		return ctx.SendMessage("notice", "no-turn", true, sdk.SendMessageOptions{TriggerTurn: &trigger})
	})

	ext.Command("send_user_message", "Send a user message", func(ctx sdk.Context, args string) error {
		var content any = "hello-user"
		if args != "" {
			if err := json.Unmarshal([]byte(args), &content); err != nil {
				return err
			}
		}
		return ctx.SendUserMessage(content, "followUp")
	})

	ext.Command("set_session_name", "Set the session name", func(ctx sdk.Context, args string) error {
		return ctx.SetSessionName("conformance-session")
	})

	ext.Command("append_entry", "Append a custom entry", func(ctx sdk.Context, args string) error {
		return ctx.AppendEntry("conformance-entry", "hello-entry")
	})

	ext.Command("sprite-probe", "Exercise sprite registration and host errors", func(ctx sdk.Context, args string) error {
		definition := conformanceSpriteDefinition()
		if err := ctx.RegisterSprite(definition); err != nil {
			return err
		}
		definition.Mascot[0] = definition.Mascot[0][:15]
		err := ctx.RegisterSprite(definition)
		if err == nil {
			return errors.New("invalid sprite definition was accepted")
		}
		ctx.Notify(err.Error(), "error")
		return nil
	})

	ext.Command("login-probe", "Exercise semantic login submission and host errors", func(ctx sdk.Context, args string) error {
		definition := conformanceLoginDefinition()
		if err := ctx.SetLogin(definition); err != nil {
			return err
		}
		definition.Brand[0] = definition.Brand[0][:40]
		err := ctx.SetLogin(definition)
		if err == nil {
			return errors.New("invalid login definition was accepted")
		}
		ctx.Notify(err.Error(), "error")
		return nil
	})

	ext.Command("scoped-models-probe", "Report the model scope", func(ctx sdk.Context, _ string) error {
		models, err := ctx.ScopedModels()
		if err != nil {
			return err
		}
		data, err := json.Marshal(models)
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})
	ext.Command("ui-availability", "Probe bound UI and headless defaults", func(ctx sdk.Context, _ string) error {
		selected, _, err := ctx.Select("Pick", []string{"first", "second"})
		if err != nil {
			return err
		}
		ctx.Notify("availability-notify", "info")
		if !ctx.HasUI() {
			if err := probeNoUI(ctx); err != nil {
				return err
			}
		}
		return ctx.AppendEntry("ui-availability", fmt.Sprintf("hasUI=%t selected=%s", ctx.HasUI(), selected))
	})

	ext.Command("ui-state-barrier", "Read UI state after a dialog", func(ctx sdk.Context, _ string) error {
		selected, _, err := ctx.Select("expand", []string{"chosen"})
		if err != nil {
			return err
		}
		named, err := ctx.GetTheme("light")
		if err != nil {
			return err
		}
		missing, err := ctx.GetTheme("missing")
		if err != nil {
			return err
		}
		success, message := ctx.SetTheme("missing")
		expanded, err := ctx.GetToolsExpanded()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("selected=%s expanded=%t named=%s missing=%t success=%t error=%s", selected, expanded, named.(map[string]any)["name"], missing == nil, success, message), "info")
		return nil
	})

	// Upstream ctx.signal (runner.ts:917-920) is the active run's signal, undefined while no run is active.
	ext.Command("signal-probe", "Report ctx.signal", func(ctx sdk.Context, _ string) error {
		signal := ctx.Signal()
		state := "none"
		if signal != nil {
			state = "live"
			if signal.Err() != nil {
				state = "aborted"
			}
		}
		ctx.Notify("signal:"+state, "info")
		return nil
	})
	ext.Command("signal-wait", "Wait for ctx.signal to abort", func(ctx sdk.Context, _ string) error {
		signal := ctx.Signal()
		if signal == nil {
			ctx.Notify("wait:none", "info")
			return nil
		}
		ctx.Notify("wait:start", "info")
		select {
		case <-signal.Done():
			ctx.Notify("wait:aborted", "info")
		case <-time.After(10 * time.Second):
			ctx.Notify("wait:timeout", "info")
		}
		return nil
	})
	// signal-poll reads ctx.signal the way a timer that fires between requests does: no request reaches the runtime while it waits for the state in args ("live" or "none").
	ext.Command("signal-poll", "Poll ctx.signal until it is live or none", func(ctx sdk.Context, args string) error {
		ctx.Notify("poll:start", "info")
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if (ctx.Signal() != nil) == (args == "live") {
				ctx.Notify("poll:"+args, "info")
				return nil
			}
			time.Sleep(5 * time.Millisecond)
		}
		ctx.Notify("poll:timeout", "info")
		return nil
	})
	ext.Command("usage-probe", "Report context usage", func(ctx sdk.Context, _ string) error {
		usage, err := ctx.GetContextUsage()
		if err != nil {
			return err
		}
		data, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		ctx.Notify(string(data), "info")
		return nil
	})

	ext.Command("context-probe", "Report ctx.mode + ctx.getSystemPromptOptions()", func(ctx sdk.Context, args string) error {
		opts, err := ctx.GetSystemPromptOptions()
		if err != nil {
			return err
		}
		trusted, err := ctx.IsProjectTrusted()
		if err != nil {
			return err
		}
		skillSource := ""
		if len(opts.Skills) > 0 {
			skillSource = opts.Skills[0].SourceInfo.Scope
		}
		// Upstream normalizes every collection to a present value; nil means the wire omitted it.
		count := func(present bool, kind string, n int) string {
			if !present {
				return "absent"
			}
			return fmt.Sprintf("%s:%d", kind, n)
		}
		shapes := strings.Join([]string{
			"selectedTools:" + count(opts.SelectedTools != nil, "array", len(opts.SelectedTools)),
			"toolSnippets:" + count(opts.ToolSnippets != nil, "object", len(opts.ToolSnippets)),
			"toolGuidelines:" + count(opts.ToolGuidelines != nil, "object", len(opts.ToolGuidelines)),
			"promptGuidelines:" + count(opts.PromptGuidelines != nil, "array", len(opts.PromptGuidelines)),
			fmt.Sprintf("appendSystemPrompt:string:%d", len(utf16.Encode([]rune(opts.AppendSystemPrompt)))),
			"sections:" + count(opts.Sections != nil, "object", func() int {
				if opts.Sections == nil {
					return 0
				}
				return len(*opts.Sections)
			}()),
			"contextFiles:" + count(opts.ContextFiles != nil, "array", len(opts.ContextFiles)),
			"skills:" + count(opts.Skills != nil, "array", len(opts.Skills)),
		}, ",")
		ctx.Notify(fmt.Sprintf("mode=%s trusted=%t spo_prompt=%s spo_cwd=%s spo_tools=%s spo_shape=%s spo_guidelines=%s spo_skill_scope=%s spo_force_empty=%t spo_custom_present=%t",
			ctx.Mode(), trusted, opts.CustomPrompt, opts.Cwd, strings.Join(opts.SelectedTools, ","), shapes, strings.Join(opts.ToolGuidelines["read"], ","), skillSource, opts.ForceSystemPrompt != nil && *opts.ForceSystemPrompt == "", opts.CustomPromptSet || opts.CustomPrompt != ""), "info")
		return nil
	})

	ext.Command("session-log-probe", "Read a paged session log", func(ctx sdk.Context, _ string) error {
		entries, err := ctx.GetEntries()
		if err != nil {
			return err
		}
		branch, err := ctx.GetBranch()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("session entries=%d branch=%d", len(entries), len(branch)), "info")
		return nil
	})

	ext.Command("dialog-probe", "Exercise interactive dialog responses", func(ctx sdk.Context, args string) error {
		selected, _, selectErr := ctx.Select("Pick", []string{"first", "second"})
		input, _, inputErr := ctx.Input("Input", "placeholder")
		edited, _, editorErr := ctx.Editor("Editor", "prefill")
		confirmed, confirmErr := ctx.Confirm("Confirm", "message")
		if selectErr != nil || inputErr != nil || editorErr != nil || confirmErr != nil {
			return errors.Join(selectErr, inputErr, editorErr, confirmErr)
		}
		ctx.Notify(fmt.Sprintf("select=%s input=%s editor=%s confirm=%t",
			selected, input, edited, confirmed), "info")
		return nil
	})

	ext.Command("focused-probe", "Exercise focused subprocess UI", func(ctx sdk.Context, args string) error {
		component := &focusedList{items: []string{"alpha", "beta", "gamma"}}
		selected, err := ctx.Custom(component, sdk.RemoteOverlayOptions{Title: "Focused", WidthFraction: 0.5, HeightFraction: 0.5})
		if err != nil {
			return err
		}
		selectedText := ""
		if selected != nil {
			selectedText = fmt.Sprint(selected)
		}
		ctx.Notify(fmt.Sprintf("focused=%s disposed=%t", selectedText, component.disposed), "info")
		return nil
	})

	ext.Command("timer-focused-probe", "Exercise timer-driven focused UI", func(ctx sdk.Context, args string) error {
		component := newTimerFocused()
		frame, err := ctx.Custom(component, sdk.RemoteOverlayOptions{Title: "Timer"})
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("timer=%v disposed=%t detached=%t", frame, component.Disposed(), component.Detached()), "info")
		return nil
	})

	ext.Command("model-stream-probe", "Exercise model streaming", func(ctx sdk.Context, _ string) error {
		registry := ctx.ModelRegistry()
		current := registry.Find("conformance", "current")
		if current == nil || fmt.Sprint(current["id"]) != "current" {
			return fmt.Errorf("find current = %#v", current)
		}
		found := registry.Find("conformance", "declared")
		if found == nil || fmt.Sprint(found["id"]) != "declared" || fmt.Sprint(found["provider"]) != "conformance" {
			return fmt.Errorf("find declared = %#v", found)
		}
		slash := registry.Find("conformance", "org/model/name")
		if slash == nil || fmt.Sprint(slash["id"]) != "org/model/name" {
			return fmt.Errorf("find slash = %#v", slash)
		}
		for _, field := range []string{"baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"} {
			if _, ok := slash[field]; !ok {
				return fmt.Errorf("find slash missing %s: %#v", field, slash)
			}
		}
		var expectedLimits map[string]any
		if err := json.Unmarshal([]byte(`{"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}}`), &expectedLimits); err != nil {
			return err
		}
		if !reflect.DeepEqual(slash["inputLimits"], expectedLimits) {
			return fmt.Errorf("find slash inputLimits = %#v", slash["inputLimits"])
		}
		active, err := ctx.GetModelInfo()
		if err != nil {
			return err
		}
		if active == nil || !reflect.DeepEqual(active.InputLimits, expectedLimits) {
			return fmt.Errorf("active inputLimits = %#v", active)
		}
		input, _ := slash["input"].([]any)
		cost, _ := slash["cost"].(map[string]any)
		tiers, _ := cost["tiers"].([]any)
		compat, _ := slash["compat"].(map[string]any)
		if len(input) != 0 || cost["input"] != float64(0) || len(tiers) != 1 || compat["supportsStrictMode"] != false {
			return fmt.Errorf("find slash shape = %#v", slash)
		}
		if missing := registry.Find("conformance", "missing"); missing != nil {
			return fmt.Errorf("find missing = %#v", missing)
		}
		if overrideOnly := registry.Find("conformance", "override-only"); overrideOnly != nil {
			return fmt.Errorf("find override-only = %#v", overrideOnly)
		}
		auth, err := registry.GetApiKeyAndHeaders(found)
		if err != nil {
			return fmt.Errorf("get auth: %w", err)
		}
		wantAuth := map[string]any{"ok": true, "apiKey": "conformance-key", "headers": map[string]any{"X-Conformance-Auth": "yes"}, "baseUrl": "https://models.invalid/v1", "env": map[string]any{"CONFORMANCE_AUTH": "yes"}}
		if !reflect.DeepEqual(auth, wantAuth) {
			return fmt.Errorf("auth = %#v, want %#v", auth, wantAuth)
		}
		model := map[string]any{"provider": "conformance", "modelId": "declared", "api": "openai-responses"}
		request := modelConformanceContext()
		options := modelConformanceOptions()
		stream := ctx.ModelRegistry().Stream(model, request, options)
		var types []string
		for event := range stream.Events(context.Background()) {
			types = append(types, fmt.Sprint(event["type"]))
		}
		if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" {
			return fmt.Errorf("stream events = %v", types)
		}
		result := stream.Result()
		content, _ := result["content"].([]any)
		block, _ := content[0].(map[string]any)
		if fmt.Sprint(block["text"]) != "streamed" {
			return fmt.Errorf("stream result = %#v", result)
		}
		simple := ctx.ModelRegistry().StreamSimple(model, request, options)
		types = nil
		for event := range simple.Events(context.Background()) {
			types = append(types, fmt.Sprint(event["type"]))
		}
		if strings.Join(types, ",") != "start,text_start,text_delta,text_end,done" {
			return fmt.Errorf("simple events = %v", types)
		}
		if fmt.Sprint(simple.Result()["stopReason"]) != "stop" {
			return fmt.Errorf("simple result = %#v", simple.Result())
		}
		if fmt.Sprint(ctx.ModelRegistry().Complete(model, request, options)["stopReason"]) != "stop" {
			return errors.New("complete did not stop")
		}
		if fmt.Sprint(ctx.ModelRegistry().Stream(model, request, options).Result()["stopReason"]) != "stop" {
			return errors.New("result without iteration did not stop")
		}
		unknown := ctx.ModelRegistry().Complete(map[string]any{"provider": "conformance", "modelId": "unknown", "api": "openai-responses"}, request, options)
		if fmt.Sprint(unknown["stopReason"]) != "error" || !strings.Contains(fmt.Sprint(unknown["errorMessage"]), "unknown model") {
			return fmt.Errorf("unknown result = %#v", unknown)
		}
		transportError := ctx.ModelRegistry().Complete(map[string]any{"provider": "conformance", "modelId": "protocol-error", "api": "openai-responses"}, request, options)
		timestamp, ok := transportError["timestamp"].(int64)
		if !ok || timestamp <= 0 {
			return fmt.Errorf("transport error timestamp = %#v", transportError["timestamp"])
		}
		delete(transportError, "timestamp")
		wantTransportError := map[string]any{
			"role": "assistant", "content": []any{}, "api": "openai-responses", "provider": "conformance", "model": "protocol-error",
			"usage":      map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
			"stopReason": "error", "errorMessage": "transport boom",
		}
		if !reflect.DeepEqual(transportError, wantTransportError) {
			return fmt.Errorf("transport error = %#v, want %#v", transportError, wantTransportError)
		}
		ctx.Notify("model-stream=ok", "info")
		return nil
	})

	ext.Command("ui-probe", "Exercise SDK UI wrappers", func(ctx sdk.Context, args string) error {
		_ = ctx.SetWorkingIndicator(sdk.WorkingIndicatorOptions{"frames": []string{"*"}})
		_ = ctx.SetHiddenThinkingLabel("hidden-thoughts")
		_ = ctx.SetFooter(nil)
		_ = ctx.SetHeader(nil)
		_ = ctx.SetEditorComponent(nil)
		_ = ctx.SetWidget("status", []string{"sdk-fixture: ui-probe"})
		_ = ctx.SetWidget("status-call", []string{"sdk-fixture: ui-probe call"}, sdk.WidgetOptions{"position": "above"})

		themes, themesErr := ctx.GetAllThemes()
		if themesErr != nil {
			return themesErr
		}
		theme, themeErr := ctx.GetTheme("dark")
		_, customErr := ctx.Custom(nil, nil)
		autoErr := ctx.AddAutocompleteProvider(nil)
		// Consume the sentinel only, so the host test can prove both that a
		// subscribed extension suppresses a chunk and that it lets others by.
		_, termErr := ctx.OnTerminalInput(func(data string) sdk.TerminalInputResult {
			return sdk.TerminalInputResult{Consume: data == "\x1b[99~"}
		})

		summary, _ := json.Marshal(map[string]any{
			"themesCount": len(themes),
			"theme":       theme,
			"themeErr":    errString(themeErr),
			"customErr":   errString(customErr),
			"autoErr":     errString(autoErr),
			"termErr":     errString(termErr),
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.Command("agent-probe", "Exercise SDK agent-control wrappers", func(ctx sdk.Context, args string) error {
		idle, err := ctx.IsIdle()
		if err != nil {
			return err
		}
		pending, err := ctx.HasPendingMessages()
		if err != nil {
			return err
		}
		ctx.Compact(nil)

		summary, _ := json.Marshal(map[string]any{
			"idle":    idle,
			"pending": pending,
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.Command("session-probe", "Exercise SDK session-control wrappers", func(ctx sdk.Context, args string) error {
		waitErr := ctx.WaitForIdle()
		reloadErr := ctx.Reload()

		summary, _ := json.Marshal(map[string]any{
			"waitErr":   errString(waitErr),
			"reloadErr": errString(reloadErr),
		})
		ctx.Notify(string(summary), "info")
		return nil
	})

	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{}, errors.New("trust-boom")
	})
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
	})
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustYes, Remember: true}, nil
	})
	ext.OnEvent("cache_warming_decision", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["warmCost"] != 0.05 || data["missCost"] != 0.5 || data["continuationProbability"] != 0.15 || data["action"] != "warm" {
			return nil, fmt.Errorf("unexpected cache decision: %v", data)
		}
		return map[string]any{"action": "stop"}, nil
	})
	ext.OnEvent("agent_before_settle", func(ctx sdk.Context, data map[string]any) (any, error) {
		entries, _ := data["entries"].([]any)
		preview, _ := data["context"].(map[string]any)
		contextEntries, _ := preview["contextEntries"].([]any)
		ctx.Notify(fmt.Sprintf("agent_before_settle:%v:%d:%v:%d:%v", data["outcome"], len(entries), data["continue"], len(contextEntries), preview["canContinue"]), "info")
		data["entries"] = append(entries, map[string]any{"type": "custom", "customType": "kept"})
		return nil, nil
	})
	ext.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		data["entries"] = append(entries, map[string]any{"type": "custom", "customType": "before-error"})
		return nil, errors.New("boundary failed")
	})
	ext.OnEvent("agent_before_settle", func(_ sdk.Context, data map[string]any) (any, error) {
		entries := data["entries"].([]any)
		preview := data["context"].(map[string]any)
		if len(entries) != 2 || len(preview["contextEntries"].([]any)) != 2 || entries[0].(map[string]any)["customType"] != "kept" || entries[1].(map[string]any)["customType"] != "before-error" {
			return nil, errors.New("lost boundary mutation or preview")
		}
		return map[string]any{
			"entries":  []any{map[string]any{"type": "custom", "customType": "conformance-boundary"}},
			"continue": true,
		}, nil
	})
	ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_start:"+fmt.Sprint(data["reason"]), "info")
		if path, ok := data["previousSessionFile"].(string); ok && path != "" {
			ctx.Notify("previous:"+path, "info")
		}
		return nil, nil
	})
	ext.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_shutdown:"+fmt.Sprint(data["reason"]), "info")
		if path, ok := data["targetSessionFile"].(string); ok && path != "" {
			ctx.Notify("target:"+path, "info")
		}
		return nil, nil
	})
	ext.OnEvent("session_info_changed", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_info_changed:"+fmt.Sprint(data["name"]), "info")
		return nil, nil
	})
	ext.OnEvent("session_before_compact", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_before_compact:%v:%v", data["reason"], data["willRetry"]), "info")
		return nil, nil
	})
	ext.OnEvent("session_compact", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_compact:%v:%v:%v", data["reason"], data["willRetry"], data["fromExtension"]), "info")
		return nil, nil
	})

	ext.OnEvent("session_compact_failed", func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify(fmt.Sprintf("session_compact_failed:%v:%v:%v:%v:%v", data["reason"], data["errorMessage"], data["aborted"], data["willRetry"], data["fromExtension"]), "info")
		return nil, nil
	})
	ext.OnEvent("turn_end", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["messageEntryId"] != "boundary-assistant" {
			return nil, nil
		}
		data["entries"] = append(data["entries"].([]any), map[string]any{"type": "custom", "customType": "mutated"})
		return nil, errors.New("turn-boundary-failure")
	})
	ext.OnEvent("turn_end", func(_ sdk.Context, data map[string]any) (any, error) {
		if data["messageEntryId"] != "boundary-assistant" {
			return nil, nil
		}
		return map[string]any{"entries": []any{map[string]any{"type": "custom", "customType": "turn-boundary", "data": data}}, "continue": true}, nil
	})
	ext.OnEvent("turn_end", func(ctx sdk.Context, data map[string]any) (any, error) {
		ids, _ := data["toolResultEntryIds"].([]any)
		var firstID any
		if len(ids) > 0 {
			firstID = ids[0]
		}
		ctx.Notify(fmt.Sprintf("turn_end:%v:%v", data["messageEntryId"], firstID), "info")
		return nil, nil
	})
	var promptMu sync.Mutex
	promptSequence := 0
	for _, event := range []string{"ui_prompt_start", "ui_prompt_end"} {
		ext.OnEvent(event, func(ctx sdk.Context, data map[string]any) (any, error) {
			promptMu.Lock()
			sequence := promptSequence
			promptSequence++
			promptMu.Unlock()
			if title, _ := data["title"].(string); strings.HasPrefix(title, "fifo:") {
				ctx.Notify(fmt.Sprintf("fifo:%d:%v:%s", sequence, data["type"], title), "info")
				return nil, nil
			}
			title := "(none)"
			if value, ok := data["title"]; ok {
				title = fmt.Sprint(value)
			}
			ctx.Notify(fmt.Sprintf("ui_prompt:%v:%v:%v:%s", data["type"], data["reason"], data["kind"], title), "info")
			return nil, nil
		})
	}

	ext.OnEvent("user_bash", func(_ sdk.Context, event map[string]any) (any, error) {
		result := map[string]any{"output": "handled", "exitCode": 7, "cancelled": false, "truncated": false}
		value := map[string]any{"result": result}
		switch event["command"] {
		case "valid":
		case "undefined":
			result["exitCode"] = nil
		case "undefined-path":
			result["fullOutputPath"] = nil
		case "missing":
			delete(result, "exitCode")
		case "invalid":
			result["exitCode"] = "invalid"
		case "null-operations":
			value["operations"] = nil
		default:
			return nil, nil
		}
		return value, nil
	})

	registerConformanceOAuth(ext)
	registerConformanceOAuthObject(ext)
	registerConformanceOAuthLarge(ext)

	// Typed tool events use one shared fixture for subprocess and fused Go.
	ext.OnEvent("tool_call", func(ctx sdk.Context, event map[string]any) (any, error) {
		name, _ := event["toolName"].(string)
		if name != "powershell" && name != "bash" {
			return nil, nil
		}
		input, _ := event["input"].(map[string]any)
		ctx.Notify(fmt.Sprintf("tool-call=%s:%v:%v", name, input["command"], input["timeout"]), "info")
		if input["command"] == "blocked-command" {
			return map[string]any{"block": true, "reason": "blocked " + name}, nil
		}
		return nil, nil
	})
	ext.OnEvent("tool_result", func(ctx sdk.Context, event map[string]any) (any, error) {
		name, _ := event["toolName"].(string)
		if name != "powershell" && name != "bash" {
			return nil, nil
		}
		details, _ := event["details"].(map[string]any)
		truncation, _ := details["truncation"].(map[string]any)
		content, _ := event["content"].([]any)
		var text any
		if len(content) > 0 {
			first, _ := content[0].(map[string]any)
			text = first["text"]
		}
		ctx.Notify(fmt.Sprintf("tool-result=%s:%v:%v:%v", name, details["fullOutputPath"], truncation["totalLines"], text), "info")
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": name + " redacted"}}}, nil
	})

	ext.OnEvent("after_provider_response", func(ctx sdk.Context, event map[string]any) (any, error) {
		headers, _ := event["headers"].(map[string]any)
		ctx.Notify(fmt.Sprintf("provider-response=%v:%v:%v", event["type"], event["status"], headers["x-probe"]), "info")
		return map[string]any{"cancel": true}, nil
	})
	ext.OnEvent("after_provider_response", func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.Notify("provider-response=second", "info")
		return nil, nil
	})

	ext.OnEvent("message_update", func(ctx sdk.Context, event map[string]any) (any, error) {
		assistant, _ := event["assistantMessageEvent"].(map[string]any)
		_, nested := assistant["assistantMessageEvent"]
		ctx.Notify(fmt.Sprintf("message-update=%v:%v:%v:%t", assistant["type"], assistant["contentIndex"], assistant["delta"], nested), "info")
		return nil, nil
	})
	ext.OnEvent("tool_execution_update", func(ctx sdk.Context, event map[string]any) (any, error) {
		partial, _ := event["partialResult"].(map[string]any)
		details, _ := partial["details"].(map[string]any)
		if event["toolName"] == "production_tool" {
			args, _ := event["args"].(map[string]any)
			nested, _ := args["nested"].(map[string]any)
			ctx.Notify(fmt.Sprintf("tool-update=%v:%v:%v:%v:%v", event["toolName"], args["path"], nested["depth"], firstPartialText(partial), details["progress"]), "info")
			return nil, nil
		}
		args, _ := json.Marshal(event["args"])
		ctx.Notify(fmt.Sprintf("tool-update=%v:%s:%v:%v", event["toolName"], args, firstPartialText(partial), details["progress"]), "info")
		return nil, nil
	})
	ext.OnEvent("tool_execution_end", func(ctx sdk.Context, event map[string]any) (any, error) {
		result, _ := event["result"].(map[string]any)
		content, _ := result["content"].([]any)
		image, _ := content[1].(map[string]any)
		details, _ := result["details"].(map[string]any)
		nested, _ := details["nested"].(map[string]any)
		if event["toolName"] == "production_tool" {
			text, _ := content[0].(map[string]any)
			ctx.Notify(fmt.Sprintf("tool-end=%v:%v:%v:%v:%v:%v:%v", event["toolName"], text["text"], len(content), image["data"], image["mimeType"], nested["value"], event["isError"]), "info")
			return nil, nil
		}
		ctx.Notify(fmt.Sprintf("tool-end=%v:%v:%v:%v:%v", event["toolName"], len(content), image["data"], nested["value"], event["isError"]), "info")
		return nil, nil
	})

	return ext
}

// registerConformanceOAuth contributes the canonical OAuth provider the
// cross-transport conformance suite drives. Its behavior must match the Rust
// and Python fixtures byte-for-byte so the OAuth recordings compare equal.
func conformanceLoginDefinition() sdk.LoginDefinition {
	return sdk.LoginDefinition{
		Brand:       repeatRow("A", 41, 5),
		Hero:        repeatRow("A", 32, 14),
		Mascot:      repeatRow("A", 16, 14),
		Palette:     map[string]string{"A": "#123ABC"},
		Name:        "Conformance Pig",
		Description: "Cross-language login fixture",
		Tagline:     "One canonical definition across every SDK",
	}
}

func conformanceSpriteDefinition() sdk.SpriteDefinition {
	return sdk.SpriteDefinition{
		ID:      "conformance-pig",
		Name:    "Conformance Pig",
		Tagline: "One canonical sprite across every SDK",
		Mascot:  repeatRow("A", 16, 14),
		Palette: map[string]string{"A": "#123ABC"},
	}
}

func repeatRow(symbol string, width, height int) []string {
	rows := make([]string, height)
	for i := range rows {
		rows[i] = strings.Repeat(symbol, width)
	}
	return rows
}

func registerConformanceOAuth(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth", sdk.ProviderConfig{
		"name": "Conformance OAuth",
		"oauth": &sdk.OAuthProvider{
			Name:           "Conformance OAuth",
			IsSubscription: true,
			Login: func(cb *sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				cb.OnDeviceCode(sdk.OAuthDeviceCodeInfo{
					UserCode:        "CONF-USER-CODE",
					VerificationURI: "https://conf.example/verify",
				})
				cb.OnProgress("waiting")
				value, err := cb.OnPrompt(sdk.OAuthPrompt{Message: "paste the code"})
				if err != nil {
					return sdk.OAuthCredentials{}, err
				}
				return sdk.OAuthCredentials{Access: "access-" + value, Refresh: "refresh-tok", Expires: 4242, AccountID: "account-login", Scope: "scope-login"}, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) {
				return sdk.OAuthCredentials{Access: "refreshed-" + creds.Refresh, Refresh: creds.Refresh, Expires: 9999, AccountID: creds.AccountID, Scope: creds.Scope}, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				if creds.Access == "boom" {
					panic("getApiKey exploded")
				}
				return "key:" + creds.Access
			},
			CredentialStore: conformanceStore{},
		},
	})
}

// registerConformanceOAuthObject contributes a provider whose callbacks treat credentials as Pi's complete token object: login returns a fractional expiry and provider-owned keys, and refresh spreads its input as `{ ...creds, access, expires }` does. Behavior must match the Rust, Python and Node fixtures.
func registerConformanceOAuthObject(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth-object", sdk.ProviderConfig{
		"name": "Conformance OAuth Object",
		"oauth": &sdk.OAuthProvider{
			Name: "Conformance OAuth Object",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				creds := sdk.OAuthCredentials{Access: "object-access", Refresh: "object-refresh", Extra: map[string]json.RawMessage{"meta": json.RawMessage(`{"k":[1,null,""]}`), "projectId": json.RawMessage(`""`)}}
				creds.SetExpiresMillis(1700000000000.25)
				return creds, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) {
				next := creds
				next.Access = "refreshed-" + creds.Refresh
				expires, _ := creds.ExpiresMillis()
				next.SetExpiresMillis(expires + 0.5)
				return next, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				typed, meta := "untyped", "nometa"
				if string(creds.Extra["type"]) == `"oauth"` {
					typed = "typed"
				}
				if _, ok := creds.Extra["meta"]; ok {
					meta = "meta"
				}
				return "key:" + typed + ":" + meta
			},
		},
	})
}

// registerConformanceOAuthLarge contributes a provider whose expiry is the double 2**60. JSON.parse reads the wire digits 1152921504606847000 as that double, so every SDK reports getApiKey's distance from 2**60 as 0; an SDK that keeps the exact integer reports 24. Behavior must match the Rust, Python and Node fixtures.
func registerConformanceOAuthLarge(ext *sdk.Extension) {
	ext.RegisterProvider("conformance-oauth-large", sdk.ProviderConfig{
		"name": "Conformance OAuth Large",
		"oauth": &sdk.OAuthProvider{
			Name: "Conformance OAuth Large",
			Login: func(*sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				creds := sdk.OAuthCredentials{Access: "large-access", Refresh: "large-refresh"}
				creds.SetExpiresMillis(1 << 60)
				return creds, nil
			},
			GetAPIKey: func(creds sdk.OAuthCredentials) string {
				expires, _ := creds.ExpiresMillis()
				return fmt.Sprintf("key:%.0f:%d", expires-(1<<60), creds.Expires-(1<<60))
			},
		},
	})
}

type conformanceStore struct{}

func (conformanceStore) CredentialStatus() sdk.OAuthCredentialStatus {
	return sdk.OAuthCredentialStatus{Present: true, AuthType: "oauth", Source: "conformance"}
}

func (conformanceStore) StoreCredentials(creds sdk.OAuthCredentials) (string, error) {
	if creds.AccountID != "account-store" || creds.Scope != "scope-store" {
		return "", fmt.Errorf("credential metadata lost: %#v", creds)
	}
	return "/conf/creds.json", nil
}

func (conformanceStore) DeleteCredentials() (bool, error) {
	return true, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// firstPartialText is the text of the first content block of a tool_execution_update partialResult, the AgentToolResult the tool passed to onUpdate (agent-loop.ts:778-786); absent when `content` is not an array of blocks.
func firstPartialText(partial map[string]any) any {
	blocks, _ := partial["content"].([]any)
	if len(blocks) == 0 {
		return nil
	}
	block, _ := blocks[0].(map[string]any)
	return block["text"]
}
