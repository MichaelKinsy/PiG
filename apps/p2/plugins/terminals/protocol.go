// Package terminals is p2's terminals plugin (apps/p2/plugins/terminals): the owner's terminal panel and the agent's
// terminal tool over one detached terminal host that owns the programs, a headless libghostty terminal for each, and the
// pi-env connections they run on.
package terminals

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/terminals/protocol.ts: the dedicated terminal byte channel and the detached host's framing.

// Exited is how a terminal's program ended: its exit code, or the error that stopped it from running.
type Exited struct {
	Code  *int
	Error *string
}

// TerminalInfo is one terminal as the host lists it.
type TerminalInfo struct {
	ID        string
	SessionID string
	// Index is its number in the session, stable while it lives: the lowest free one from 1 (the agent's `-t 2`).
	Index int
	// Name was given when it was opened (`new-window -n`).
	Name string
	// Agent: the agent opened it (the terminal tool); the owner's own have no mark.
	Agent     bool
	Cwd       string
	Env       *string
	Title     string
	CreatedAt int64
	Cols      int
	Rows      int
	Exited    *Exited
}

// Clone copies the terminal's info (the host mutates its own while it runs).
func (info TerminalInfo) Clone() TerminalInfo {
	if info.Exited != nil {
		exited := *info.Exited
		info.Exited = &exited
	}
	return info
}

// TerminalTheme is a viewer's colors.
type TerminalTheme struct {
	Foreground int
	Background int
	Scheme     string
}

// Request types.
const (
	RequestList     = "list"
	RequestTheme    = "theme"
	RequestOpen     = "open"
	RequestAttach   = "attach"
	RequestResize   = "resize"
	RequestAck      = "ack"
	RequestClose    = "close"
	RequestShutdown = "shutdown"
	RequestCall     = "call"
	RequestCancel   = "cancel"
)

// TerminalRequest is a request to the host (`TerminalRequest`): the fields its Type uses.
type TerminalRequest struct {
	Type       string
	SessionID  string
	Cwd        string
	Env        *string
	Cols, Rows int
	TerminalID string
	Bytes      int
	// Force is `close`'s `force`, absent when nil.
	Force *bool
	ID    int
	Call  *HostCall
	Theme TerminalTheme
}

// TerminalWait is how long a read waits for output, and for what. A timeout is no error: the read returns what is there.
type TerminalWait struct {
	// Until is "quiet", "exit" or "match".
	Until string
	// Pattern (`match`) is a JavaScript regular expression, multiline, tested against the plain text read.
	Pattern    *string
	IgnoreCase bool
	// QuietMs (`quiet`) is how long without output counts as quiet; zero when unset.
	QuietMs   int
	TimeoutMs int
}

// Operations of a HostCall.
const (
	OpList         = "list"
	OpOpen         = "open"
	OpSend         = "send"
	OpCursor       = "cursor"
	OpRead         = "read"
	OpCapture      = "capture"
	OpClose        = "close"
	OpCloseSession = "closeSession"
)

// HostCall is one of the host's calls (`HostCall`). Each names its session: a terminal of another session is unknown to
// it. Key makes a call that changes something idempotent: a call with a key the host has seen returns the first call's
// result, done once.
type HostCall struct {
	Op        string
	SessionID string
	Key       string
	Cwd       string
	Env       *string
	Name      string
	// Command is typed into the new shell, then Enter.
	Command    string
	Cols, Rows int
	Target     string
	Keys       []string
	Literal    bool
	From       *int64
	Wait       *TerminalWait
	Scrollback int
	Escapes    bool
}

// HasKey is whether the call changes something (`"key" in request`): open, send and close.
func (call HostCall) HasKey() bool {
	return call.Op == OpOpen || call.Op == OpSend || call.Op == OpClose
}

// TerminalRead is what a read returns.
type TerminalRead struct {
	Terminal TerminalInfo
	// From and To are the output read: absolute byte offsets of the terminal's output.
	From, To int64
	// Text is plain text: escape sequences removed, carriage-return overwrites applied.
	Text string
	// Skipped counts bytes after From the host no longer kept (its ring holds the last MiB).
	Skipped int64
	// Truncated counts characters of Text left out at its start (it keeps the last READ_TEXT_LIMIT).
	Truncated int
	// Screen: a full-screen program (alternate screen) runs: its screen as plain text, which says more than its output.
	Screen *string
	// Waited is why a wait ended: "quiet", "exit", "match", "timeout" or "closed".
	Waited string
	// Foreground is the job in front of the shell, when one runs.
	Foreground string
}

// Window is a terminal with the job in front of its shell.
type Window struct {
	Terminal   TerminalInfo
	Foreground string
}

// Result kinds.
const (
	KindList     = "list"
	KindTerminal = "terminal"
	KindCursor   = "cursor"
	KindRead     = "read"
	KindCapture  = "capture"
	KindClosed   = "closed"
	KindDone     = "done"
)

// HostResult is the result of a HostCall (`HostResult`): the fields its Kind uses.
type HostResult struct {
	Kind       string
	Windows    []Window
	Terminal   TerminalInfo
	Offset     int64
	Read       TerminalRead
	Text       string
	TerminalID string
}

// ReadTextLimit is the plain text a read returns at most: its last characters.
const ReadTextLimit = 256 * 1024

// Event types.
const (
	EventList      = "list"
	EventAttached  = "attached"
	EventChanged   = "changed"
	EventClosed    = "closed"
	EventBusy      = "busy"
	EventError     = "error"
	EventHello     = "hello"
	EventOutdated  = "outdated"
	EventTerminals = "terminals"
	EventReply     = "reply"
)

// TerminalEvent is what the host sends (`TerminalEvent`): the fields its Type uses.
type TerminalEvent struct {
	Type      string
	Terminals []TerminalInfo
	Terminal  TerminalInfo
	// TerminalID is `closed`'s and `busy`'s terminalId.
	TerminalID string
	// Program is `busy`'s program, empty when unknown.
	Program string
	Message string
	Version string
	// Running is `hello`'s and `outdated`'s running count; HasRunning tells whether `outdated` carries it.
	Running    int
	HasRunning bool
	Pid        int
	// ID, Result and Err are `reply`'s: Err is set for the error form.
	ID     int
	Result *HostResult
	Err    *string
}

// TerminalOutputWindow is the output a viewer may have unacknowledged. It bounds how long a keystroke's echo can wait
// behind earlier output: at 5 Mbit/s, 256 KiB is 0.4 s. Viewers acknowledge every 64 KiB (Terminal.tsx), so four batches
// are in flight.
const TerminalOutputWindow = 256 * 1024

// MaxBacklog is what a connection may have unsent.
const MaxBacklog = 8 * 1024 * 1024

const maxFrame = 16 * 1024 * 1024

// EncodeFrame is a message (a JSON value) or bytes as one frame: a big-endian length (of the kind byte and the body), the
// kind (0 JSON, 1 bytes), the body.
func EncodeFrame(message any) ([]byte, error) {
	var kind byte
	var body []byte
	switch typed := message.(type) {
	case []byte:
		kind, body = 1, typed
	default:
		var err error
		if body, err = chordjson.Marshal(typed); err != nil {
			return nil, err
		}
	}
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)+1))
	frame[4] = kind
	copy(frame[5:], body)
	return frame, nil
}

// ErrCorruptFrame is a frame the reader refuses.
var ErrCorruptFrame = errors.New("Corrupt terminal frame") //nolint:staticcheck // Pi's message

// Frame is one read frame: Bytes for a bytes frame, else the parsed JSON value.
type Frame struct {
	Bytes []byte
	JSON  any
	Raw   bool
}

// ReadFrames reads frames from reader until it fails (the error, or ErrCorruptFrame), calling onFrame for each.
func ReadFrames(reader io.Reader, onFrame func(Frame)) error {
	header := make([]byte, 5)
	for {
		if _, err := io.ReadFull(reader, header[:4]); err != nil {
			return err
		}
		length := binary.BigEndian.Uint32(header)
		if length < 1 || length > maxFrame {
			return ErrCorruptFrame
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return err
		}
		switch body[0] {
		case 1:
			onFrame(Frame{Bytes: body[1:], Raw: true})
		case 0:
			parsed, err := chordjson.Decode(body[1:])
			if err != nil {
				return ErrCorruptFrame
			}
			onFrame(Frame{JSON: parsed})
		default:
			return ErrCorruptFrame
		}
	}
}

// --- JSON forms. Keys are written in Pi's object-literal order, so a frame is the text JSON.stringify makes.

func number(value int) float64 { return float64(value) }

func objectOf(pairs ...any) *chordjson.Object { return chordjson.ObjectOf(pairs...) }

func optional(object *chordjson.Object, key string, value any, present bool) {
	if present {
		object.Set(key, value)
	}
}

func stringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// JSON is the info as `JSON.stringify` writes it.
func (info TerminalInfo) JSON() *chordjson.Object {
	object := objectOf("id", info.ID, "sessionId", info.SessionID, "index", number(info.Index))
	optional(object, "name", info.Name, info.Name != "")
	optional(object, "agent", true, info.Agent)
	object.Set("cwd", info.Cwd)
	object.Set("env", stringPointer(info.Env))
	object.Set("title", info.Title)
	object.Set("createdAt", float64(info.CreatedAt))
	object.Set("cols", number(info.Cols))
	object.Set("rows", number(info.Rows))
	if info.Exited != nil {
		exited := objectOf()
		if info.Exited.Code != nil {
			exited.Set("code", number(*info.Exited.Code))
		}
		if info.Exited.Error != nil {
			exited.Set("error", *info.Exited.Error)
		}
		object.Set("exited", exited)
	}
	return object
}

func infosJSON(infos []TerminalInfo) []any {
	out := make([]any, len(infos))
	for index, info := range infos {
		out[index] = info.JSON()
	}
	return out
}

// JSON is the read as `JSON.stringify` writes it.
func (read TerminalRead) JSON() *chordjson.Object {
	object := objectOf("terminal", read.Terminal.JSON(), "from", float64(read.From), "to", float64(read.To), "text", read.Text,
		"skipped", float64(read.Skipped), "truncated", number(read.Truncated))
	optional(object, "screen", read.Screen, read.Screen != nil)
	optional(object, "waited", read.Waited, read.Waited != "")
	optional(object, "foreground", read.Foreground, read.Foreground != "")
	return object
}

// JSON is the result as `JSON.stringify` writes it.
func (result HostResult) JSON() *chordjson.Object {
	switch result.Kind {
	case KindList:
		windows := make([]any, len(result.Windows))
		for index, window := range result.Windows {
			item := objectOf("terminal", window.Terminal.JSON())
			optional(item, "foreground", window.Foreground, window.Foreground != "")
			windows[index] = item
		}
		return objectOf("kind", KindList, "windows", windows)
	case KindTerminal:
		return objectOf("kind", KindTerminal, "terminal", result.Terminal.JSON())
	case KindCursor:
		return objectOf("kind", KindCursor, "terminal", result.Terminal.JSON(), "offset", float64(result.Offset))
	case KindRead:
		object := objectOf("kind", KindRead)
		for key, value := range result.Read.JSON().All() {
			object.Set(key, value)
		}
		return object
	case KindCapture:
		return objectOf("kind", KindCapture, "terminal", result.Terminal.JSON(), "text", result.Text)
	case KindClosed:
		return objectOf("kind", KindClosed, "terminalId", result.TerminalID)
	}
	return objectOf("kind", KindDone)
}

// JSON is the event as `JSON.stringify` writes it.
func (event TerminalEvent) JSON() *chordjson.Object {
	switch event.Type {
	case EventList, EventTerminals:
		return objectOf("type", event.Type, "terminals", infosJSON(event.Terminals))
	case EventAttached, EventChanged:
		return objectOf("type", event.Type, "terminal", event.Terminal.JSON())
	case EventClosed:
		return objectOf("type", EventClosed, "terminalId", event.TerminalID)
	case EventBusy:
		object := objectOf("type", EventBusy, "terminalId", event.TerminalID)
		optional(object, "program", event.Program, event.Program != "")
		return object
	case EventError:
		return objectOf("type", EventError, "message", event.Message)
	case EventHello:
		return objectOf("type", EventHello, "version", event.Version, "running", number(event.Running), "pid", number(event.Pid))
	case EventOutdated:
		object := objectOf("type", EventOutdated)
		optional(object, "running", number(event.Running), event.HasRunning)
		return object
	}
	if event.Err != nil {
		return objectOf("type", EventReply, "id", number(event.ID), "error", *event.Err)
	}
	return objectOf("type", EventReply, "id", number(event.ID), "result", event.Result.JSON())
}

// JSON is the wait as `JSON.stringify` writes it.
func (wait TerminalWait) JSON() *chordjson.Object {
	object := objectOf("until", wait.Until)
	if wait.Pattern != nil {
		object.Set("pattern", *wait.Pattern)
	}
	optional(object, "ignoreCase", true, wait.IgnoreCase)
	optional(object, "quietMs", number(wait.QuietMs), wait.QuietMs != 0)
	object.Set("timeoutMs", number(wait.TimeoutMs))
	return object
}

// JSON is the call as `JSON.stringify` writes it.
func (call HostCall) JSON() *chordjson.Object {
	switch call.Op {
	case OpList:
		return objectOf("op", OpList, "sessionId", call.SessionID)
	case OpOpen:
		object := objectOf("op", OpOpen, "sessionId", call.SessionID, "key", call.Key, "cwd", call.Cwd, "env", stringPointer(call.Env))
		optional(object, "name", call.Name, call.Name != "")
		optional(object, "command", call.Command, call.Command != "")
		object.Set("cols", number(call.Cols))
		object.Set("rows", number(call.Rows))
		return object
	case OpSend:
		keys := make([]any, len(call.Keys))
		for index, key := range call.Keys {
			keys[index] = key
		}
		return objectOf("op", OpSend, "sessionId", call.SessionID, "key", call.Key, "target", call.Target, "keys", keys, "literal", call.Literal)
	case OpCursor:
		return objectOf("op", OpCursor, "sessionId", call.SessionID, "target", call.Target)
	case OpRead:
		object := objectOf("op", OpRead, "sessionId", call.SessionID, "target", call.Target)
		optional(object, "from", float64(derefInt64(call.From)), call.From != nil)
		if call.Wait != nil {
			object.Set("wait", call.Wait.JSON())
		}
		return object
	case OpCapture:
		object := objectOf("op", OpCapture, "sessionId", call.SessionID, "target", call.Target, "scrollback", number(call.Scrollback))
		optional(object, "escapes", true, call.Escapes)
		return object
	case OpClose:
		return objectOf("op", OpClose, "sessionId", call.SessionID, "key", call.Key, "target", call.Target)
	}
	return objectOf("op", OpCloseSession, "sessionId", call.SessionID)
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// JSON is the request as `JSON.stringify` writes it.
func (request TerminalRequest) JSON() *chordjson.Object {
	switch request.Type {
	case RequestList:
		return objectOf("type", RequestList, "sessionId", request.SessionID)
	case RequestTheme:
		return objectOf("type", RequestTheme, "foreground", number(request.Theme.Foreground), "background", number(request.Theme.Background), "scheme", request.Theme.Scheme)
	case RequestOpen:
		return objectOf("type", RequestOpen, "sessionId", request.SessionID, "cwd", request.Cwd, "env", stringPointer(request.Env), "cols", number(request.Cols), "rows", number(request.Rows))
	case RequestAttach:
		return objectOf("type", RequestAttach, "terminalId", request.TerminalID)
	case RequestResize:
		return objectOf("type", RequestResize, "cols", number(request.Cols), "rows", number(request.Rows))
	case RequestAck:
		return objectOf("type", RequestAck, "bytes", number(request.Bytes))
	case RequestClose:
		object := objectOf("type", RequestClose)
		if request.Force != nil {
			object.Set("force", *request.Force)
		}
		return object
	case RequestCall:
		return objectOf("type", RequestCall, "id", number(request.ID), "call", request.Call.JSON())
	case RequestCancel:
		return objectOf("type", RequestCancel, "id", number(request.ID))
	}
	return objectOf("type", RequestShutdown)
}

// --- Reading JSON forms.

type jsonReader struct{ err error }

func (reader *jsonReader) object(value any, what string) *chordjson.Object {
	object, ok := value.(*chordjson.Object)
	if !ok && reader.err == nil {
		reader.err = fmt.Errorf("%s is not an object", what)
	}
	return object
}

func (reader *jsonReader) text(object *chordjson.Object, key string) string {
	if object == nil {
		return ""
	}
	value, ok := object.Value(key).(string)
	if !ok && reader.err == nil {
		reader.err = fmt.Errorf("%s is not a string", key)
	}
	return value
}

func (reader *jsonReader) optionalText(object *chordjson.Object, key string) string {
	if object == nil || !object.Has(key) {
		return ""
	}
	return reader.text(object, key)
}

func (reader *jsonReader) integer(object *chordjson.Object, key string) int {
	return int(reader.float(object, key))
}

func (reader *jsonReader) float(object *chordjson.Object, key string) float64 {
	if object == nil {
		return 0
	}
	value, ok := object.Value(key).(float64)
	if (!ok || math.IsNaN(value)) && reader.err == nil {
		reader.err = fmt.Errorf("%s is not a number", key)
	}
	return value
}

func (reader *jsonReader) optionalInt(object *chordjson.Object, key string) (int, bool) {
	if object == nil || !object.Has(key) {
		return 0, false
	}
	return reader.integer(object, key), true
}

func (reader *jsonReader) boolean(object *chordjson.Object, key string) bool {
	if object == nil {
		return false
	}
	value, _ := object.Value(key).(bool)
	return value
}

func (reader *jsonReader) envOf(object *chordjson.Object) *string {
	if object == nil {
		return nil
	}
	value := object.Value("env")
	if value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok && reader.err == nil {
		reader.err = errors.New("env is not a string")
	}
	return &text
}

// ParseInfo reads a terminal's info.
func ParseInfo(value any) (TerminalInfo, error) {
	reader := &jsonReader{}
	info := reader.info(value)
	return info, reader.err
}

func (reader *jsonReader) info(value any) TerminalInfo {
	object := reader.object(value, "terminal")
	info := TerminalInfo{
		ID: reader.text(object, "id"), SessionID: reader.text(object, "sessionId"), Index: reader.integer(object, "index"),
		Name: reader.optionalText(object, "name"), Agent: reader.boolean(object, "agent"), Cwd: reader.text(object, "cwd"),
		Env: reader.envOf(object), Title: reader.text(object, "title"), CreatedAt: int64(reader.float(object, "createdAt")),
		Cols: reader.integer(object, "cols"), Rows: reader.integer(object, "rows"),
	}
	if object != nil && object.Has("exited") {
		exited := reader.object(object.Value("exited"), "exited")
		info.Exited = &Exited{}
		if code, ok := reader.optionalInt(exited, "code"); ok {
			info.Exited.Code = &code
		}
		if exited != nil && exited.Has("error") {
			message := reader.text(exited, "error")
			info.Exited.Error = &message
		}
	}
	return info
}

func (reader *jsonReader) infos(value any) []TerminalInfo {
	list, ok := value.([]any)
	if !ok && reader.err == nil {
		reader.err = errors.New("terminals is not an array")
	}
	infos := make([]TerminalInfo, len(list))
	for index, each := range list {
		infos[index] = reader.info(each)
	}
	return infos
}

func (reader *jsonReader) read(object *chordjson.Object) TerminalRead {
	read := TerminalRead{
		Terminal: reader.info(object.Value("terminal")), From: int64(reader.float(object, "from")), To: int64(reader.float(object, "to")),
		Text: reader.text(object, "text"), Skipped: int64(reader.float(object, "skipped")), Truncated: reader.integer(object, "truncated"),
		Waited: reader.optionalText(object, "waited"), Foreground: reader.optionalText(object, "foreground"),
	}
	if object.Has("screen") {
		screen := reader.text(object, "screen")
		read.Screen = &screen
	}
	return read
}

func (reader *jsonReader) result(value any) *HostResult {
	object := reader.object(value, "result")
	if object == nil {
		return nil
	}
	result := &HostResult{Kind: reader.text(object, "kind")}
	switch result.Kind {
	case KindList:
		list, _ := object.Value("windows").([]any)
		for _, each := range list {
			window := reader.object(each, "window")
			result.Windows = append(result.Windows, Window{Terminal: reader.info(window.Value("terminal")), Foreground: reader.optionalText(window, "foreground")})
		}
	case KindTerminal:
		result.Terminal = reader.info(object.Value("terminal"))
	case KindCursor:
		result.Terminal = reader.info(object.Value("terminal"))
		result.Offset = int64(reader.float(object, "offset"))
	case KindRead:
		result.Read = reader.read(object)
	case KindCapture:
		result.Terminal = reader.info(object.Value("terminal"))
		result.Text = reader.text(object, "text")
	case KindClosed:
		result.TerminalID = reader.text(object, "terminalId")
	}
	return result
}

// ParseEvent reads an event the host sent.
func ParseEvent(value any) (TerminalEvent, error) {
	reader := &jsonReader{}
	object := reader.object(value, "event")
	event := TerminalEvent{Type: reader.text(object, "type")}
	switch event.Type {
	case EventList, EventTerminals:
		event.Terminals = reader.infos(object.Value("terminals"))
	case EventAttached, EventChanged:
		event.Terminal = reader.info(object.Value("terminal"))
	case EventClosed:
		event.TerminalID = reader.text(object, "terminalId")
	case EventBusy:
		event.TerminalID = reader.text(object, "terminalId")
		event.Program = reader.optionalText(object, "program")
	case EventError:
		event.Message = reader.text(object, "message")
	case EventHello:
		event.Version = reader.text(object, "version")
		event.Running = reader.integer(object, "running")
		event.Pid = reader.integer(object, "pid")
	case EventOutdated:
		event.Running, event.HasRunning = reader.optionalInt(object, "running")
	case EventReply:
		event.ID = reader.integer(object, "id")
		if object != nil && object.Has("error") {
			message := reader.text(object, "error")
			event.Err = &message
		} else if object != nil {
			event.Result = reader.result(object.Value("result"))
		}
	}
	return event, reader.err
}

func (reader *jsonReader) wait(value any) *TerminalWait {
	object := reader.object(value, "wait")
	if object == nil {
		return nil
	}
	wait := &TerminalWait{Until: reader.text(object, "until"), IgnoreCase: reader.boolean(object, "ignoreCase"), TimeoutMs: reader.integer(object, "timeoutMs")}
	if object.Has("pattern") {
		pattern := reader.text(object, "pattern")
		wait.Pattern = &pattern
	}
	wait.QuietMs, _ = reader.optionalInt(object, "quietMs")
	return wait
}

func (reader *jsonReader) call(value any) *HostCall {
	object := reader.object(value, "call")
	if object == nil {
		return nil
	}
	call := &HostCall{Op: reader.text(object, "op"), SessionID: reader.text(object, "sessionId")}
	switch call.Op {
	case OpList, OpCloseSession:
	case OpOpen:
		call.Key, call.Cwd, call.Env = reader.text(object, "key"), reader.text(object, "cwd"), reader.envOf(object)
		call.Name, call.Command = reader.optionalText(object, "name"), reader.optionalText(object, "command")
		call.Cols, call.Rows = reader.integer(object, "cols"), reader.integer(object, "rows")
	case OpSend:
		call.Key, call.Target, call.Literal = reader.text(object, "key"), reader.text(object, "target"), reader.boolean(object, "literal")
		keys, _ := object.Value("keys").([]any)
		for _, key := range keys {
			text, ok := key.(string)
			if !ok && reader.err == nil {
				reader.err = errors.New("keys is not an array of strings")
			}
			call.Keys = append(call.Keys, text)
		}
	case OpCursor:
		call.Target = reader.text(object, "target")
	case OpRead:
		call.Target = reader.text(object, "target")
		if object.Has("from") {
			from := int64(reader.float(object, "from"))
			call.From = &from
		}
		if object.Has("wait") {
			call.Wait = reader.wait(object.Value("wait"))
		}
	case OpCapture:
		call.Target, call.Scrollback, call.Escapes = reader.text(object, "target"), reader.integer(object, "scrollback"), reader.boolean(object, "escapes")
	case OpClose:
		call.Key, call.Target = reader.text(object, "key"), reader.text(object, "target")
	default:
		if reader.err == nil {
			reader.err = errors.New("Unknown call")
		}
	}
	return call
}

// ParseRequest reads a request a client sent to the host.
func ParseRequest(value any) (TerminalRequest, error) {
	reader := &jsonReader{}
	object := reader.object(value, "request")
	request := TerminalRequest{Type: reader.text(object, "type")}
	switch request.Type {
	case RequestList:
		request.SessionID = reader.text(object, "sessionId")
	case RequestTheme:
		request.Theme = TerminalTheme{Foreground: reader.integer(object, "foreground"), Background: reader.integer(object, "background"), Scheme: reader.text(object, "scheme")}
	case RequestOpen:
		request.SessionID, request.Cwd, request.Env = reader.text(object, "sessionId"), reader.text(object, "cwd"), reader.envOf(object)
		request.Cols, request.Rows = reader.integer(object, "cols"), reader.integer(object, "rows")
	case RequestAttach:
		request.TerminalID = reader.text(object, "terminalId")
	case RequestResize:
		request.Cols, request.Rows = reader.integer(object, "cols"), reader.integer(object, "rows")
	case RequestAck:
		request.Bytes = reader.integer(object, "bytes")
	case RequestClose:
		if object != nil && object.Has("force") {
			force := reader.boolean(object, "force")
			request.Force = &force
		}
	case RequestCall:
		request.ID = reader.integer(object, "id")
		request.Call = reader.call(object.Value("call"))
	case RequestCancel:
		request.ID = reader.integer(object, "id")
	}
	return request, reader.err
}

// ParseRead reads what a read returns.
func ParseRead(value any) (TerminalRead, error) {
	reader := &jsonReader{}
	object := reader.object(value, "read")
	if object == nil {
		return TerminalRead{}, reader.err
	}
	read := reader.read(object)
	return read, reader.err
}
