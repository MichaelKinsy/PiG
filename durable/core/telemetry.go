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

// telHost is the session.Telemetry of a Harness opened with telemetry: its scheduler's phase spans and metrics, as notices.
type telHost struct{ h *handler }

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
func (t *telHost) StartPhase(p *session.PhaseInfo) {
	body := jv.NewObject()
	body.Set("task", float64(p.Task))
	body.Set("name", p.Kind)
	body.Set("phase", p.Phase)
	body.Set("abort", p.Abort)
	body.Set("resumed", p.Resumed)
	if p.HasAttempt {
		body.Set("attempt", p.Attempt)
	}
	if p.Trace != nil {
		body.Set("trace", p.Trace)
	}
	if p.Links != nil {
		body.Set("links", p.Links)
	}
	t.h.hookEffect("phaseSpan", 0, 0, p.Task, body, nil, func(v any, err error) {
		var span uint32
		var carrier any
		if o, ok := v.(*jv.Object); ok && err == nil {
			span = uint32(numOf(o, "span"))
			carrier, _ = o.Get("carrier")
		}
		p.Started(span, carrier)
	})
}

// EndPhase ends a phase's span.
func (t *telHost) EndPhase(span uint32, outcome session.PhaseOutcome, next, ended, message string) {
	buf := t.h.telOp("end", span)
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
	t.h.telSend(buf)
}

// Checkpoint is the event of a commit that moved the running checkpoint.
func (t *telHost) Checkpoint(span uint32, phase string) {
	t.h.telSend(telString(t.h.telOp("checkpoint", span), "phase", phase))
}

// ToolCall sets the called tool on the phase's span.
func (t *telHost) ToolCall(span uint32, name, callID string) {
	t.h.telSend(telString(telString(t.h.telOp("tool", span), "name", name), "callId", callID))
}

// Replay is the event of a nested tool call answered from its stored result.
func (t *telHost) Replay(span uint32, name string) {
	t.h.telSend(telString(t.h.telOp("replay", span), "tool", name))
}

// Active counts a task in or out of the active-task metric.
func (t *telHost) Active(kind string, delta int) {
	t.h.telSend(telNumber(telString(t.h.telOp("active", 0), "task", kind), "delta", float64(delta)))
}
