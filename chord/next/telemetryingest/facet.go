package telemetryingest

// Ports packages/chord/src/telemetry-ingest/facet.ts (Pi main c23b9ec1). The server's telemetry ingest (decisions "Telemetry
// from browser pages", "The server is the one place telemetry is stored", "Who a span was for"): a facet serving
// PageTelemetry and WorkerTelemetry, whose `ingest(batch)` writes what pages and workers recorded to the server's store. A
// page is untrusted: its spans must carry its own ids (`browser:<its incarnation>:...`), are capped per page and minute,
// marked `chord.host: browser` and stamped with the principal of the link they came over; a worker's are written as they
// are, stamped with its host. A page's own `chord.*` attributes are dropped (15a51145): what Chord records about a span,
// the server stamps itself.

import (
	"context"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// IngestTarget is where ingested batches go: the server's store (`SqliteTelemetry`), which queues them; Ingest returns
// how many spans it queued.
type IngestTarget interface {
	Ingest(batch TelemetryBatch) int
}

// IngestOptions is what the ingest facet takes.
type IngestOptions struct {
	Target IngestTarget
	// WorkerHost is the address key of the worker whose link stamps principal; ok is false for any other principal.
	WorkerHost func(principal data.Principal) (key string, ok bool)
	// PrincipalAttributes are the attributes a page's spans record for the principal of their link (the server host's
	// `principalAttributes`).
	PrincipalAttributes func(principal data.Principal) telemetry.SpanAttributes
}

// PageSpansPerMinute is how many spans one page may send per minute; past it they are dropped (and counted in its
// receipts).
const PageSpansPerMinute = 1200

// pages is how many pages' minute counts one principal's object keeps (the earliest goes first).
const pages = 1000

const minuteMs = 60_000

// IngestFacet is the ingest facet: PageTelemetry and WorkerTelemetry on the server, writing to options.Target. Their
// calls record no span of their own (a span per batch would be telemetry about telemetry, written back with the next
// batch).
func IngestFacet(options IngestOptions) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(PageTelemetry, func(_ *delta.JsonObject, principal data.Principal, _ host.Self) (contracts.Implementation, error) {
			// Per span, as for every span Chord records with a principal: caching is the hook's own.
			who := func() telemetry.SpanAttributes { return options.PrincipalAttributes(principal) }
			budgets := &pageBudgets{budgets: map[string]*budget{}}
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"ingest": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						incarnation := ctx.(host.Ctx).Incarnation()
						spans := args.(*delta.JsonObject).Value("spans").([]any)
						accepted := pageSpans(spans, "browser:"+incarnation+":", who, budgets.of(incarnation, jsclock.DateNow()))
						written := options.Target.Ingest(TelemetryBatch{Spans: accepted})
						return receiptOf(written, len(spans)-written), nil
					},
				},
			}}, nil
		}, host.ServeOptions{Untraced: true}); err != nil {
			return err
		}
		return h.Serve(WorkerTelemetry, func(_ *delta.JsonObject, principal data.Principal, _ host.Self) (contracts.Implementation, error) {
			worker, ok := options.WorkerHost(principal)
			if !ok {
				return contracts.Implementation{Variant: "other"}, nil
			}
			return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"ingest": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						batch := args.(*delta.JsonObject)
						spans := decodeSpans(batch.Value("spans").([]any))
						for i := range spans {
							spans[i].Attributes = spans[i].Attributes.Set("chord.host", worker)
						}
						rows := batch.Value("metrics").([]any)
						written := options.Target.Ingest(TelemetryBatch{Spans: spans, Metrics: decodeMetrics(rows)})
						return receiptOf(written, len(spans)-written), nil
					},
				},
			}}, nil
		}, host.ServeOptions{Untraced: true})
	})
}

func receiptOf(written, dropped int) delta.JsonValue {
	return delta.JsonObjectOf("written", float64(written), "dropped", float64(dropped))
}

// budget is one page's spans of the current minute.
type budget struct {
	start float64
	spans int
}

// pageBudgets are the minute budgets of one principal's pages, by incarnation, bounded (the earliest page goes first).
type pageBudgets struct {
	mu      sync.Mutex
	budgets map[string]*budget
	order   []string
}

// of is the budget of the page incarnation at now: a new minute starts a new one.
func (p *pageBudgets) of(incarnation string, now float64) *budget {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.budgets[incarnation]
	if current != nil && now-current.start < minuteMs {
		return current
	}
	if current != nil {
		delete(p.budgets, incarnation)
		for i, key := range p.order {
			if key == incarnation {
				p.order = append(p.order[:i], p.order[i+1:]...)
				break
			}
		}
	}
	if len(p.budgets) >= pages {
		delete(p.budgets, p.order[0])
		p.order = p.order[1:]
	}
	current = &budget{start: now}
	p.budgets[incarnation] = current
	p.order = append(p.order, incarnation)
	return current
}

// pageSpans are the spans of a page's batch the store takes: its own (ids, trace and parent under prefix, so it never
// writes into another sender's trace or poses as a server span), ending after they start, within its minute's budget; each
// marked `chord.host: browser` and stamped with who() (the link's principal's attributes), which no attribute of the page's
// may override: the page's own `chord.*` attributes are dropped (a claimed `chord.principal` too, when the hook records
// none).
func pageSpans(rows []any, prefix string, who func() telemetry.SpanAttributes, budget *budget) []telemetry.StoredSpan {
	var accepted []telemetry.StoredSpan
	for _, span := range decodeSpans(rows) {
		if budget.spans >= PageSpansPerMinute {
			break
		}
		own := strings.HasPrefix(span.ID, prefix) && strings.HasPrefix(span.TraceID, prefix) &&
			(span.ParentID == nil || strings.HasPrefix(*span.ParentID, prefix))
		if !own || span.EndedMs < span.StartedMs {
			continue
		}
		budget.spans++
		stamped := telemetry.SpanAttributes{telemetry.Attr("chord.host", "browser")}
		for _, attribute := range span.Attributes {
			if !strings.HasPrefix(attribute.Name, "chord.") {
				stamped = stamped.Set(attribute.Name, attribute.Value)
			}
		}
		for _, attribute := range who() {
			stamped = stamped.Set(attribute.Name, attribute.Value)
		}
		span.Attributes = stamped
		accepted = append(accepted, span)
	}
	return accepted
}
