package sdk

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// MaxFrameSize is the maximum allowed message size (128 MB). Bounds a single
// length-prefixed frame to guard against unbounded allocation while allowing
// large host responses such as getBranch on a long session (the full branch
// history can run to tens of MB). Must match the host and other-language SDK
// MaxFrameSize constants.
const MaxFrameSize = 128 * 1024 * 1024

// Message types (must match host's protocol.go).
const (
	msgRegister     = "register"
	msgReady        = "ready"
	msgRequest      = "request"
	msgResponse     = "response"
	msgNotify       = "notify"
	msgCancel       = "cancel"
	msgCall         = "call"
	msgCallResult   = "call_result"
	msgWidgetPush   = "widget_push"
	msgShutdown     = "shutdown"
	msgPing         = "ping"
	msgPong         = "pong"
	msgRequestState = "request_state"
)

// envelope is the top-level wire message.
type envelope struct {
	Type         string           `json:"type"`
	ID           string           `json:"id,omitempty"`
	Register     *registerMsg     `json:"register,omitempty"`
	Ready        *readyMsg        `json:"ready,omitempty"`
	Request      *requestMsg      `json:"request,omitempty"`
	Response     *responseMsg     `json:"response,omitempty"`
	Notify       *notifyMsg       `json:"notify,omitempty"`
	Cancel       *cancelMsg       `json:"cancel,omitempty"`
	Call         *callMsg         `json:"call,omitempty"`
	CallResult   *callResultMsg   `json:"call_result,omitempty"`
	WidgetPush   *widgetPushMsg   `json:"widget_push,omitempty"`
	Shutdown     *shutdownMsg     `json:"shutdown,omitempty"`
	Ping         *pingMsg         `json:"ping,omitempty"`
	Pong         *pongMsg         `json:"pong,omitempty"`
	RequestState *requestStateMsg `json:"request_state,omitempty"`
}

type registerMsg struct {
	Name           string        `json:"name"`
	Tools          []toolDef     `json:"tools,omitempty"`
	Commands       []cmdDef      `json:"commands,omitempty"`
	Shortcuts      []shortcutDef `json:"shortcuts,omitempty"`
	Handlers       []handlerDef  `json:"handlers,omitempty"`
	Flags          []flagDef     `json:"flags,omitempty"`
	Providers      []providerDef `json:"providers,omitempty"`
	Renderers      []rendererDef `json:"message_renderers,omitempty"`
	EntryRenderers []rendererDef `json:"entry_renderers,omitempty"`
}

type toolDef struct {
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Parameters          any      `json:"parameters"`                     // JSON Schema: must match host's ToolDecl.Parameters
	ConstrainedSampling any      `json:"constrained_sampling,omitempty"` // false | ConstrainedSampling: provider-side constrained sampling request
	PromptGuidelines    []string `json:"prompt_guidelines,omitempty"`    // Bullets injected into system prompt Guidelines section when tool is active
	Source              string   `json:"source,omitempty"`               // pig additive (D23): optional per-tool source; defaults to extension name
	RenderShell         string   `json:"render_shell,omitempty"`         // "self" when the renderers draw their own framing
	RendersCall         bool     `json:"renders_call,omitempty"`         // the tool has a call renderer
	RendersResult       bool     `json:"renders_result,omitempty"`       // the tool has a result renderer
}

type handlerDef struct {
	Event     string `json:"event"`
	CanBlock  bool   `json:"can_block"`
	HandlerID int    `json:"handler_id"`
}

type cmdDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type shortcutDef struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type flagDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Type        string          `json:"type"`
	Default     json.RawMessage `json:"default,omitempty"`
}

type providerDef struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type rendererDef struct {
	CustomType string `json:"custom_type"`
}

type readyMsg struct {
	SessionName string          `json:"session_name"`
	Cwd         string          `json:"cwd"`
	Mode        string          `json:"mode"`
	Width       int             `json:"width"`
	Height      int             `json:"height,omitempty"`
	Model       string          `json:"model"`
	State       json.RawMessage `json:"state,omitempty"` // initial StatePayload snapshot
}

type requestMsg struct {
	Method     string          `json:"method"` // "tool_call", "event", "command", "shortcut", "render_message", "render_entry", "render_tool"
	Tool       string          `json:"tool,omitempty"`
	Event      string          `json:"event,omitempty"`
	HandlerID  int             `json:"handler_id,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
}

type responseMsg struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *errorInfo      `json:"error,omitempty"`
}

type notifyMsg struct {
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args,omitempty"`
}

type callMsg struct {
	Method          string          `json:"method"`
	Args            json.RawMessage `json:"args,omitempty"`
	ParentRequestID string          `json:"parent_request_id,omitempty"`
}

type callResultMsg struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *errorInfo      `json:"error,omitempty"`
}

type widgetPushMsg struct {
	Key   string   `json:"key"`
	Lines []string `json:"lines"`
}

type cancelMsg struct {
	RequestID string `json:"request_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type pingMsg struct {
	Nonce string `json:"nonce"`
}
type pongMsg struct {
	Nonce string `json:"nonce"`
}
type requestStateMsg struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type shutdownMsg struct {
	Reason string `json:"reason"`
}

type errorInfo struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// conn manages the socket connection with read/write goroutines and
// request/response correlation for extension→host calls.
type conn struct {
	nc      net.Conn
	writeMu sync.Mutex // serializes writes

	// callID generates unique IDs for extension→host calls.
	callID atomic.Int64

	// pending tracks outstanding call requests awaiting call_result.
	pendingMu      sync.Mutex
	pending        map[string]chan *callResultMsg
	pendingParents map[string]string

	// incoming is where the read loop puts request/notify/shutdown messages.
	incoming chan envelope

	// done is closed when the connection is shutting down.
	done chan struct{}

	// notifications counts notify frames queued for the main loop. The read
	// loop increments it before it delivers any later call_result, so a caller
	// can wait until the loop has applied every notification that preceded
	// its result.
	notifications atomic.Uint64
}

type pendingCall struct {
	method string
	ch     <-chan *callResultMsg
}

func newConn(nc net.Conn) *conn {
	return &conn{
		nc:             nc,
		pending:        make(map[string]chan *callResultMsg),
		pendingParents: make(map[string]string),
		incoming:       make(chan envelope, 16),
		done:           make(chan struct{}),
	}
}

// start begins the read loop goroutine.
func (c *conn) start() {
	go c.readLoop()
}

func (c *conn) readLoop() {
	defer func() {
		c.pendingMu.Lock()
		clear(c.pending)
		clear(c.pendingParents)
		c.pendingMu.Unlock()
		close(c.done)
	}()
	defer close(c.incoming)
	for {
		data, err := c.readFrame()
		if err != nil {
			return
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue // skip malformed
		}
		if env.Type == msgPing && env.Ping != nil {
			if err := c.send(envelope{Type: msgPong, Pong: &pongMsg{Nonce: env.Ping.Nonce}}); err != nil {
				return
			}
			continue
		}
		// Route call_result to pending callers.
		if env.Type == msgCallResult {
			c.pendingMu.Lock()
			ch, ok := c.pending[env.ID]
			if ok {
				delete(c.pending, env.ID)
				delete(c.pendingParents, env.ID)
			}
			c.pendingMu.Unlock()
			if ok && ch != nil {
				ch <- env.CallResult
			}
			continue
		}
		// Everything else goes to the main loop.
		if env.Type == msgNotify {
			c.notifications.Add(1)
		}
		c.incoming <- env
	}
}

func (c *conn) readFrame() ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.nc, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > MaxFrameSize {
		return nil, fmt.Errorf("frame too large: %d bytes", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(c.nc, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *conn) writeFrame(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := c.nc.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.nc.Write(data)
	return err
}

func (c *conn) send(env envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(data) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d bytes exceeds %d", len(data), MaxFrameSize)
	}
	return c.writeFrame(data)
}

// call sends a call message and blocks until the host replies with call_result.
func (c *conn) call(method string, args any) (*callResultMsg, error) {
	return c.callFor("", method, args)
}

func (c *conn) callFor(parentRequestID, method string, args any) (*callResultMsg, error) {
	pending, err := c.beginCallFor(parentRequestID, method, args)
	if err != nil {
		return nil, err
	}
	return c.waitCall(pending)
}

// beginCall sends a host call and returns only after its frame has been
// written. Streaming APIs use this to establish the host-side consumer before
// sending ordered notify frames on the same connection.
func (c *conn) beginCallFor(parentRequestID, method string, args any) (pendingCall, error) {
	id := fmt.Sprintf("c%d", c.callID.Add(1))

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return pendingCall{}, err
	}

	ch := make(chan *callResultMsg, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingParents[id] = parentRequestID
	c.pendingMu.Unlock()

	if err := c.send(envelope{
		Type: msgCall,
		ID:   id,
		Call: &callMsg{Method: method, Args: argsJSON, ParentRequestID: parentRequestID},
	}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		delete(c.pendingParents, id)
		c.pendingMu.Unlock()
		return pendingCall{}, err
	}
	return pendingCall{method: method, ch: ch}, nil
}

func (c *conn) waitCall(call pendingCall) (*callResultMsg, error) {
	select {
	case result, ok := <-call.ch:
		if !ok {
			return nil, fmt.Errorf("host call %s cancelled with its parent request", call.method)
		}
		return result, nil
	case <-c.done:
		return nil, fmt.Errorf("connection closed while waiting for %s response", call.method)
	}
}

func (c *conn) cancelParentCalls(parentRequestID string) {
	c.pendingMu.Lock()
	var cancelled []chan *callResultMsg
	for id, parent := range c.pendingParents {
		if parent != parentRequestID {
			continue
		}
		if ch := c.pending[id]; ch != nil {
			cancelled = append(cancelled, ch)
		}
		delete(c.pending, id)
		delete(c.pendingParents, id)
	}
	c.pendingMu.Unlock()
	for _, ch := range cancelled {
		close(ch)
	}
}

func (c *conn) notify(method string, args any) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return c.send(envelope{
		Type:   msgNotify,
		Notify: &notifyMsg{Method: method, Args: argsJSON},
	})
}

// respond sends a response to a host request.
func (c *conn) respond(id string, result any, respErr error) error {
	if err := c.requestState(id, "completed", ""); err != nil {
		return err
	}
	resp := &responseMsg{}
	if respErr != nil {
		resp.Error = &errorInfo{Message: respErr.Error()}
	}
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			resp.Error = &errorInfo{Message: err.Error()}
		} else {
			resp.Result = data
		}
	}
	return c.send(envelope{Type: msgResponse, ID: id, Response: resp})
}

func (c *conn) requestState(id, state, reason string) error {
	return c.send(envelope{
		Type:         msgRequestState,
		RequestState: &requestStateMsg{RequestID: id, State: state, Reason: reason},
	})
}

// pushWidget sends a widget_push message (fire-and-forget).
func (c *conn) pushWidget(key string, lines []string) error {
	return c.send(envelope{
		Type:       msgWidgetPush,
		WidgetPush: &widgetPushMsg{Key: key, Lines: lines},
	})
}
