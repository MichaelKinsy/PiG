package extensionconformance

import (
	"encoding/json"
	"strings"
	"sync"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// orderLog records, in wire order, the frames the ordering rows compare: the tool_call and tool_result requests the host writes, the
// tool_call answers and tool_execution_update frames it reads, and the ui.notify calls it reads. It sits in the wireTap's two observers,
// which run on each connection's write loop and read loop in the order the frames cross the socket.
type orderLog struct {
	// ext names the extension whose connection the log records: a packed cell's peer extension gets the same hook requests.
	ext    string
	mu     sync.Mutex
	events []orderEvent
	// toolCalls maps a tool_call request id to its tool call id.
	toolCalls map[string]string
}

// orderEvent is one frame. kind is "request" (a tool_call the host wrote), "hook" (a tool_result request the host wrote), "answer" (a tool_call
// response the host read), "update" (a tool update the host read) or "notify" (a ui.notify call the host read). tool is the tool call id; for a
// notify it is the message.
type orderEvent struct {
	kind string
	tool string
}

func (l *orderLog) outbound(extName string, frame []byte) {
	if extName != l.ext {
		return
	}
	var env subprocess.Envelope
	if sdkjson.Unmarshal(frame, &env) != nil || env.Type != subprocess.MsgRequest || env.Request == nil {
		return
	}
	switch {
	case env.Request.Method == subprocess.MethodToolCall:
		l.mu.Lock()
		if l.toolCalls == nil {
			l.toolCalls = map[string]string{}
		}
		l.toolCalls[env.ID] = env.Request.ToolCallID
		l.events = append(l.events, orderEvent{"request", env.Request.ToolCallID})
		l.mu.Unlock()
	case env.Request.Method == "event" && env.Request.Event == "tool_result":
		var args struct {
			ToolCallID string `json:"toolCallId"`
		}
		if json.Unmarshal(env.Request.Args, &args) != nil {
			return
		}
		l.mu.Lock()
		l.events = append(l.events, orderEvent{"hook", args.ToolCallID})
		l.mu.Unlock()
	}
}

func (l *orderLog) inbound(extName string, env *subprocess.Envelope) {
	if extName != l.ext {
		return
	}
	switch {
	case env.Type == subprocess.MsgResponse:
		l.mu.Lock()
		if tool, ok := l.toolCalls[env.ID]; ok {
			l.events = append(l.events, orderEvent{"answer", tool})
		}
		l.mu.Unlock()
	case env.Type == subprocess.MsgNotify && env.Notify != nil && env.Notify.Method == subprocess.NotifyToolUpdate:
		var update subprocess.ToolUpdatePayload
		if json.Unmarshal(env.Notify.Args, &update) != nil {
			return
		}
		l.mu.Lock()
		l.events = append(l.events, orderEvent{"update", l.toolCalls[update.RequestID]})
		l.mu.Unlock()
	case env.Call != nil && env.Call.Method == "ui.notify":
		var args struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(env.Call.Args, &args) != nil {
			return
		}
		l.mu.Lock()
		l.events = append(l.events, orderEvent{"notify", args.Message})
		l.mu.Unlock()
	}
}

// take returns the frames recorded since the last take, in wire order, and forgets them.
func (l *orderLog) take() orderFrames {
	l.mu.Lock()
	defer l.mu.Unlock()
	frames := orderFrames(l.events)
	l.events = nil
	return frames
}

type orderFrames []orderEvent

// tools returns the tool call ids of the frames of one kind, in wire order.
func (f orderFrames) tools(kind string) []string {
	var out []string
	for _, event := range f {
		if event.kind == kind {
			out = append(out, event.tool)
		}
	}
	return out
}

// notifies returns what follows the prefix in the ui.notify calls whose message starts with one of the prefixes, in wire order. A call that
// carries the second prefix keeps its number, so the order of both of a tool's calls shows.
func (f orderFrames) notifies(prefixes ...string) []string {
	var out []string
	for _, message := range f.tools("notify") {
		for _, prefix := range prefixes {
			if rest, ok := strings.CutPrefix(message, prefix); ok {
				out = append(out, rest)
				break
			}
		}
	}
	return out
}
