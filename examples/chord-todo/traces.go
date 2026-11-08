package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The trace viewer's server half: the owner's JSON routes over the app's telemetry store, which the page's viewer at `/traces` reads
// (`client/traces.ts`). The routes open the store's file in the data directory read-only, read through the store's read interface only
// (`FindTraces`, filtered by who the spans were for, and `GetTrace`) and never follow a link to another trace. A page's spans come
// with the page's clock: the trace moves them onto the server's (`clamp`). Pi's app has them in a plugin (`plugins/traces`); a Go
// app's server code is a Go facet.

// recent is how many traces the list shows, newest first.
const recent = 40

// tracesFacet serves the viewer's routes for the store in dataDir.
func tracesFacet(dataDir string) host.Facet {
	forOwner := func(handler func(request chordhttp.HttpRequest, store *telemetrysqlite.SqliteTelemetryStore) (chordhttp.HttpResponse, error)) chordhttp.HttpRoute {
		return func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
			// Spans name who called what: only the owner reads them.
			if principal := ctx.Principal(); principal == nil || principal.Value("kind") != "owner" {
				return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
			}
			store, err := telemetrysqlite.OpenTelemetryStore(telemetryFile(dataDir))
			if err != nil {
				return chordhttp.HttpResponse{}, err
			}
			defer func() { _ = store.Close() }()
			return handler(request, store)
		}
	}
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			chordhttp.RouteFunc("GET /api/traces", forOwner(func(request chordhttp.HttpRequest, store *telemetrysqlite.SqliteTelemetryStore) (chordhttp.HttpResponse, error) {
				query := telemetry.TraceQuery{Limit: new(recent)}
				// `?principal=user:u1`: the traces with a span for that principal (`principalAttributes`).
				if principal, ok := request.Query().Get("principal"); ok && principal != "" {
					query.Attributes = map[string]telemetry.AttributeValue{"chord.principal": principal}
				}
				found, err := store.FindTraces(query)
				summaries := []any{}
				for _, trace := range found.Traces {
					summaries = append(summaries, summaryOf(trace))
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: summaries}, err
			})),
			chordhttp.RouteFunc("GET /api/traces/:id", forOwner(func(request chordhttp.HttpRequest, store *telemetrysqlite.SqliteTelemetryStore) (chordhttp.HttpResponse, error) {
				trace, err := store.GetTrace(request.Params()["id"])
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				if trace == nil {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 404}, nil
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: map[string]any{"id": trace.TraceID, "spans": rowsOf(clamp(trace.Spans))}}, nil
			})),
		}})
	})
}

// hostOf is the host that recorded span, as every span of Chord's names it (`chord.host`).
func hostOf(span telemetry.StoredSpan) (string, bool) {
	value, _ := span.Attributes.Get("chord.host")
	text, ok := value.(string)
	return text, ok
}

// clamp is spans with the page's moved onto the server's clock: a page's clock may be off by seconds, so its spans are moved by the
// least amount that puts every server child of a page span inside it, at least each child's end past its parent's, at most each
// child's start past its parent's (centred when no shift fits both: a child longer than its parent); none without such a pair.
func clamp(spans []telemetry.StoredSpan) []telemetry.StoredSpan {
	byID := map[string]telemetry.StoredSpan{}
	for _, span := range spans {
		byID[span.ID] = span
	}
	least, most, paired := 0.0, 0.0, false
	for _, child := range spans {
		if child.ParentID == nil {
			continue
		}
		parent, found := byID[*child.ParentID]
		parentHost, _ := hostOf(parent)
		childHost, _ := hostOf(child)
		if !found || parentHost != "browser" || childHost == "browser" {
			continue
		}
		low, high := child.EndedMs-parent.EndedMs, child.StartedMs-parent.StartedMs
		if !paired {
			least, most, paired = low, high, true
		} else {
			least, most = max(least, low), min(most, high)
		}
	}
	if !paired {
		return spans
	}
	skew := min(max(0, least), most)
	if least > most {
		skew = (least + most) / 2
	}
	if skew == 0 {
		return spans
	}
	moved := slices.Clone(spans)
	for i, span := range moved {
		if spanHost, _ := hostOf(span); spanHost == "browser" {
			moved[i].StartedMs += skew
			moved[i].EndedMs += skew
		}
	}
	return moved
}

// detail is the attributes of a trace's first span the list shows: an object's type and member, an HTTP route.
var detail = []string{"type", "member", "route"}

// summaryOf is a trace as the list shows it: its first span's name and details, whom it was for (a user's name, else the principal
// key of its first span that has one), its size, its hosts, its length, its failure.
func summaryOf(stored telemetry.StoredTrace) map[string]any {
	spans := clamp(stored.Spans)
	start, end := spans[0].StartedMs, spans[0].EndedMs
	hosts, failed := map[string]bool{}, false
	for _, span := range spans {
		start, end = min(start, span.StartedMs), max(end, span.EndedMs)
		if spanHost, ok := hostOf(span); ok {
			hosts[spanHost] = true
		}
		failed = failed || span.Status.Status == telemetry.SpanStatusCodeError
	}
	var parts []string
	for _, key := range detail {
		if value, ok := spans[0].Attributes.Get(key); ok {
			parts = append(parts, fmt.Sprint(value))
		}
	}
	who := ""
	for _, span := range spans {
		if key, ok := span.Attributes.Get("chord.principal"); ok {
			who = fmt.Sprint(key)
			if name, ok := span.Attributes.Get("user.name"); ok {
				who = fmt.Sprint(name)
			}
			break
		}
	}
	return map[string]any{
		"id": stored.TraceID, "name": spans[0].Name, "detail": strings.Join(parts, " "), "principal": who,
		"spans": len(spans), "hosts": len(hosts), "durationMs": end - start, "error": failed,
	}
}

// rowsOf is the trace's spans as the tree shows them, parents before their children (in start order), each with its depth, its host,
// its offset and length from the trace's start, and its attributes, events, links and error as text. A span whose parent is not in
// the trace (another trace's, or one not stored) is a root. A span naming no host (one an app started on an adapter itself) is shown
// on its parent's.
func rowsOf(spans []telemetry.StoredSpan) []any {
	start := spans[0].StartedMs
	ids := map[string]bool{}
	for _, span := range spans {
		start = min(start, span.StartedMs)
		ids[span.ID] = true
	}
	// The store gives a trace's spans in start order, so each parent's children stay in it.
	children := map[string][]telemetry.StoredSpan{}
	var roots []telemetry.StoredSpan
	for _, span := range spans {
		if span.ParentID != nil && ids[*span.ParentID] {
			children[*span.ParentID] = append(children[*span.ParentID], span)
		} else {
			roots = append(roots, span)
		}
	}
	rows := []any{}
	var visit func(span telemetry.StoredSpan, depth int, around string)
	visit = func(span telemetry.StoredSpan, depth int, around string) {
		spanHost, ok := hostOf(span)
		if !ok {
			spanHost = around
		}
		rows = append(rows, rowOf(span, depth, spanHost, start))
		for _, child := range children[span.ID] {
			visit(child, depth+1, spanHost)
		}
	}
	for _, root := range roots {
		visit(root, 0, "?")
	}
	return rows
}

// rowOf is one span of the tree (see rowsOf), recorded on spanHost.
func rowOf(span telemetry.StoredSpan, depth int, spanHost string, start float64) map[string]any {
	attributes, events, links := []any{}, []any{}, []any{}
	for _, pair := range span.Attributes {
		// Its host is shown as its tag.
		if pair.Name != "chord.host" {
			attributes = append(attributes, pairOf(pair))
		}
	}
	for _, event := range span.Events {
		parts := []string{event.Name}
		for _, pair := range event.Attributes {
			parts = append(parts, pairOf(pair))
		}
		events = append(events, strings.Join(parts, " "))
	}
	for _, link := range span.Links {
		// A reference to a span of another trace, shown and never followed.
		links = append(links, fmt.Sprintf("trace %s span %s", link.TraceID, link.SpanID))
	}
	var failure any
	if span.Status.Status == telemetry.SpanStatusCodeError {
		failure = "failed"
		if span.Status.Error != nil {
			failure = span.Status.Error.Message
		}
	}
	return map[string]any{
		"name": span.Name, "depth": depth, "host": spanHost, "offsetMs": span.StartedMs - start, "durationMs": span.EndedMs - span.StartedMs,
		"attributes": attributes, "events": events, "links": links, "error": failure,
	}
}

// pairOf is an attribute as `key=value`: the value as JSON, a fraction at two decimals (`duration=0.41`).
func pairOf(pair telemetry.SpanAttribute) string {
	if number, ok := pair.Value.(float64); ok && number != float64(int64(number)) {
		return fmt.Sprintf("%s=%.2f", pair.Name, number)
	}
	text, _ := json.Marshal(pair.Value)
	return pair.Name + "=" + string(text)
}
