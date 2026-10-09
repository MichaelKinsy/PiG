package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's withSession callback runs in the extension that requested the replacement, with a context of the replacement Session (agent-session-runtime.ts:187-194, agent-session.ts:4325-4333). Here that extension is a process of the outgoing Session's host. The host sends it a with_session request on the connection that made the call and serves the calls of that request from the replacement: the extension's member in the replacement Session's host answers context calls, and the replacement context's awaited sendMessage and sendUserMessage run on the replacement Session.

// withSessionRoute is the replacement Session that serves the calls of one with_session request.
type withSessionRoute struct {
	// target and member are the replacement Session's host and the requesting extension's member in it; nil when the replacement does not load that extension.
	target   *Host
	member   *managedExt
	replaced *extension.ReplacedSessionContext
	// manager is the replacement Session's SessionManager for the calls of a setup request; nil for a with_session request.
	manager any
}

// SetReplacementHost installs the resolver of the host that serves the Session replacing this host's Session. The factory that builds each Session's host installs it; without it, a replacement context serves only its awaited messages.
func (h *Host) SetReplacementHost(resolve func() *Host) {
	h.replacementHost.Store(&resolve)
}

func (c *Conn) withSessionRoute(requestID string) *withSessionRoute {
	if requestID == "" {
		return nil
	}
	c.withSessionMu.Lock()
	defer c.withSessionMu.Unlock()
	return c.withSessionRoutes[requestID]
}

func (c *Conn) setWithSessionRoute(requestID string, route *withSessionRoute) {
	c.withSessionMu.Lock()
	defer c.withSessionMu.Unlock()
	if route == nil {
		delete(c.withSessionRoutes, requestID)
		return
	}
	if c.withSessionRoutes == nil {
		c.withSessionRoutes = make(map[string]*withSessionRoute)
	}
	c.withSessionRoutes[requestID] = route
}

// withSessionCallback returns the WithSession option of a newSession, fork or switchSession call that extName made on owner under handle.
func (h *Host) withSessionCallback(ctx context.Context, extName string, owner *Conn, handle string) func(*extension.ReplacedSessionContext) error {
	// pig additive (D19): the callback crosses the subprocess wire as a with_session request whose calls the replacement Session serves.
	return func(replaced *extension.ReplacedSessionContext) error {
		route := &withSessionRoute{replaced: replaced}
		if resolve := h.replacementHost.Load(); resolve != nil {
			if target := (*resolve)(); target != nil {
				route.target = target
				target.mu.Lock()
				route.member = target.exts[extName]
				target.mu.Unlock()
			}
		}
		args := WithSessionArgs{Handle: handle, Ready: route.ready()}
		data, err := json.Marshal(args)
		if err != nil {
			return fmt.Errorf("encode with_session request: %w", err)
		}
		env := &Envelope{Type: MsgRequest, ID: fmt.Sprintf("r%d", owner.nextID.Add(1)), Request: &RequestPayload{Method: RequestWithSession, Args: data}}
		owner.setWithSessionRoute(env.ID, route)
		defer owner.setWithSessionRoute(env.ID, nil)
		resp, err := owner.Request(ctx, env)
		if err != nil {
			return err
		}
		if resp.Response != nil && resp.Response.Error != nil {
			return resp.Response.Error.ToError()
		}
		return nil
	}
}

// setupCallback returns the Setup option of a newSession call that made extName's connection owner name the setup callback handle. It crosses the wire as a setup request whose sessionRead and sessionWrite calls act on the replacement Session's SessionManager, which Pi hands the callback (agent-session-runtime.ts:254-257).
func (h *Host) setupCallback(ctx context.Context, extName string, owner *Conn, handle string) func(any) error {
	// pig additive (D19): the callback crosses the subprocess wire as a setup request whose calls the replacement Session serves.
	return func(manager any) error {
		route := &withSessionRoute{manager: manager}
		if resolve := h.replacementHost.Load(); resolve != nil {
			if target := (*resolve)(); target != nil {
				route.target = target
				target.mu.Lock()
				route.member = target.exts[extName]
				target.mu.Unlock()
			}
		}
		data, err := json.Marshal(SetupArgs{Handle: handle})
		if err != nil {
			return fmt.Errorf("encode setup request: %w", err)
		}
		env := &Envelope{Type: MsgRequest, ID: fmt.Sprintf("r%d", owner.nextID.Add(1)), Request: &RequestPayload{Method: RequestSetup, Args: data}}
		owner.setWithSessionRoute(env.ID, route)
		defer owner.setWithSessionRoute(env.ID, nil)
		resp, err := owner.Request(ctx, env)
		if err != nil {
			return err
		}
		if resp.Response != nil && resp.Response.Error != nil {
			return resp.Response.Error.ToError()
		}
		return nil
	}
}

// sessionWriter is the append surface of Pi's SessionManager that a setup callback uses (session-manager.ts:218-480); the replacement Session's manager has these methods.
type sessionWriter interface {
	AppendMessage(extension.SessionMessage) (string, error)
	AppendCustomEntry(customType string, data any) (string, error)
	AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error)
	AppendSessionInfo(name string) (string, error)
	AppendModelChange(provider, modelID string) (string, error)
	AppendThinkingLevelChange(level string) (string, error)
	AppendLabelChange(targetID string, label *string) (string, error)
}

// runSessionWrite applies one SessionManager append of a setup request to the replacement Session's manager and answers the new entry id.
func (r *withSessionRoute) runSessionWrite(conn *Conn, callID string, call *CallPayload) {
	callCtx, release := conn.hostCallContext(call.ParentRequestID, callID)
	defer release()
	reply := func(result *CallResultPayload) {
		if callID != "" {
			_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: result})
		}
	}
	if callCtx.Err() != nil {
		return
	}
	writer, ok := r.manager.(sessionWriter)
	if !ok {
		reply(&CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: "sessionWrite is not available: the replacement Session's manager cannot append"}})
		return
	}
	id, err := applySessionWrite(writer, call.Args)
	if err != nil {
		reply(&CallResultPayload{Error: &ErrorInfo{Message: err.Error()}})
		return
	}
	result, _ := json.Marshal(id)
	reply(&CallResultPayload{Result: result})
}

// applySessionWrite decodes a sessionWrite call, {"method": name, "args": {...}}, and appends to writer.
func applySessionWrite(writer sessionWriter, raw json.RawMessage) (string, error) {
	var call struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		return "", fmt.Errorf("parse sessionWrite args: %w", err)
	}
	switch call.Method {
	case "appendMessage":
		var p struct {
			Message agent.AgentMessage `json:"message"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendMessage args: %w", err)
		}
		return writer.AppendMessage(p.Message)
	case "appendCustomEntry":
		var p struct {
			CustomType string `json:"customType"`
			Data       any    `json:"data"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendCustomEntry args: %w", err)
		}
		return writer.AppendCustomEntry(p.CustomType, p.Data)
	case "appendCustomMessageEntry":
		var p struct {
			CustomType string `json:"customType"`
			Content    any    `json:"content"`
			Display    bool   `json:"display"`
			Details    any    `json:"details"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendCustomMessageEntry args: %w", err)
		}
		return writer.AppendCustomMessageEntry(p.CustomType, p.Content, p.Display, p.Details)
	case "appendSessionInfo":
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendSessionInfo args: %w", err)
		}
		return writer.AppendSessionInfo(p.Name)
	case "appendModelChange":
		var p struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendModelChange args: %w", err)
		}
		return writer.AppendModelChange(p.Provider, p.ModelID)
	case "appendThinkingLevelChange":
		var p struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendThinkingLevelChange args: %w", err)
		}
		return writer.AppendThinkingLevelChange(p.ThinkingLevel)
	case "appendLabelChange":
		var p struct {
			TargetID string  `json:"targetId"`
			Label    *string `json:"label"`
		}
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return "", fmt.Errorf("parse appendLabelChange args: %w", err)
		}
		return writer.AppendLabelChange(p.TargetID, p.Label)
	}
	return "", fmt.Errorf("unknown sessionWrite method %q", call.Method)
}

// ready is the replacement Session's ready payload as its host would send it to the member.
func (r *withSessionRoute) ready() *ReadyPayload {
	if r.target == nil {
		return nil
	}
	ready := &ReadyPayload{Cwd: r.target.cwd, Mode: r.target.mode}
	if r.target.uiBridge != nil {
		var flags []string
		if r.member != nil {
			flags = r.member.flagNames
		}
		ready.State = r.target.uiBridge.Snapshot(flags, 0, false)
	}
	return ready
}

// awaitsReplacementMessage reports whether a route runs method as the replacement context's awaited message operation.
func awaitsReplacementMessage(method string) bool {
	return method == "sendMessage" || method == "sendUserMessage"
}

// run applies one call of the with_session request. The result goes to conn, the requesting extension's connection.
func (r *withSessionRoute) run(conn *Conn, callID string, call *CallPayload, mark func()) {
	if call.Method == "sessionWrite" && r.manager != nil {
		r.runSessionWrite(conn, callID, call)
		return
	}
	if awaitsReplacementMessage(call.Method) {
		r.runMessage(conn, callID, call)
		return
	}
	if r.target == nil || r.member == nil {
		_, release := conn.hostCallContext(call.ParentRequestID, callID)
		release()
		if callID != "" {
			_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: &CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: call.Method + " is not available: the replacement Session does not load this extension"}}})
		}
		return
	}
	r.target.runReplacementCall(r.member, conn, callID, call, mark)
}

// runReplacementCall applies a replacement context's call as a context call of member, the requesting extension's member in this host, and answers on conn. Only context calls reach a replacement context, so the UI bridge serves them all; the replacement Session's later invalidation rejects them with its stale message.
func (h *Host) runReplacementCall(member *managedExt, conn *Conn, callID string, call *CallPayload, mark func()) {
	callCtx, releaseCall := conn.hostCallContext(call.ParentRequestID, callID)
	defer releaseCall()
	reply := func(result *CallResultPayload) {
		if callID != "" && result != nil {
			_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: result})
		}
	}
	if callCtx.Err() != nil {
		return
	}
	if message := h.staleMessage.Load(); message != nil {
		reply(&CallResultPayload{Error: &ErrorInfo{Message: *message}})
		return
	}
	if member.replaced.Load() || h.uiBridge == nil || call.Method == "watchSessionLog" {
		reply(&CallResultPayload{Error: &ErrorInfo{Code: "unsupported", Message: call.Method + " is not available on a replacement context"}})
		return
	}
	if takesInteractiveFocus(call.Method) {
		defer conn.countHostCall(call.ParentRequestID, true)()
	} else if startsAsync(call.Method) {
		defer conn.countHostCall(call.ParentRequestID, false)()
	}
	if mark != nil {
		callCtx = extension.WithCallInitiation(callCtx, mark)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			message := fmt.Sprintf("panic in HandleCall for %s.%s: %v", member.config.Name, call.Method, recovered)
			fmt.Fprintf(os.Stderr, "%s\n", message)
			reply(&CallResultPayload{Error: &ErrorInfo{Code: "internal_panic", Message: message}})
		}
	}()
	defer h.uiBridge.releaseAutocompleteCall(conn, call)
	result, err := h.uiBridge.handleCall(callCtx, member.config.Name, conn, call)
	if err != nil {
		reply(&CallResultPayload{Error: &ErrorInfo{Message: err.Error()}})
		return
	}
	reply(result)
}

// runMessage runs the replacement context's sendMessage or sendUserMessage, which await the Session operation (agent-session.ts:4330-4331).
func (r *withSessionRoute) runMessage(conn *Conn, callID string, call *CallPayload) {
	callCtx, release := conn.hostCallContext(call.ParentRequestID, callID)
	defer release()
	var err error
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(os.Stderr, "panic in replacement %s: %v\n", call.Method, recovered)
			err = fmt.Errorf("panic in replacement %s: %v", call.Method, recovered)
		}
		if callID == "" {
			return
		}
		result := &CallResultPayload{}
		if err != nil {
			result.Error = &ErrorInfo{Message: err.Error()}
		}
		_ = conn.Send(&Envelope{Type: MsgCallResult, ID: callID, CallResult: result})
	}()
	if callCtx.Err() != nil {
		err = errors.New("host call cancelled with its parent request")
		return
	}
	switch call.Method {
	case "sendMessage":
		var p struct {
			Message extension.CustomMessageRef    `json:"message"`
			Options *extension.SendMessageOptions `json:"options"`
		}
		if err = json.Unmarshal(call.Args, &p); err != nil {
			err = fmt.Errorf("parse sendMessage args: %w", err)
			return
		}
		err = r.replaced.SendMessage(p.Message, p.Options)
	case "sendUserMessage":
		var p struct {
			Content any                                              `json:"content"`
			Options *extension.ReplacedSessionSendUserMessageOptions `json:"options"`
		}
		if err = json.Unmarshal(call.Args, &p); err != nil {
			err = fmt.Errorf("parse sendUserMessage args: %w", err)
			return
		}
		err = r.replaced.SendUserMessage(p.Content, p.Options)
	}
}
