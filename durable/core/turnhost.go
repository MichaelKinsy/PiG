// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package core

import (
	"errors"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/docs"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/hopcost"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// newTurnHost is the Harness the built-in tasks run against (turn.Host): settings and models from the open payload,
// committed state from the Session and its mirror, and the effects of ABI section 6 for everything host code does.
func (h *handler) newTurnHost() *turn.Host {
	return &turn.Host{
		Now:            func() float64 { return h.now },
		Settings:       h.turnSettings,
		Agent:          h.resolveAgent,
		ContextWindow:  h.contextWindow,
		KeptRange:      h.keptRange,
		Context:        h.turnContext,
		RequestContext: h.requestContext,
		Render:         h.renderSections,
		Stream:         h.stream,
		Hooks:          func(name string, conv turn.ID) int { return len(h.hookHandlers(name, conv)) },
		HookExtension:  h.hookExtension,
		Hook:           h.callHook,
		FetchDeferred: func(model turn.ModelRef, handle *jv.Object, cancel *session.Cancel, then func(*jv.Object, error)) {
			h.deferredEffect("fetch", model, handle, cancel, func(v any, err error) { then(asObject(v), err) })
		},
		CancelDeferred: func(model turn.ModelRef, handle *jv.Object, cancel *session.Cancel, then func(error)) {
			h.deferredEffect("cancel", model, handle, cancel, func(_ any, err error) { then(err) })
		},
		SnapshotLoaded: func(def *session.DocDef, conv turn.ID) bool { return h.sess.Loaded(def, session.DocArgs{Owner: conv}) },
		Snapshot: func(def *session.DocDef, conv turn.ID) (*jv.Object, error) {
			return h.sess.Snapshot(def, session.DocArgs{Owner: conv})
		},
		// runtime.entry reads storage.entry(conversationId, id) on the line (scheduler.ts :1216-1228): the conversation's row, then the entry.
		Entry: func(id turn.ID) (*jv.Object, error) {
			h.read(hopcost.ReadEntryIn)
			h.job.direct = true
			e, _, err := h.mir.Entry(id)
			return e, err
		},
		AddTools: docs.AddTools,
		PrepareArguments: func(tool string, args *jv.Object, then func(any, string, error)) {
			h.checkArguments("prepareArguments", tool, nil, args, then)
		},
		ValidateArguments: func(tool string, call *jv.Object, args any, then func(*jv.Object, string, error)) {
			h.checkArguments("validateArguments", tool, call, args, func(checked any, errText string, err error) { then(asObject(checked), errText, err) })
		},
		CheckStructuredOutput: func(tool string, result *jv.Object, then func(string, error)) {
			payload := jobj("toolName", tool, "result", result)
			h.hookEffect("checkStructuredOutput", 0, 0, 0, payload, nil, func(v any, err error) {
				text, _ := valueOf(asObject(v), "error").(string)
				then(text, err)
			})
		},
		RunTool:   h.runTool,
		MaxTokens: func(model turn.ModelRef) float64 { return numOf(h.catalogueModel(model), "maxTokens") },
		Complete: func(req *turn.CompleteRequest, cancel *session.Cancel, then func(*jv.Object, error)) {
			payload := jv.NewObject()
			m := jv.NewObject()
			m.Set("provider", req.Model.Provider)
			m.Set("modelId", req.Model.ModelID)
			payload.Set("model", m)
			payload.Set("context", req.Context)
			if req.Options != nil {
				payload.Set("options", req.Options)
			}
			h.hookEffect("complete", 0, 0, 0, payload, cancel, func(v any, err error) { then(asObject(v), err) })
		},
		After:        h.after,
		NewSessionID: h.uuid,
		Hops:         h.hopsThen,
		ScanHops:     h.scanThen,
	}
}

// turnSettings reads the Harness settings the host resolved over their defaults (open payload, resolveSettings).
func (h *handler) turnSettings() turn.Settings {
	s := objOf(h.open, "settings")
	retry, compaction, progress := objOf(s, "retry"), objOf(s, "compaction"), objOf(s, "progress")
	out := turn.Settings{
		MaxRetries: numOf(retry, "maxRetries"), BaseDelayMs: numOf(retry, "baseDelayMs"),
		Compaction: history.Policy{ReserveTokens: numOf(compaction, "reserveTokens"), KeepRecentTokens: numOf(compaction, "keepRecentTokens"),
			BackgroundTokens: numOf(compaction, "backgroundTokens")},
		PartialIntervalMs: numOf(progress, "partialIntervalMs"), OutputIntervalMs: numOf(progress, "outputIntervalMs"),
		Stream: objOf(s, "stream"), ToolExecution: strOf(s, "toolExecution"), Queue: h.queueModes(), AfterFailedRun: h.afterFailedRun(),
	}
	out.RetryEnabled, _ = boolOf(retry, "enabled")
	out.Compaction.Enabled, _ = boolOf(compaction, "enabled")
	if overrides := objOf(compaction, "modelOverrides"); overrides != nil {
		out.CompactionOverrides = map[string]history.PolicyOverride{}
		overrides.Members(func(key string, value any) bool {
			entry := asObject(value)
			var override history.PolicyOverride
			if v, ok := boolOf(entry, "enabled"); ok {
				override.Enabled = &v
			}
			for name, field := range map[string]**float64{"reserveTokens": &override.ReserveTokens, "keepRecentTokens": &override.KeepRecentTokens, "backgroundTokens": &override.BackgroundTokens} {
				if v, ok := valueOf(entry, name).(float64); ok {
					*field = &v
				}
			}
			out.CompactionOverrides[key] = override
			return true
		})
	}
	if v, ok := valueOf(retry, "maxAgentDelayMs").(float64); ok {
		out.MaxAgentDelayMs = &v
	}
	if out.ToolExecution == "" {
		out.ToolExecution = "parallel"
	}
	return out
}

// contextWindow is the catalogue entry's contextWindow of a model the host lets the core name (open payload models).
func (h *handler) contextWindow(model turn.ModelRef) (float64, bool) {
	m := h.catalogueModel(model)
	return numOf(m, "contextWindow"), m != nil
}

// catalogueModel is the open payload's catalogue entry of a model, or nil.
func (h *handler) catalogueModel(model turn.ModelRef) *jv.Object {
	list, _ := valueOf(h.open, "models").([]any)
	for _, m := range list {
		o := asObject(m)
		if strOf(o, "provider") == model.Provider && strOf(o, "id") == model.ModelID {
			return o
		}
	}
	return nil
}

func limitsOf(o *jv.Object) turn.ToolLimits {
	var l turn.ToolLimits
	if v, ok := valueOf(o, "maxBytes").(float64); ok {
		n := int(v)
		l.MaxBytes = &n
	}
	if v, ok := valueOf(o, "maxLines").(float64); ok {
		n := int(v)
		l.MaxLines = &n
	}
	l.Retain = strOf(o, "retain")
	return l
}

// checkArguments asks the host to repair (prepareArguments) or validate (validateArguments) a call's arguments: the host
// answers {arguments} or {error} with the text of what threw (harness/tool.ts prepare, validate). A repair's arguments are
// any JSON value: Pi validates whatever prepareArguments returned, and its error text shows that value.
func (h *handler) checkArguments(name, tool string, call *jv.Object, args any, then func(any, string, error)) {
	payload := jv.NewObject()
	payload.Set("toolName", tool)
	if call != nil {
		payload.Set("call", call)
	}
	payload.Set("arguments", args)
	h.hookEffect(name, 0, 0, 0, payload, nil, func(v any, err error) {
		o := asObject(v)
		if err != nil {
			then(nil, "", err)
			return
		}
		if text, failed := valueOf(o, "error").(string); failed {
			then(nil, text, nil)
			return
		}
		then(valueOf(o, "arguments"), "", nil)
	})
}

// runTool is the tool effect {taskId, conversationId, toolName, callId, arguments, outputWindow, replay, handler}: its
// tool_progress events report output (kind 0, skipped-framed when skipped > 0), details (kind 1, acknowledged with
// progress_ack once committed) and diagnostics (kind 2); tool_done carries the result or what execute threw.
func (h *handler) runTool(run *turn.ToolRun, io *turn.ToolIO, cancel *session.Cancel, then func(*jv.Object, error)) {
	// tool.ts builds the environment for this call (runtime.env) right before execute; a throw there fails the call like
	// a throw from execute.
	envBody := jv.NewObject()
	envBody.Set("conversationId", float64(run.Conversation))
	envBody.Set("taskId", float64(run.Task))
	if agent := h.agents[run.Conversation]; agent != nil && agent.Cwd != "" {
		envBody.Set("cwd", agent.Cwd)
	}
	h.completion(abi.EffectEnv, envBody, cancel, func(_ any, err error) {
		if err != nil {
			then(nil, err)
			return
		}
		h.startTool(run, io, cancel, then)
	})
}

func (h *handler) startTool(run *turn.ToolRun, io *turn.ToolIO, cancel *session.Cancel, then func(*jv.Object, error)) {
	body := jv.NewObject()
	body.Set("taskId", float64(run.Task))
	body.Set("conversationId", float64(run.Conversation))
	body.Set("toolName", run.Name)
	body.Set("callId", run.CallID)
	body.Set("arguments", run.Arguments)
	if run.OutputWindow != nil {
		body.Set("outputWindow", run.OutputWindow)
	}
	replay, handler := "unsafe", -1
	if agent := h.agents[run.Conversation]; agent != nil {
		for _, t := range agent.Tools {
			if strOf(t.Declaration, "name") == run.Name {
				handler = t.Handler
				if t.Replay != "" {
					replay = t.Replay
				}
				break
			}
		}
	}
	body.Set("replay", replay)
	if handler >= 0 {
		body.Set("handler", float64(handler))
	}
	var remove func()
	id := h.startEffect(abi.EffectTool, jv.Append(nil, body), &pending{
		progress: func(ev *abi.Event) bool {
			payload := append([]byte(nil), ev.Payload...)
			switch ev.Phase {
			case 0:
				var skip *turn.Skip
				data := payload
				if ev.Skipped > 0 && len(payload) >= 4 {
					n := int(uint32(payload[0]) | uint32(payload[1])<<8 | uint32(payload[2])<<16 | uint32(payload[3])<<24)
					meta, _ := jv.Parse(payload[4 : 4+n])
					m := asObject(meta)
					ends, _ := boolOf(m, "endsWithNewline")
					skip = &turn.Skip{Bytes: int(numOf(m, "bytes")), Newlines: int(numOf(m, "newlines")), EndsWithNewline: ends}
					data = payload[4+n:]
				}
				_ = io.Output(data, utf8.Valid(data), skip)
			case 1:
				value, _ := jv.Parse(payload)
				wait := ev.WaitID
				h.loop.Defer(func() { io.Details(value, func(err error) { h.progressAck(wait, err) }) })
			case 2:
				value, _ := jv.Parse(payload)
				h.loop.Defer(func() { _ = io.Diagnostic(asObject(value)) })
			}
			return false
		},
		done: func(outcome uint8, payload []byte) {
			if remove != nil {
				remove()
			}
			value, err := jv.Parse(payload)
			if err == nil && outcome == abi.OutcomeUnrecordable {
				err = &turn.ResultFault{Text: strOf(asObject(value), "message")}
			} else if err == nil && outcome != abi.OutcomeResult {
				err = thrown(value)
			}
			h.loop.Defer(func() { then(asObject(value), err) })
		},
	})
	if cancel != nil {
		// Aborting the invocation aborts the tool's signal and nothing else: tool.ts awaits `tool.execute` itself and the call ends
		// when it settles (tool.ts:260-270), so the host still reports tool_done and the core settles on it.
		remove = cancel.OnAbort(func() { h.cancelEffect(id) })
	}
}

// progressAck answers a details update: u32 waitId, u8 outcome (0 committed, 1 rejected), then the error JSON.
func (h *handler) progressAck(wait uint32, err error) {
	out := []byte{byte(wait), byte(wait >> 8), byte(wait >> 16), byte(wait >> 24), 0}
	if err != nil {
		out[4] = 1
		out = append(out, errorJSON(err)...)
	}
	h.b.Notice(abi.NoticeProgressAck, out)
}

// hookEffect starts a hook effect {name, handler, conversationId, taskId, payload} and delivers its hook_done.
func (h *handler) hookEffect(name string, handler int, conv, task turn.ID, payload any, cancel *session.Cancel, then func(any, error)) {
	body := jv.NewObject()
	body.Set("name", name)
	body.Set("handler", float64(handler))
	body.Set("conversationId", float64(conv))
	body.Set("taskId", float64(task))
	body.Set("payload", payload)
	h.completion(abi.EffectHook, body, cancel, then)
}

// completion starts an effect answered by hook_done (outcome 0 a JSON value, 1 a thrown {name, message}); cancelling
// the signal cancels the effect. The continuation runs on the Loop.
func (h *handler) completion(kind abi.EffectKind, body *jv.Object, cancel *session.Cancel, then func(any, error)) {
	var remove func()
	// runtime.env is `#read` (async, returns read()) over buildEnv (async) (scheduler.ts:1161, :1269; harness.ts:226): #read's
	// promise adopts buildEnv's, which costs a resolve-thenable job a hop after the call and a reaction a hop after both that job
	// ran and buildEnv settled, and the caller's await resumes a hop later. The caller resumes at the later of three hops after
	// the call (a buildEnv that settles at once, as without an `env` option) and two hops after the host's answer.
	left := 1
	var value any
	var err error
	resume := func() {
		if left--; left == 0 {
			then(value, err)
		}
	}
	if kind == abi.EffectEnv {
		left++
		h.hopsThen(envCallHops, resume)
	}
	id := h.startEffect(kind, jv.Append(nil, body), &pending{done: func(outcome uint8, payload []byte) {
		if remove != nil {
			remove()
		}
		value, err = jv.Parse(payload)
		if err == nil && outcome != abi.OutcomeResult {
			err = thrown(value)
		}
		if kind == abi.EffectEnv {
			h.hopsThen(envAnswerHops, resume)
			return
		}
		h.loop.Defer(resume)
	}})
	if cancel != nil {
		// Aborting the signal only signals the handler: Pi awaits it directly (tool.ts:67 beforeTool, scheduler hooks, generation.ts:229
		// fetchDeferred), so the host still reports the handler's end and the core settles on it.
		remove = cancel.OnAbort(func() { h.cancelEffect(id) })
	}
}

// envCallHops and envAnswerHops bound when runtime.env's caller resumes: three hops after the call, two after buildEnv settles.
const (
	envCallHops   = 3
	envAnswerHops = 2
)

// resolveAgent is runtime.agent(): the host composes the agent from pi.agent and the registry (resolveAgent hook).
func (h *handler) resolveAgent(conv turn.ID, cancel *session.Cancel, then func(*turn.Agent, error)) {
	// runtime.agent() reads pi.agent through Session.snapshot: a loaded document answers from the cache without a job on
	// the Session line; otherwise the load is a line job that observes the commits queued before it.
	var state *jv.Object
	if h.sess.Loaded(docs.AgentDef, session.DocArgs{Owner: conv}) {
		var err error
		if state, err = h.sess.Snapshot(docs.AgentDef, session.DocArgs{Owner: conv}); err != nil {
			h.loop.Defer(func() { then(nil, err) })
			return
		}
		h.requestAgent(conv, state, cancel, then)
		return
	}
	h.loop.Line(func() error {
		var err error
		state, err = h.sess.Snapshot(docs.AgentDef, session.DocArgs{Owner: conv})
		return err
	}, func(err error) {
		if err != nil {
			then(nil, err)
			return
		}
		h.requestAgent(conv, state, cancel, then)
	})
}

func (h *handler) requestAgent(conv turn.ID, state *jv.Object, cancel *session.Cancel, then func(*turn.Agent, error)) {
	h.requestAgentRaw(state, conv, cancel, func(v any, err error) {
		if err != nil {
			then(nil, err)
			return
		}
		agent := agentOf(asObject(v))
		if h.agents == nil {
			h.agents = map[turn.ID]*turn.Agent{}
		}
		h.agents[conv] = agent
		then(agent, nil)
	})
}

// requestAgentRaw asks the host to resolve the agent from the stored pi.agent state (resolveAgent hook) and hands over its JSON.
func (h *handler) requestAgentRaw(state *jv.Object, conv turn.ID, cancel *session.Cancel, then func(any, error)) {
	payload := jv.NewObject()
	if state != nil {
		payload.Set("state", state)
	}
	h.hookEffect("resolveAgent", 0, conv, 0, payload, cancel, then)
}

// agentOf reads the host's resolved agent JSON (PROTOCOL.md, Resolved agent JSON).
func agentOf(o *jv.Object) *turn.Agent {
	a := &turn.Agent{ThinkingLevel: strOf(o, "thinkingLevel")}
	if m := objOf(o, "model"); m != nil {
		a.Model = &turn.ModelRef{Provider: strOf(m, "provider"), ModelID: strOf(m, "modelId")}
	}
	toolsOf := func(key string) []turn.Tool {
		var out []turn.Tool
		list, _ := valueOf(o, key).([]any)
		for _, t := range list {
			to := asObject(t)
			structured, _ := boolOf(to, "structuredOutput")
			out = append(out, turn.Tool{Declaration: turn.ToolDeclaration(to), ExecutionMode: strOf(to, "executionMode"),
				Handler: int(numOf(to, "handler")), Replay: strOf(to, "replay"), Raw: to, Limits: limitsOf(objOf(to, "outputLimits")), StructuredOutput: structured, Extension: strOf(to, "extension")})
		}
		return out
	}
	a.Tools, a.Callable = toolsOf("tools"), toolsOf("callable")
	sections, _ := valueOf(o, "sections").([]any)
	for _, s := range sections {
		so := asObject(s)
		tag, has := boolOf(so, "tag")
		a.Sections = append(a.Sections, turn.Section{Key: strOf(so, "key"), Tag: tag || !has, Handler: int(numOf(so, "handler")), Extension: strOf(so, "extension")})
	}
	hooks, _ := valueOf(o, "hooks").([]any)
	for _, hk := range hooks {
		table := map[string]int{}
		if handlers := objOf(asObject(hk), "handlers"); handlers != nil {
			handlers.Entries(func(name string, v any) bool {
				if f, ok := v.(float64); ok {
					table[name] = int(f)
				}
				return true
			})
		}
		a.HookHandlers = append(a.HookHandlers, table)
		a.HookExtensions = append(a.HookExtensions, strOf(asObject(hk), "extension"))
	}
	a.Instructions, a.Cwd = strOf(o, "instructions"), strOf(o, "cwd")
	return a
}

// renderSections is generation.ts's environment build (an env effect for the task) followed by prompt.ts renderSections:
// each section renders in order through a section effect; text is wrapped as <key>\n…\n</key> unless tag is false, an
// undefined render omits the section, and a throwing one keeps the text the context shows (report).
func (h *handler) renderSections(conv, task turn.ID, agent *turn.Agent, shown *turn.Sections, cancel *session.Cancel, then func(*turn.Sections, error)) {
	envBody := jv.NewObject()
	envBody.Set("conversationId", float64(conv))
	envBody.Set("taskId", float64(task))
	if agent.Cwd != "" {
		envBody.Set("cwd", agent.Cwd)
	}
	h.completion(abi.EffectEnv, envBody, cancel, func(_ any, err error) {
		if err != nil {
			if cancel != nil && cancel.Aborted() {
				then(nil, err)
				return
			}
			h.reportFrom(task, err, session.ReportFrom{Callback: "env"})
		}
		shownObj := jv.NewObject()
		for _, k := range shown.Keys() {
			v, _ := shown.Get(k)
			shownObj.Set(k, v)
		}
		input := jv.NewObject()
		input.Set("shown", shownObj)
		desired := &turn.Sections{}
		var next func(i int)
		next = func(i int) {
			if i == len(agent.Sections) {
				// renderSections is async: its caller's `await` resumes one hop after the last section settled (prompt.ts:36, generation.ts:150).
				h.hopsThen(1, func() { then(desired, nil) })
				return
			}
			s := agent.Sections[i]
			body := jv.NewObject()
			body.Set("conversationId", float64(conv))
			body.Set("taskId", float64(task))
			body.Set("section", float64(i))
			body.Set("handler", float64(s.Handler))
			body.Set("input", input)
			h.completion(abi.EffectSection, body, cancel, func(v any, err error) {
				if err != nil {
					if cancel != nil && cancel.Aborted() {
						then(nil, err)
						return
					}
					h.reportFrom(task, err, session.ReportFrom{Extension: s.Extension})
					if old, ok := shown.Get(s.Key); ok {
						desired.Set(s.Key, old)
					}
				} else if text, isText := v.(string); isText {
					if s.Tag {
						text = "<" + s.Key + ">\n" + text + "\n</" + s.Key + ">"
					}
					desired.Set(s.Key, text)
				}
				next(i + 1)
			})
		}
		next(0)
	})
}

// stream is the model request: a model_context effect whose model_event batches carry pi-ai events; each partial goes to
// onPartial, and the batch ending in done or error ends the request. A batch is split, not parsed: a partial message is
// forwarded as the text the host sent (the request keeps only the last one it commits), and only the message that ends the
// request is built.
func (h *handler) stream(req *turn.StreamRequest, onPartial func(jv.Raw), cancel *session.Cancel, then func(*jv.Object, error)) {
	out := h.modelContextPayload(req)
	h.keepPayload(out)
	var remove func()
	finished := false
	id := h.startEffect(abi.EffectModelContext, out, &pending{progress: func(ev *abi.Event) bool {
		actions, err := streamActions(ev.Payload)
		if err != nil {
			finished = true
			h.loop.Defer(func() { then(nil, err) })
			return true
		}
		for _, a := range actions {
			switch a.kind {
			case actPartial:
				h.loop.Defer(func() { onPartial(a.partial) })
			case actDone, actError:
				finished = true
				h.loop.Defer(func() { then(a.message, nil) })
			case actFault:
				finished = true
				h.loop.Defer(func() { then(nil, errors.New(a.text)) })
			}
		}
		if finished && remove != nil {
			remove()
		}
		return finished
	}})
	if cancel != nil {
		// Aborting the invocation aborts the provider's signal and nothing else: generation.ts consumes the stream to its end
		// and classify() throws afterwards (generation.ts:415-426), so the request settles when the provider ends the stream
		// (the faux provider checks its signal at its next chunk), not at the abort.
		remove = cancel.OnAbort(func() { h.cancelEffect(id) })
	}
}

// streamActionKind is what one pi-ai event of a model_event batch asks of the request.
type streamActionKind uint8

const (
	actPartial streamActionKind = iota + 1 // a partial assistant message
	actDone                                // the request ended with a message
	actError                               // the request ended with an error message
	actFault                               // the host could not hand the final message over as strict JSON
)

// streamAction is one thing a batch does, in event order: a partial's text, the terminal message, or the fault's text.
type streamAction struct {
	kind    streamActionKind
	partial jv.Raw
	message *jv.Object
	text    string
}

// streamActions reads a model_event batch, a JSON array of pi-ai AssistantMessageEvents (types.ts). An event that is an object
// with a partial object gives a partial action (its text, copied: the host's buffer is reused by the next step), then one for
// its type: done (message), error (error) or fault (the host's message text). Any other event gives none, as does a batch that
// is not an array. It fails with the error jv.Parse gives when the batch is not JSON.
func streamActions(payload []byte) ([]streamAction, error) {
	events, err := jv.SplitArrayMembers(payload)
	if err != nil && !errors.Is(err, jv.ErrNotArray) {
		return nil, err
	}
	var actions []streamAction
	for _, members := range events {
		var partial, typ, message, failure jv.Raw
		for _, m := range members { // a member named twice counts at its last value, as in the parsed object
			switch m.Key {
			case "partial":
				partial = m.Raw
			case "type":
				typ = m.Raw
			case "message":
				message = m.Raw
			case "error":
				failure = m.Raw
			}
		}
		if isObjectText(partial) {
			actions = append(actions, streamAction{kind: actPartial, partial: append(jv.Raw(nil), partial...)})
		}
		switch textOf(typ) {
		case "done":
			actions = append(actions, streamAction{kind: actDone, message: objectOf(message)})
		case "error":
			actions = append(actions, streamAction{kind: actError, message: objectOf(failure)})
		case "fault":
			actions = append(actions, streamAction{kind: actFault, text: textOf(message)})
		}
	}
	return actions, nil
}

// isObjectText reports whether raw, the text of a JSON value Parse accepts, is an object.
func isObjectText(raw []byte) bool { return len(raw) > 0 && raw[0] == '{' }

// objectOf is the object that raw, the text of a JSON value Parse accepts, parses to; nil when the value is not an object.
func objectOf(raw []byte) *jv.Object {
	if !isObjectText(raw) {
		return nil
	}
	v, _ := jv.Parse(raw)
	o, _ := v.(*jv.Object)
	return o
}

// textOf is the string that raw, the text of a JSON value Parse accepts, parses to; "" when the value is not a string.
func textOf(raw []byte) string {
	if len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	v, _ := jv.Parse(raw)
	s, _ := v.(string)
	return s
}

// hookHandlers are the handlers of a hook name that the conversation's agent selects, in extension order (agent.ts agentHooks).
func (h *handler) hookHandlers(name string, conv turn.ID) []int {
	var out []int
	if agent := h.agents[conv]; agent != nil {
		for _, table := range agent.HookHandlers {
			if index, ok := table[name]; ok {
				out = append(out, index)
			}
		}
	}
	return out
}

// hookExtension is the name of the extension of handler index of a hook name that the conversation's agent selects, in extension
// order (agent.ts agentHooks).
func (h *handler) hookExtension(name string, index int, conv turn.ID) string {
	agent := h.agents[conv]
	if agent == nil {
		return ""
	}
	seen := 0
	for i, table := range agent.HookHandlers {
		if _, ok := table[name]; !ok {
			continue
		}
		if seen == index && i < len(agent.HookExtensions) {
			return agent.HookExtensions[i]
		}
		seen++
	}
	return ""
}

func (h *handler) callHook(name string, index int, conv, task turn.ID, payload *jv.Object, then func(any, error)) {
	handlers := h.hookHandlers(name, conv)
	if index >= len(handlers) {
		then(nil, errors.New("core: hook "+name+" has no handler "+itoa(int64(index))))
		return
	}
	// A hook handler runs under the invocation's signal: aborting the task cancels it (scheduler.ts hooks.each).
	var cancel *session.Cancel
	if rt := h.runtimes[task]; rt != nil {
		cancel = rt.Signal()
	}
	h.hookEffect(name, handlers[index], conv, task, payload, cancel, then)
}

// deferredEffect is models.fetchDeferred or models.cancelDeferred: a deferred effect {op, model, handle}.
func (h *handler) deferredEffect(op string, model turn.ModelRef, handle *jv.Object, cancel *session.Cancel, then func(any, error)) {
	body := jv.NewObject()
	body.Set("op", op)
	m := jv.NewObject()
	m.Set("provider", model.Provider)
	m.Set("modelId", model.ModelID)
	body.Set("model", m)
	body.Set("handle", handle)
	h.completion(abi.EffectDeferred, body, cancel, then)
}

// ThrownError is what host code (a tool, a hook, a section) threw: its text is the error's message, as errorText in
// harness/tool.ts reads it, and its name survives for a caller that classifies.
type ThrownError struct{ Name, Message string }

func (e *ThrownError) Error() string { return e.Message }

// thrown reads the {name, message} a host sends for a throw.
func thrown(value any) error {
	o := asObject(value)
	name := strOf(o, "name")
	if name == "" {
		name = "Error"
	}
	return &ThrownError{name, strOf(o, "message")}
}
