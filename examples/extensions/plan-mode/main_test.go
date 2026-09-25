package planmode

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

type wireCommand struct {
	Name string `json:"name"`
}

type wireShortcut struct {
	Key string `json:"key"`
}

type wireFlag struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type wireHandler struct {
	Event     string `json:"event"`
	HandlerID int    `json:"handler_id"`
}

type wireRegister struct {
	Name      string         `json:"name"`
	Commands  []wireCommand  `json:"commands"`
	Shortcuts []wireShortcut `json:"shortcuts"`
	Flags     []wireFlag     `json:"flags"`
	Handlers  []wireHandler  `json:"handlers"`
}

type wireCall struct {
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args"`
}

type wireResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type wireWidgetPush struct {
	Key   string   `json:"key"`
	Lines []string `json:"lines"`
}

type wireEnvelope struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Register   *wireRegister   `json:"register"`
	Call       *wireCall       `json:"call"`
	Response   *wireResponse   `json:"response"`
	WidgetPush *wireWidgetPush `json:"widget_push"`
}

type hostCall struct {
	method string
	args   map[string]any
}

type extensionHost struct {
	t           *testing.T
	conn        net.Conn
	reader      *bufio.Reader
	register    wireRegister
	activeTools []string
	entries     []json.RawMessage
	selectValue string
	editorValue string
	flagPlan    bool
	calls       []hostCall
	widgets     []wireWidgetPush
	requestID   atomic.Uint64
}

func startExtensionHost(t *testing.T, activeTools []string) *extensionHost {
	t.Helper()

	hostConn, extensionConn := net.Pipe()
	host := &extensionHost{
		t:           t,
		conn:        hostConn,
		reader:      bufio.NewReader(hostConn),
		activeTools: slices.Clone(activeTools),
	}
	done := make(chan error, 1)
	go func() {
		done <- Extension().RunWithConn(extensionConn)
	}()

	envelope := host.readEnvelope()
	if envelope.Type != "register" || envelope.Register == nil {
		t.Fatalf("first extension frame = %+v, want register", envelope)
	}
	host.register = *envelope.Register
	host.writeEnvelope(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"cwd":    "/work",
			"mode":   "tui",
			"width":  100,
			"height": 30,
			"state": map[string]any{
				"activeTools":    activeTools,
				"isIdle":         true,
				"projectTrusted": true,
				"hasUI":          true,
			},
		},
	})

	t.Cleanup(func() {
		_ = hostConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("extension did not stop after transport close")
		}
	})
	return host
}

func (h *extensionHost) readEnvelope() wireEnvelope {
	h.t.Helper()
	if err := h.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		h.t.Fatalf("set read deadline: %v", err)
	}
	var header [4]byte
	if _, err := io.ReadFull(h.reader, header[:]); err != nil {
		h.t.Fatalf("read frame header: %v", err)
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(h.reader, payload); err != nil {
		h.t.Fatalf("read frame body: %v", err)
	}
	var envelope wireEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		h.t.Fatalf("decode frame %s: %v", payload, err)
	}
	return envelope
}

func (h *extensionHost) writeEnvelope(value any) {
	h.t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		h.t.Fatalf("encode frame: %v", err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := h.conn.Write(header[:]); err != nil {
		h.t.Fatalf("write frame header: %v", err)
	}
	if _, err := h.conn.Write(payload); err != nil {
		h.t.Fatalf("write frame body: %v", err)
	}
}

func (h *extensionHost) invokeCommand(name string) json.RawMessage {
	h.t.Helper()
	return h.invoke("command", name, "", 0, "")
}

func (h *extensionHost) invokeEvent(name string, data any) json.RawMessage {
	h.t.Helper()
	for _, handler := range h.register.Handlers {
		if handler.Event == name {
			return h.invoke("event", "", name, handler.HandlerID, data)
		}
	}
	h.t.Fatalf("event %q was not registered", name)
	return nil
}

func (h *extensionHost) invoke(method, tool, event string, handlerID int, args any) json.RawMessage {
	h.t.Helper()
	id := fmt.Sprintf("request-%d", h.requestID.Add(1))
	rawArgs, err := json.Marshal(args)
	if err != nil {
		h.t.Fatalf("encode request args: %v", err)
	}
	h.writeEnvelope(map[string]any{
		"type": "request",
		"id":   id,
		"request": map[string]any{
			"method":     method,
			"tool":       tool,
			"event":      event,
			"handler_id": handlerID,
			"args":       json.RawMessage(rawArgs),
		},
	})

	for {
		envelope := h.readEnvelope()
		switch envelope.Type {
		case "request_state":
		case "widget_push":
			if envelope.WidgetPush != nil {
				h.widgets = append(h.widgets, *envelope.WidgetPush)
			}
		case "call":
			h.handleCall(envelope)
		case "response":
			if envelope.ID != id {
				h.t.Fatalf("response id = %q, want %q", envelope.ID, id)
			}
			if envelope.Response == nil {
				h.t.Fatal("response frame has no response payload")
			}
			if envelope.Response.Error != nil {
				h.t.Fatalf("extension request failed: %s", envelope.Response.Error.Message)
			}
			return envelope.Response.Result
		default:
			h.t.Fatalf("unexpected extension frame while awaiting response: %+v", envelope)
		}
	}
}

func (h *extensionHost) handleCall(envelope wireEnvelope) {
	h.t.Helper()
	if envelope.Call == nil {
		h.t.Fatal("call frame has no call payload")
	}
	var args map[string]any
	if len(envelope.Call.Args) > 0 && string(envelope.Call.Args) != "null" {
		if err := json.Unmarshal(envelope.Call.Args, &args); err != nil {
			h.t.Fatalf("decode %s args: %v", envelope.Call.Method, err)
		}
	}
	h.calls = append(h.calls, hostCall{method: envelope.Call.Method, args: args})

	result := any(map[string]any{})
	switch envelope.Call.Method {
	case "getActiveTools":
		result = map[string]any{"tools": h.activeTools}
	case "setActiveTools":
		rawTools, _ := args["tools"].([]any)
		h.activeTools = make([]string, 0, len(rawTools))
		for _, raw := range rawTools {
			if tool, ok := raw.(string); ok {
				h.activeTools = append(h.activeTools, tool)
			}
		}
	case "getFlag":
		result = map[string]any{"value": h.flagPlan}
	case "watchSessionLog":
		complete, _ := args["complete"].(bool)
		entries := h.entries
		if complete {
			entries = nil
		}
		result = map[string]any{"entries": entries, "entryCount": len(h.entries), "hasMore": false, "leafId": ""}
	case "ui.select":
		result = map[string]any{"selected": h.selectValue, "ok": h.selectValue != ""}
	case "ui.editor":
		result = map[string]any{"text": h.editorValue, "ok": h.editorValue != ""}
	}
	h.writeEnvelope(map[string]any{
		"type": "call_result",
		"id":   envelope.ID,
		"call_result": map[string]any{
			"result": result,
		},
	})
}

func (h *extensionHost) clearEffects() {
	h.calls = nil
	h.widgets = nil
}

func (h *extensionHost) callsFor(method string) []hostCall {
	var calls []hostCall
	for _, call := range h.calls {
		if call.method == method {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestExtensionRegistersPlanModeSurface(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "bash", "edit", "write"})
	if host.register.Name != "plan-mode" {
		t.Fatalf("extension name = %q", host.register.Name)
	}
	commandNames := make([]string, 0, len(host.register.Commands))
	for _, command := range host.register.Commands {
		commandNames = append(commandNames, command.Name)
	}
	if !slices.Equal(commandNames, []string{"plan", "todos"}) {
		t.Fatalf("commands = %v", commandNames)
	}
	if len(host.register.Shortcuts) != 1 || host.register.Shortcuts[0].Key != "ctrl+alt+p" {
		t.Fatalf("shortcuts = %+v", host.register.Shortcuts)
	}
	if len(host.register.Flags) != 1 || host.register.Flags[0] != (wireFlag{Name: "plan", Type: "boolean"}) {
		t.Fatalf("flags = %+v", host.register.Flags)
	}
}

func TestPlanModePreservesCustomActiveToolsWhileToggling(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "bash", "edit", "write", "echo_tool"})
	host.invokeCommand("plan")
	wantPlan := []string{"read", "bash", "echo_tool", "grep", "find", "ls", "questionnaire"}
	if !slices.Equal(host.activeTools, wantPlan) {
		t.Fatalf("plan tools = %v, want %v", host.activeTools, wantPlan)
	}

	host.invokeCommand("plan")
	wantNormal := []string{"read", "bash", "edit", "write", "echo_tool"}
	if !slices.Equal(host.activeTools, wantNormal) {
		t.Fatalf("restored tools = %v, want %v", host.activeTools, wantNormal)
	}
}

func TestPlanModeDoesNotPromptWithoutAPlan(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "bash", "edit", "write"})
	host.invokeCommand("plan")
	host.clearEffects()
	host.invokeEvent("agent_end", map[string]any{
		"type":     "agent_end",
		"messages": []any{assistantMessage("This file defines the command-line argument parser.")},
	})
	if calls := host.callsFor("ui.select"); len(calls) != 0 {
		t.Fatalf("ui.select calls = %v, want none", calls)
	}
	if calls := host.callsFor("sendMessage"); len(calls) != 0 {
		t.Fatalf("sendMessage calls = %v, want none", calls)
	}
}

func TestPlanModeQueuesRefinementAsFollowUpUserMessage(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "bash", "edit", "write"})
	host.selectValue = "Refine the plan"
	host.editorValue = "Add a regression test."
	host.invokeCommand("plan")
	host.clearEffects()
	host.invokeEvent("agent_end", map[string]any{
		"type":     "agent_end",
		"messages": []any{assistantMessage("Plan:\n1. Inspect the current implementation\n2. Add a regression test")},
	})
	calls := host.callsFor("sendUserMessage")
	if len(calls) != 1 {
		t.Fatalf("sendUserMessage calls = %v, want one", calls)
	}
	if calls[0].args["content"] != "Add a regression test." {
		t.Fatalf("sendUserMessage content = %#v", calls[0].args["content"])
	}
	options, _ := calls[0].args["options"].(map[string]any)
	if options["deliverAs"] != "followUp" {
		t.Fatalf("sendUserMessage options = %v", options)
	}
}

func TestPlanModeQueuesExecutionAsFollowUpCustomMessage(t *testing.T) {
	host := startExtensionHost(t, []string{"read", "bash", "edit", "write", "echo_tool"})
	host.selectValue = "Execute the plan (track progress)"
	host.invokeCommand("plan")
	host.clearEffects()
	host.invokeEvent("agent_end", map[string]any{
		"type":     "agent_end",
		"messages": []any{assistantMessage("Plan:\n1. Inspect the current implementation\n2. Add a regression test")},
	})
	if want := []string{"read", "bash", "edit", "write", "echo_tool"}; !slices.Equal(host.activeTools, want) {
		t.Fatalf("execution tools = %v, want %v", host.activeTools, want)
	}
	calls := host.callsFor("sendMessage")
	var execution *hostCall
	for i := range calls {
		message, _ := calls[i].args["message"].(map[string]any)
		if message["customType"] == "plan-mode-execute" {
			execution = &calls[i]
			break
		}
	}
	if execution == nil {
		t.Fatalf("sendMessage calls = %v, want plan-mode-execute", calls)
	}
	options, _ := execution.args["options"].(map[string]any)
	if options["triggerTurn"] != true || options["deliverAs"] != "followUp" {
		t.Fatalf("execution options = %v", options)
	}
}

func assistantMessage(text string) map[string]any {
	return map[string]any{
		"role":       "assistant",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"stopReason": "stop",
	}
}
