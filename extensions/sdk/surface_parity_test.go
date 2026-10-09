// SPDX-License-Identifier: MIT

package sdk

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// surfaceHost runs ext against a mock host through the ready handshake and
// returns the register payload.
func surfaceHost(t *testing.T, ext *Extension, ready *readyMsg) (*mockHost, *registerMsg, chan error) {
	t.Helper()
	host := newMockHost(t)
	t.Cleanup(host.close)
	t.Setenv("PIG_EXT_SOCKET", host.sockPath)
	done := make(chan error, 1)
	go func() { done <- ext.Run() }()
	host.accept(t)
	reg := host.readEnvelope(t)
	if reg.Type != msgRegister || reg.Register == nil {
		t.Fatalf("expected register, got %+v", reg)
	}
	if ready == nil {
		ready = &readyMsg{Cwd: "/tmp", Width: 80}
	}
	host.writeEnvelope(t, envelope{Type: msgReady, Ready: ready})
	return host, reg.Register, done
}

func surfaceShutdown(t *testing.T, host *mockHost, done chan error) {
	t.Helper()
	host.writeEnvelope(t, envelope{Type: msgShutdown, Shutdown: &shutdownMsg{Reason: "done"}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("extension did not stop")
	}
}

// runSurfaceCommand runs command name and answers each host call with
// answer, returning the calls in order and the command's response.
func runSurfaceCommand(t *testing.T, host *mockHost, name string, answer func(call *callMsg) *callResultMsg) ([]*callMsg, *responseMsg) {
	t.Helper()
	id := "req-" + name
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "command", Tool: name}})
	var calls []*callMsg
	for {
		env := host.readEnvelope(t)
		switch env.Type {
		case msgCall:
			calls = append(calls, env.Call)
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: answer(env.Call)})
		case msgResponse:
			if env.ID != id {
				t.Fatalf("response for %q, want %q", env.ID, id)
			}
			return calls, env.Response
		}
	}
}

func decodeArgs(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("decode call args %s: %v", raw, err)
	}
	return args
}

func TestEventConstantsMatchPiEventNames(t *testing.T) {
	want := map[string]string{
		EventProjectTrust: "project_trust", EventResourcesDiscover: "resources_discover",
		EventSessionStart: "session_start", EventSessionInfoChanged: "session_info_changed",
		EventSessionBeforeSwitch: "session_before_switch", EventSessionBeforeFork: "session_before_fork",
		EventSessionBeforeCompact: "session_before_compact", EventSessionCompact: "session_compact",
		EventSessionCompactFailed: "session_compact_failed", EventSessionShutdown: "session_shutdown",
		EventSessionBeforeTree: "session_before_tree", EventSessionTree: "session_tree",
		EventContext: "context", EventContextWithSystem: "context_with_system",
		EventCacheWarmingDecision: "cache_warming_decision", EventBeforeProviderRequest: "before_provider_request",
		EventBeforeProviderHeaders: "before_provider_headers", EventAfterProviderResponse: "after_provider_response",
		EventBeforeAgentStart: "before_agent_start", EventAgentStart: "agent_start", EventAgentEnd: "agent_end",
		EventAgentBeforeSettle: "agent_before_settle", EventAgentSettled: "agent_settled",
		EventUIPromptStart: "ui_prompt_start", EventUIPromptEnd: "ui_prompt_end",
		EventTurnStart: "turn_start", EventTurnEnd: "turn_end",
		EventMessageStart: "message_start", EventMessageUpdate: "message_update", EventMessageEnd: "message_end",
		EventToolExecutionStart: "tool_execution_start", EventToolExecutionUpdate: "tool_execution_update",
		EventToolExecutionEnd: "tool_execution_end", EventModelSelect: "model_select",
		EventThinkingLevelSelect: "thinking_level_select", EventToolCall: "tool_call",
		EventToolResult: "tool_result", EventUserBash: "user_bash", EventInput: "input",
	}
	if len(want) != 39 {
		t.Fatalf("event constants are not distinct: %d of 39", len(want))
	}
	for got, name := range want {
		if got != name {
			t.Errorf("event constant %q, want %q", got, name)
		}
	}
}

func TestRegisterToolSendsFullDefinition(t *testing.T) {
	ext := New("surface")
	ext.RegisterTool(ToolDefinition{
		Name:             "grep_all",
		Label:            "Grep All",
		Description:      "Search every file",
		PromptSnippet:    "grep_all: search every file",
		PromptGuidelines: []string{"Use grep_all for repository-wide searches."},
		Parameters:       Schema{"type": "object"},
		ConstrainedSampling: &ConstrainedSampling{
			Type: "json_schema", Strict: "prefer",
		},
		RenderShell:   ToolRenderShellSelf,
		ExecutionMode: "sequential",
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			params["prepared"] = true
			return params, nil
		},
		Execute: func(_ Context, params map[string]any) (any, error) {
			if params["prepared"] != nil {
				return nil, errors.New("tool_call prepared the arguments the host already prepared")
			}
			return ToolResult{Content: "done", Usage: map[string]any{"input": 3, "output": 4, "totalTokens": 7}}, nil
		},
		RenderCall: func(Context, map[string]any, ToolRenderContext, int) ([]string, error) { return nil, nil },
	})
	host, reg, done := surfaceHost(t, ext, nil)

	raw, err := json.Marshal(reg.Tools)
	if err != nil {
		t.Fatal(err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name":                 "grep_all",
		"label":                "Grep All",
		"description":          "Search every file",
		"parameters":           map[string]any{"type": "object"},
		"constrained_sampling": map[string]any{"type": "json_schema", "strict": "prefer"},
		"prompt_guidelines":    []any{"Use grep_all for repository-wide searches."},
		"prompt_snippet":       "grep_all: search every file",
		"execution_mode":       "sequential",
		"render_shell":         "self",
		"renders_call":         true,
		"prepares_arguments":   true,
	}
	if len(tools) != 1 || !reflect.DeepEqual(tools[0], want) {
		t.Fatalf("registered tools = %#v, want [%#v]", tools, want)
	}

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-tool", Request: &requestMsg{Method: "tool_call", Tool: "grep_all", ToolCallID: "call-1", Args: json.RawMessage(`{}`)}})
	resp := host.readEnvelope(t)
	if resp.Type != msgResponse || resp.Response == nil || resp.Response.Error != nil {
		t.Fatalf("tool response = %+v", resp)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Response.Result, &result); err != nil {
		t.Fatal(err)
	}
	wantResult := map[string]any{"content": "done", "usage": map[string]any{"input": float64(3), "output": float64(4), "totalTokens": float64(7)}}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("tool result = %#v, want %#v", result, wantResult)
	}
	surfaceShutdown(t, host, done)
}

func TestSendCustomMessageSendsDetailsAndBlocks(t *testing.T) {
	ext := New("surface")
	trigger := true
	ext.Command("send", "", func(ctx Context, _ string) error {
		return ctx.SendCustomMessage(CustomMessage{
			CustomType: "note",
			Content:    []map[string]any{{"type": "text", "text": "hi"}, {"type": "image", "data": "AAAA", "mimeType": "image/png"}},
			Display:    true,
			Details:    map[string]any{"k": 1},
		}, SendMessageOptions{TriggerTurn: &trigger, DeliverAs: "followUp"})
	})
	host, _, done := surfaceHost(t, ext, nil)
	calls, resp := runSurfaceCommand(t, host, "send", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	if resp.Error != nil || len(calls) != 1 || calls[0].Method != "sendMessage" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	want := map[string]any{
		"message": map[string]any{
			"customType": "note",
			"content":    []any{map[string]any{"type": "text", "text": "hi"}, map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}},
			"display":    true,
			"details":    map[string]any{"k": float64(1)},
		},
		"options": map[string]any{"triggerTurn": true, "deliverAs": "followUp"},
	}
	if got := decodeArgs(t, calls[0].Args); !reflect.DeepEqual(got, want) {
		t.Fatalf("sendMessage args = %#v, want %#v", got, want)
	}
	surfaceShutdown(t, host, done)
}

func TestExecWithOptionsSendsOptionsAndDecodesKilled(t *testing.T) {
	ext := New("surface")
	got := make(chan *ExecResult, 1)
	ext.Command("exec", "", func(ctx Context, _ string) error {
		result, err := ctx.ExecWithOptions("sleep", []string{"10"}, ExecOptions{Timeout: 250, Cwd: "/work"})
		got <- result
		return err
	})
	host, _, done := surfaceHost(t, ext, nil)
	calls, resp := runSurfaceCommand(t, host, "exec", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"stdout":"out","stderr":"err","code":137,"killed":true}`)}
	})
	if resp.Error != nil || len(calls) != 1 || calls[0].Method != "exec" {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	want := map[string]any{"command": "sleep", "args": []any{"10"}, "options": map[string]any{"timeout": float64(250), "cwd": "/work"}}
	if args := decodeArgs(t, calls[0].Args); !reflect.DeepEqual(args, want) {
		t.Fatalf("exec args = %#v, want %#v", args, want)
	}
	if result := <-got; result == nil || *result != (ExecResult{Stdout: "out", Stderr: "err", ExitCode: 137, Killed: true}) {
		t.Fatalf("exec result = %+v", result)
	}
	surfaceShutdown(t, host, done)
}

func TestDialogsWithOptionsSendTimeout(t *testing.T) {
	ext := New("surface")
	type outcome struct {
		selected, text string
		selectOK       bool
		confirmed      bool
		inputOK        bool
	}
	got := make(chan outcome, 1)
	ext.Command("dialogs", "", func(ctx Context, _ string) error {
		var o outcome
		var err error
		if o.selected, o.selectOK, err = ctx.SelectWithOptions("Pick", []string{"a", "b"}, DialogOptions{Timeout: 1000}); err != nil {
			return err
		}
		if o.confirmed, err = ctx.ConfirmWithOptions("Sure?", "really", DialogOptions{Timeout: 2000}); err != nil {
			return err
		}
		if o.text, o.inputOK, err = ctx.InputWithOptions("Name", "type", DialogOptions{Timeout: 3000}); err != nil {
			return err
		}
		got <- o
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	calls, resp := runSurfaceCommand(t, host, "dialogs", func(call *callMsg) *callResultMsg {
		switch call.Method {
		case "ui.select":
			return &callResultMsg{Result: json.RawMessage(`{"selected":"b","ok":true}`)}
		case "ui.confirm":
			return &callResultMsg{Result: json.RawMessage(`{"confirmed":true}`)}
		default:
			return &callResultMsg{Result: json.RawMessage(`{"text":"pig","ok":true}`)}
		}
	})
	if resp.Error != nil || len(calls) != 3 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	want := []struct {
		method string
		args   map[string]any
	}{
		{"ui.select", map[string]any{"title": "Pick", "options": []any{"a", "b"}, "opts": map[string]any{"timeout": float64(1000)}}},
		{"ui.confirm", map[string]any{"title": "Sure?", "message": "really", "opts": map[string]any{"timeout": float64(2000)}}},
		{"ui.input", map[string]any{"title": "Name", "placeholder": "type", "opts": map[string]any{"timeout": float64(3000)}}},
	}
	for i, w := range want {
		if calls[i].Method != w.method {
			t.Fatalf("call %d = %s, want %s", i, calls[i].Method, w.method)
		}
		if args := decodeArgs(t, calls[i].Args); !reflect.DeepEqual(args, w.args) {
			t.Fatalf("%s args = %#v, want %#v", w.method, args, w.args)
		}
	}
	if o := <-got; o != (outcome{selected: "b", selectOK: true, confirmed: true, text: "pig", inputOK: true}) {
		t.Fatalf("dialog results = %+v", o)
	}
	surfaceShutdown(t, host, done)
}

func TestHasUIAndUIThemeFollowReplicatedState(t *testing.T) {
	ext := New("surface")
	if (Context{ext: ext}).HasUI() {
		t.Fatal("HasUI before any state = true, want Pi's unbound no-op UI")
	}
	type snapshot struct {
		hasUI bool
		theme UITheme
	}
	got := make(chan snapshot, 1)
	ext.Command("read", "", func(ctx Context, _ string) error {
		got <- snapshot{ctx.HasUI(), ctx.UITheme()}
		return nil
	})
	state := json.RawMessage(`{"hasUI":false,"theme":{"name":"dark","sourcePath":"/themes/dark.json","foregrounds":{"accent":"\u001b[38;5;1m","thinkingHigh":"\u001b[38;5;2m"},"backgrounds":{"selectedBg":"\u001b[48;5;3m"},"modifiers":true,"mode":"256color"}}`)
	host, _, done := surfaceHost(t, ext, &readyMsg{Cwd: "/tmp", Width: 80, State: state})
	runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	first := <-got
	if first.hasUI {
		t.Fatal("HasUI = true, want the snapshot's false")
	}
	theme := first.theme
	if theme.Name != "dark" || theme.SourcePath != "/themes/dark.json" || theme.GetColorMode() != "256color" {
		t.Fatalf("theme identity = %q %q %q", theme.Name, theme.SourcePath, theme.GetColorMode())
	}
	if got := theme.Fg("accent", "x"); got != "\x1b[38;5;1mx\x1b[39m" {
		t.Fatalf("Fg = %q", got)
	}
	if got := theme.Bg("selectedBg", "x"); got != "\x1b[48;5;3mx\x1b[49m" {
		t.Fatalf("Bg = %q", got)
	}
	if got := theme.Bold("x"); got != "\x1b[1mx\x1b[22m" {
		t.Fatalf("Bold = %q", got)
	}
	if got := theme.Strikethrough("x"); got != "\x1b[9mx\x1b[29m" {
		t.Fatalf("Strikethrough = %q", got)
	}
	if ansi, err := theme.GetFgAnsi("accent"); err != nil || ansi != "\x1b[38;5;1m" {
		t.Fatalf("GetFgAnsi = %q, %v", ansi, err)
	}
	if _, err := theme.GetFgAnsi("nope"); err == nil || err.Error() != "Unknown theme color: nope" {
		t.Fatalf("GetFgAnsi(unknown) error = %v", err)
	}
	if _, err := theme.GetBgAnsi("nope"); err == nil || err.Error() != "Unknown theme color: nope" {
		t.Fatalf("GetBgAnsi(unknown) error = %v", err)
	}
	if got := theme.GetThinkingBorderColor("high")("x"); got != "\x1b[38;5;2mx\x1b[39m" {
		t.Fatalf("GetThinkingBorderColor(high) = %q", got)
	}

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "theme_change", Args: json.RawMessage(`{"name":"light","foregrounds":{"bashMode":"\u001b[38;2;1;2;3m"},"backgrounds":{},"modifiers":false,"mode":"truecolor"}`)}})
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "state_update", Args: json.RawMessage(`{"state":{"hasUI":true}}`)}})
	runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	second := <-got
	if !second.hasUI {
		t.Fatal("HasUI after state_update = false")
	}
	light := second.theme
	if light.Name != "light" || light.SourcePath != "" || light.GetColorMode() != "truecolor" {
		t.Fatalf("theme after change = %q %q %q", light.Name, light.SourcePath, light.GetColorMode())
	}
	if got := light.Bold("x"); got != "x" {
		t.Fatalf("Bold with modifiers off = %q", got)
	}
	if got := light.GetBashModeBorderColor()("x"); got != "\x1b[38;2;1;2;3mx\x1b[39m" {
		t.Fatalf("GetBashModeBorderColor = %q", got)
	}
	func() {
		defer func() {
			if err, _ := recover().(error); err == nil || err.Error() != "Unknown theme color: accent" {
				t.Fatalf("Fg(accent) after change recovered %v, want the new palette's unknown-token error", err)
			}
		}()
		_ = light.Fg("accent", "x")
	}()
	if got := theme.Fg("accent", "x"); got != "\x1b[38;5;1mx\x1b[39m" {
		t.Fatalf("earlier snapshot changed: %q", got)
	}
	surfaceShutdown(t, host, done)
}

// loop_turned is the host step a Node runtime waits for after a print prompt's last call. A Go extension has no such wait, so the notify changes nothing: the next request runs with the state the extension held before it.
func TestLoopTurnedNotifyChangesNothing(t *testing.T) {
	ext := New("surface")
	got := make(chan bool, 1)
	ext.Command("read", "", func(ctx Context, _ string) error {
		got <- ctx.HasUI()
		return nil
	})
	host, _, done := surfaceHost(t, ext, &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"hasUI":true}`)})
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "loop_turned"}})
	if _, response := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} }); response.Error != nil {
		t.Fatalf("read after loop_turned: %+v", response.Error)
	}
	if !<-got {
		t.Fatal("HasUI after loop_turned = false, want the ready state's true")
	}
	surfaceShutdown(t, host, done)
}

type staticComponent struct{}

func (staticComponent) Render(int) []string { return []string{"frame"} }
func (staticComponent) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}

func TestCustomForwardsOverlayOptions(t *testing.T) {
	ext := New("surface")
	margin := 2
	ext.Command("custom", "", func(ctx Context, _ string) error {
		_, err := ctx.Custom(staticComponent{}, RemoteOverlayOptions{
			Overlay: true,
			OverlayOptions: &OverlayOptions{
				Width:        OverlayPercent(50),
				MinWidth:     20,
				MaxHeight:    OverlayCells(10),
				Anchor:       "top-right",
				OffsetX:      -1,
				OffsetY:      1,
				Row:          OverlayPercent(25),
				Col:          OverlayCells(4),
				Margin:       &OverlayMargin{All: &margin},
				NonCapturing: true,
			},
		})
		return err
	})
	host, _, done := surfaceHost(t, ext, &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"hasUI":true}`)})
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-custom", Request: &requestMsg{Method: "command", Tool: "custom"}})
	var open *callMsg
	for open == nil {
		env := host.readEnvelope(t)
		if env.Type == msgCall && env.Call.Method == "ui.custom" {
			open = env.Call
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"ok":false}`)}})
		}
	}
	for {
		if env := host.readEnvelope(t); env.Type == msgResponse {
			if env.Response.Error != nil {
				t.Fatalf("custom response error = %+v", env.Response.Error)
			}
			break
		}
	}
	args := decodeArgs(t, open.Args)
	want := map[string]any{
		"width": "50%", "minWidth": float64(20), "maxHeight": float64(10), "anchor": "top-right",
		"offsetX": float64(-1), "offsetY": float64(1), "row": "25%", "col": float64(4),
		"margin": float64(2), "nonCapturing": true,
	}
	if args["overlay"] != true || !reflect.DeepEqual(args["overlayOptions"], want) {
		t.Fatalf("ui.custom args = %#v, want overlay and overlayOptions %#v", args, want)
	}
	edges, err := json.Marshal(OverlayMargin{Top: 1, Right: 2, Bottom: 3, Left: 4})
	if err != nil || string(edges) != `{"bottom":3,"left":4,"right":2,"top":1}` {
		t.Fatalf("edge margin = %s, %v", edges, err)
	}
	surfaceShutdown(t, host, done)
}

func TestCompactWithOptionsReportsOutcome(t *testing.T) {
	ext := New("surface")
	completed := make(chan map[string]any, 1)
	failed := make(chan error, 1)
	ext.Command("compact-ok", "", func(ctx Context, _ string) error {
		ctx.CompactWithOptions(CompactOptions{
			CustomInstructions: "keep the plan",
			OnComplete:         func(result map[string]any) { completed <- result },
			OnError:            func(err error) { failed <- err },
		})
		return nil
	})
	ext.Command("compact-fail", "", func(ctx Context, _ string) error {
		ctx.CompactWithOptions(CompactOptions{OnError: func(err error) { failed <- err }})
		return nil
	})
	ext.Command("compact-plain", "", func(ctx Context, _ string) error {
		ctx.CompactWithOptions(CompactOptions{CustomInstructions: "short"})
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)

	// The command answers before compaction does: the call outlives it.
	callID, call := startDetachedCompact(t, host, "compact-ok")
	if call.Method != "compact" || call.ParentRequestID != "" {
		t.Fatalf("compact call = %+v, want no parent request", call)
	}
	if args := decodeArgs(t, call.Args); !reflect.DeepEqual(args, map[string]any{"customInstructions": "keep the plan", "awaitCompletion": true}) {
		t.Fatalf("compact args = %#v", args)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: callID, CallResult: &callResultMsg{Result: json.RawMessage(`{"summary":"s","firstKeptEntryId":"e1","tokensBefore":1200}`)}})
	select {
	case result := <-completed:
		if !reflect.DeepEqual(result, map[string]any{"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": float64(1200)}) {
			t.Fatalf("OnComplete result = %#v", result)
		}
	case err := <-failed:
		t.Fatalf("OnError(%v), want OnComplete", err)
	case <-time.After(5 * time.Second):
		t.Fatal("OnComplete not called")
	}

	callID, call = startDetachedCompact(t, host, "compact-fail")
	if call.ParentRequestID != "" || !strings.Contains(string(call.Args), `"awaitCompletion":true`) {
		t.Fatalf("failing compact call = %+v", call)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: callID, CallResult: &callResultMsg{Error: &errorInfo{Message: "Nothing to compact"}}})
	select {
	case err := <-failed:
		if err.Error() != "Nothing to compact" {
			t.Fatalf("OnError = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnError not called")
	}

	calls, _ := runSurfaceCommand(t, host, "compact-plain", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	if len(calls) != 1 || calls[0].Method != "compact" || calls[0].ParentRequestID != "req-compact-plain" {
		t.Fatalf("plain compact calls = %+v", calls)
	}
	if args := decodeArgs(t, calls[0].Args); !reflect.DeepEqual(args, map[string]any{"customInstructions": "short"}) {
		t.Fatalf("plain compact args = %#v", args)
	}
	surfaceShutdown(t, host, done)
}

// startDetachedCompact runs command name and returns the compact call it
// starts, which may reach the host after the command's response.
func startDetachedCompact(t *testing.T, host *mockHost, name string) (string, *callMsg) {
	t.Helper()
	id := "req-" + name
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: "command", Tool: name}})
	var callID string
	var call *callMsg
	responded := false
	for call == nil || !responded {
		env := host.readEnvelope(t)
		switch {
		case env.Type == msgCall:
			callID, call = env.ID, env.Call
		case env.Type == msgResponse && env.ID == id:
			if env.Response.Error != nil {
				t.Fatalf("%s error = %+v", name, env.Response.Error)
			}
			responded = true
		}
	}
	return callID, call
}
