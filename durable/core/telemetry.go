// Ports packages/durable/src/harness/telemetry.ts (the vocabulary, DURABLE_TELEMETRY_SCHEMA) and its use in
// packages/durable/src/{harness/scheduler.ts, harness/submissions.ts, session/session.ts} (Pi 2ec55d80 "Durable and pi-ai telemetry").
//
// The spans are the host's: a JavaScript adapter's objects, which a state machine cannot hold. The core names them by the handle the
// host assigned (a call's `span`, or the answer of the `phaseSpan` hook for a phase) and tells it what to record in `telemetry`
// notices, one JSON op each:
//
//	{"op":"entry","span":S,"id":N,"kind":K}                       a committed entry's `pi.durable.entry` event
//	{"op":"submission","span":S,"id":N,"status":T,"answer":N}     a settled submission's `pi.durable.submission` event
//	{"op":"checkpoint","span":S,"phase":P}                        `pi.durable.checkpoint`
//	{"op":"replay","span":S,"tool":T}                             `pi.durable.replay`
//	{"op":"tool","span":S,"name":T,"callId":C}                    the called tool, as `tool.name` and `tool.call_id`
//	{"op":"end","span":S,"outcome":O,"next":P,"ended":E,"message":M}  the phase span's end: outcome, `next=<P>`, how a faulted task ended
//	{"op":"active","task":K,"delta":D}                            `pi.durable.tasks.active`
//
// The span of a commit's Context is `span` of the call that made it (submit, tx begin), or the invocation's phase span; zero is the
// NOOP span, which takes no event.

package core

import (
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// telOp starts the JSON of one op in a notice buffer: {"op":op,"span":span (when not zero).
func (h *handler) telOp(op string, span uint32) []byte {
	buf := append(h.payloadBuffer(192), `{"op":"`...)
	buf = append(buf, op...)
	buf = append(buf, '"')
	if span != 0 {
		buf = append(buf, `,"span":`...)
		buf = jv.AppendNumber(buf, float64(span))
	}
	return buf
}

// telSend ends the op and publishes it.
func (h *handler) telSend(buf []byte) {
	buf = append(buf, '}')
	h.b.Notice(abi.NoticeTelemetry, buf)
	h.keepPayload(buf)
}

// telString appends `,"key":"value"`.
func telString(buf []byte, key, value string) []byte {
	buf = append(buf, `,"`...)
	buf = append(buf, key...)
	buf = append(buf, `":`...)
	return jv.AppendString(buf, value)
}

// telNumber appends `,"key":value`.
func telNumber(buf []byte, key string, value float64) []byte {
	buf = append(buf, `,"`...)
	buf = append(buf, key...)
	buf = append(buf, `":`...)
	return jv.AppendNumber(buf, value)
}

// publishEvents records the entry and settlement events of a stored commit on the span of its Context (session.ts #publish: its
// telemetry events come first, then the commit's listeners).
//
//go:noinline
func (h *handler) publishEvents(pub *session.Publication) {
	for _, c := range pub.Changes {
		if c.Kind != session.ChangeTable {
			continue
		}
		switch c.Table {
		case session.WriteEntry:
			buf := telNumber(h.telOp("entry", pub.Span), "id", numOf(c.Record, "id"))
			h.telSend(telString(buf, "kind", strOf(c.Record, "kind")))
		case session.WriteSubmission:
			status := strOf(c.Record, "status")
			if status != "done" && status != "unanswered" {
				continue
			}
			buf := telString(telNumber(h.telOp("submission", pub.Span), "id", numOf(c.Record, "id")), "status", status)
			if c.Record.Has("answer") {
				buf = telNumber(buf, "answer", numOf(c.Record, "answer"))
			}
			h.telSend(buf)
		}
	}
}

// StartPhase asks the host for the span of a phase invocation (the `phaseSpan` hook, answered {span, carrier?}).
func (h *handler) StartPhase(p *session.PhaseInfo) {
	buf := append(h.payloadBuffer(256), `{"task":`...)
	buf = jv.AppendNumber(buf, float64(p.Task))
	buf = telString(buf, "name", p.Kind)
	buf = telString(buf, "phase", p.Phase)
	if p.Abort {
		buf = append(buf, `,"abort":true`...)
	}
	if p.Resumed {
		buf = append(buf, `,"resumed":true`...)
	}
	if p.HasAttempt {
		buf = telNumber(buf, "attempt", p.Attempt)
	}
	if p.Trace != nil {
		buf = append(buf, `,"trace":`...)
		buf = jv.Append(buf, p.Trace)
	}
	if p.Links != nil {
		buf = append(buf, `,"links":`...)
		buf = jv.Append(buf, p.Links)
	}
	buf = append(buf, '}')
	h.telPayload = buf
	h.hookEffect("phaseSpan", 0, 0, p.Task, &h.telPayload, nil, p.Answered)
}

// EndPhase ends a phase's span.
func (h *handler) EndPhase(span uint32, outcome session.PhaseOutcome, next, ended, message string) {
	buf := h.telOp("end", span)
	buf = append(buf, `,"outcome":"`...)
	switch outcome {
	case session.PhaseNext:
		buf = append(buf, `next"`...)
	case session.PhaseWaiting:
		buf = append(buf, `waiting"`...)
	case session.PhaseSleeping:
		buf = append(buf, `sleeping"`...)
	case session.PhaseDone:
		buf = append(buf, `done"`...)
	case session.PhaseFaulted:
		buf = append(buf, `faulted"`...)
	default:
		buf = append(buf, `aborted"`...)
	}
	if next != "" {
		buf = telString(buf, "next", next)
	}
	if ended != "" {
		buf = telString(buf, "ended", ended)
	}
	if message != "" {
		buf = telString(buf, "message", message)
	}
	h.telSend(buf)
}

// Checkpoint is the event of a commit that moved the running checkpoint.
func (h *handler) Checkpoint(span uint32, phase string) {
	h.telSend(telString(h.telOp("checkpoint", span), "phase", phase))
}

// ToolCall sets the called tool on the phase's span.
func (h *handler) ToolCall(span uint32, name, callID string) {
	h.telSend(telString(telString(h.telOp("tool", span), "name", name), "callId", callID))
}

// Replay is the event of a nested tool call answered from its stored result.
func (h *handler) Replay(span uint32, name string) {
	h.telSend(telString(h.telOp("replay", span), "tool", name))
}

// Active counts a task in or out of the active-task metric.
func (h *handler) Active(kind string, delta int) {
	h.telSend(telNumber(telString(h.telOp("active", 0), "task", kind), "delta", float64(delta)))
}

// withSpan names the span of the phase an effect runs under in its JSON payload (`span`, the host's handle); zero is none.
func withSpan(payload []byte, span uint32) []byte {
	if span == 0 {
		return payload
	}
	payload = append(payload[:len(payload)-1], `,"span":`...)
	return append(jv.AppendNumber(payload, float64(span)), '}')
}

// appendPublishedSpan names the span of the commit's Context in a published notice: where the host's commit listeners' reports go
// (session.ts #notifyHosts). Kept out of publish, which every commit runs.
//
//go:noinline
func appendPublishedSpan(item []byte, pub *session.Publication) []byte {
	if pub.Span == 0 {
		return item
	}
	item = append(item, `,"span":`...)
	return jv.AppendNumber(item, float64(pub.Span))
}
