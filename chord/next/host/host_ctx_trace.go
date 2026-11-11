package host

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports the `ctx.trace` of packages/chord/src/host/ctx.ts: a child span of the handler's and a child ctx under it.

// traceable is a ctx that makes its child under a span: the same kind of ctx (a job's has its id and state), and its
// host's `chord.host`.
type traceable interface {
	Ctx
	spanHost() string
	traced(span telemetry.TelemetrySpan) Ctx
}

// traceUnder is `ctx.trace` of ctx: fn in a child span with its child ctx; with nothing recorded, fn with ctx itself.
// Either way a panic of fn is its error (recording changes nothing fn sees but its span).
func traceUnder(ctx traceable, name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error) {
	parent := chordctx.Telemetry(ctx)
	if chordtelemetry.Untraced(parent) {
		return tryPlugin(func() (any, error) { return fn(ctx) })
	}
	all := make(telemetry.SpanAttributes, 0, len(attributes)+1)
	all = append(all, telemetry.Attr("chord.host", ctx.spanHost()))
	all = append(all, attributes...)
	var result any
	err := parent.StartSpan(telemetry.SpanOptions{Name: name, Attributes: all}, func(span telemetry.TelemetrySpan) error {
		value, err := tryPlugin(func() (any, error) { return fn(ctx.traced(span)) })
		if err != nil {
			chordtelemetry.Ended(span, "trace_failed", err, ctx)
		}
		result = value
		return err
	})
	return result, err
}

// Trace implements Ctx.
func (c *callContext) Trace(name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error) {
	return traceUnder(c, name, attributes, fn)
}

func (c *callContext) spanHost() string { return c.host.spanHost }

func (c *callContext) traced(span telemetry.TelemetrySpan) Ctx { return newSpanCtx(c, span) }

// spanCtx is a ctx under a child span (`ctx.trace`): its parent's caller, signal and references, its telemetry the span.
type spanCtx struct {
	context.Context
	parent traceable
}

var _ Ctx = (*spanCtx)(nil)

func newSpanCtx(parent traceable, span telemetry.TelemetrySpan) *spanCtx {
	return &spanCtx{Context: chordctx.WithTelemetry(parent, span), parent: parent}
}

func (s *spanCtx) String() string { return fmt.Sprint(s.parent) + ".Trace" }

// Principal implements Ctx.
func (s *spanCtx) Principal() data.Principal { return s.parent.Principal() }

// Incarnation implements Ctx.
func (s *spanCtx) Incarnation() string { return s.parent.Incarnation() }

// Get implements Ctx: the parent's, under the parent itself (its references).
func (s *spanCtx) Get(typ *contracts.Contract, params any, options GetOptions) (*Reference, error) {
	return s.parent.Get(typ, params, options)
}

// Trace implements Ctx.
func (s *spanCtx) Trace(name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error) {
	return traceUnder(s, name, attributes, fn)
}

func (s *spanCtx) spanHost() string { return s.parent.spanHost() }

func (s *spanCtx) traced(span telemetry.TelemetrySpan) Ctx { return newSpanCtx(s, span) }

// jobSpanCtx is a job run's ctx under a child span: of its own kind, with the run's id and state.
type jobSpanCtx struct {
	*spanCtx
	job JobCtx
}

var _ JobCtx = (*jobSpanCtx)(nil)

// JobID implements JobCtx.
func (s *jobSpanCtx) JobID() string { return s.job.JobID() }

// State implements JobCtx.
func (s *jobSpanCtx) State() RunState { return s.job.State() }

func (s *jobSpanCtx) traced(span telemetry.TelemetrySpan) Ctx {
	return &jobSpanCtx{spanCtx: newSpanCtx(s, span), job: s.job}
}

// Trace implements Ctx.
func (s *jobSpanCtx) Trace(name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error) {
	return traceUnder(s, name, attributes, fn)
}

func (ctx *jobContext) traced(span telemetry.TelemetrySpan) Ctx {
	return &jobSpanCtx{spanCtx: newSpanCtx(ctx, span), job: ctx}
}

// Trace implements Ctx.
func (ctx *jobContext) Trace(name string, attributes telemetry.SpanAttributes, fn func(ctx Ctx) (any, error)) (any, error) {
	return traceUnder(ctx, name, attributes, fn)
}
