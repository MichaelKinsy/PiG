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
		"maxTokens": 321, "temperature": 0.65, "samplingParams": map[string]any{"topP": 0.8},
		"thinkingBudgets": map[string]any{"minimal": 11, "low": 22, "medium": 33, "high": 44}, "thinking": "high", "isReasoning": true,
		"env": map[string]any{"WIRE_ENV": "request-value", "SECOND_ENV": "distinct-value"}, "headers": map[string]any{"X-Wire": "yes", "X-Remove": nil}, "sessionId": "conformance-session", "transport": "sse",
	}
}

// Extension returns the canonical Go SDK fixture shared by subprocess and
// fused conformance harnesses.
func Extension() *sdk.Extension {
	ext := sdk.New("sdk-fixture")
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

	ext.Tool("echo", "Echo back the input", sdk.Schema{
		"type":     "object",
		"required": []string{"text"},
		"properties": map[string]any{
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

	ext.ToolWithSource("sourced_tool", "Tool with explicit source", sdk.Schema{"type": "object"},
		"mcp:test-server",
		[]string{"Use sourced_tool to test per-tool source attribution."},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "sourced"}, nil
		})

	ext.ToolWithConstrainedSampling("grammar_tool", "Tool with a grammar constrained sampling request", sdk.Schema{"type": "object"},
		sdk.ConstrainedSampling{Type: "grammar", Variants: map[string]string{"openai_lark": "start: NUMBER"}},
		func(ctx sdk.Context, params map[string]any) (any, error) {
			return map[string]string{"content": "grammar"}, nil
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

	ext.Command("context-probe", "Report ctx.mode + ctx.getSystemPromptOptions()", func(ctx sdk.Context, args string) error {
		opts := ctx.GetSystemPromptOptions()
		ctx.Notify(fmt.Sprintf("mode=%s trusted=%t spo_prompt=%s spo_cwd=%s spo_tools=%s",
			ctx.Mode(), ctx.IsProjectTrusted(), opts.CustomPrompt, opts.Cwd, strings.Join(opts.SelectedTools, ",")), "info")
		return nil
	})

	ext.Command("session-log-probe", "Read a paged session log", func(ctx sdk.Context, _ string) error {
		ctx.Notify(fmt.Sprintf("session entries=%d branch=%d", len(ctx.GetEntries()), len(ctx.GetBranch())), "info")
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
		active := ctx.GetModelInfo()
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

		themes := ctx.GetAllThemes()
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
		idle := ctx.IsIdle()
		pending := ctx.HasPendingMessages()
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
		return nil, nil
	})
	ext.OnSessionShutdown(func(ctx sdk.Context, data map[string]any) (any, error) {
		ctx.Notify("session_shutdown:"+fmt.Sprint(data["reason"]), "info")
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

	registerConformanceOAuth(ext)

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
			ctx.Notify(fmt.Sprintf("tool-update=%v:%v:%v:%v:%v", event["toolName"], args["path"], nested["depth"], partial["content"], details["progress"]), "info")
			return nil, nil
		}
		args, _ := json.Marshal(event["args"])
		ctx.Notify(fmt.Sprintf("tool-update=%v:%s:%v:%v", event["toolName"], args, partial["content"], details["progress"]), "info")
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
				return sdk.OAuthCredentials{Access: "access-" + value, Refresh: "refresh-tok", Expires: 4242}, nil
			},
			RefreshToken: func(creds sdk.OAuthCredentials) (sdk.OAuthCredentials, error) {
				return sdk.OAuthCredentials{Access: "refreshed-" + creds.Refresh, Refresh: creds.Refresh, Expires: 9999}, nil
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

type conformanceStore struct{}

func (conformanceStore) CredentialStatus() sdk.OAuthCredentialStatus {
	return sdk.OAuthCredentialStatus{Present: true, AuthType: "oauth", Source: "conformance"}
}

func (conformanceStore) StoreCredentials(sdk.OAuthCredentials) (string, error) {
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
