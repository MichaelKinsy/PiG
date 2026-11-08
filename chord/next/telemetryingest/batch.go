package telemetryingest

// Ports packages/chord/src/telemetry-ingest/batch.ts (Pi main c23b9ec1). What pages and workers send the server's telemetry
// objects (decisions "Telemetry from browser pages", "The server is the one place telemetry is stored"): a batch of
// finished spans (a worker's with its metric aggregates), its schemas (a page's strict: every field bounded, ids printable
// ASCII, no control characters; a worker's, the server's own child, generous), and the two remote objects on the server
// that take them, PageTelemetry and WorkerTelemetry.
//
// Forced Go differences (D454): the schemas are the contracts package's schema nodes; the contracts are package variables
// made at initialisation (a malformed declaration is a programming error and panics there, as a package-level `remote(...)`
// throws at import).

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

const (
	// PageBatchSpans is at most this many spans in a page's batch.
	PageBatchSpans = 100
	// WorkerBatchRows is at most this many spans and metric rows in a worker's batch.
	WorkerBatchRows = 1000
)

// id is an id: printable ASCII, no spaces (a page's are `browser:<incarnation>:<n>`).
func id(max float64) contracts.Schema {
	return contracts.Str(contracts.Min(1), contracts.Max(max), contracts.Pattern(`^[\x21-\x7e]+$`))
}

// text is text: no control characters (C0, DEL, C1), at most max characters.
func text(max float64, min ...float64) contracts.Schema {
	options := []contracts.Option{contracts.Max(max), contracts.Pattern(`^[^\u0000-\u001f\u007f-\u009f]*$`)}
	if len(min) > 0 {
		options = append(options, contracts.Min(min[0]))
	} else {
		options = append(options, contracts.Min(0))
	}
	return contracts.Str(options...)
}

// attributes is a record of attributes: at most count keys of 1 to 64 characters of text (other keys refused), values of
// value.
func attributes(count float64, value contracts.Schema) contracts.Schema {
	return contracts.Schema{Node: delta.JsonObjectOf(
		"type", "object",
		"maxProperties", count,
		"patternProperties", delta.JsonObjectOf(`^[^\u0000-\u001f\u007f-\u009f]{1,64}$`, value.Node),
		"additionalProperties", false,
	)}
}

// spanLimits are the bounds of one span's fields.
type spanLimits struct {
	id, name, attributes float64
	value                contracts.Schema
	events, links        float64
}

// spanSchema is a finished span as StoredSpan's fields allow: ids, a name, times, attributes, events, links and a status.
func spanSchema(limits spanLimits) contracts.Schema {
	record := attributes(limits.attributes, limits.value)
	return contracts.Obj(
		contracts.Prop("id", id(limits.id)),
		contracts.Prop("traceId", id(limits.id)),
		contracts.Prop("parentId", contracts.Opt(id(limits.id))),
		contracts.Prop("name", text(limits.name, 1)),
		contracts.Prop("startedMs", contracts.Num(contracts.Min(0))),
		contracts.Prop("endedMs", contracts.Num(contracts.Min(0))),
		contracts.Prop("attributes", record),
		contracts.Prop("events", contracts.Arr(contracts.Obj(
			contracts.Prop("name", text(limits.name, 1)),
			contracts.Prop("timeMs", contracts.Num(contracts.Min(0))),
			contracts.Prop("attributes", record),
		), contracts.Max(limits.events))),
		contracts.Prop("links", contracts.Arr(contracts.Obj(
			contracts.Prop("traceId", id(limits.id)),
			contracts.Prop("spanId", id(limits.id)),
			contracts.Prop("attributes", contracts.Opt(record)),
		), contracts.Max(limits.links))),
		contracts.Prop("status", contracts.Union([]contracts.Schema{
			contracts.Obj(contracts.Prop("status", contracts.Lit("ok"))),
			contracts.Obj(
				contracts.Prop("status", contracts.Lit("error")),
				contracts.Prop("error", contracts.Opt(contracts.Obj(
					contracts.Prop("name", text(64)), contracts.Prop("message", text(500)),
				))),
			),
		})),
	)
}

// pageSpan is a page's spans: what a recorded click needs, little more (decision "Telemetry from browser pages").
var pageSpan = spanSchema(spanLimits{
	id: 128, name: 128, attributes: 32,
	value:  contracts.Union([]contracts.Schema{text(256), contracts.Num(), contracts.Bool()}),
	events: 16, links: 8,
})

// workerSpan is a worker's spans: bounded generously (its spans are Chord's and its plugins').
var workerSpan = spanSchema(spanLimits{
	id: 256, name: 1024, attributes: 256,
	value: contracts.Union([]contracts.Schema{
		contracts.Str(contracts.Max(65_536)), contracts.Num(), contracts.Bool(),
		contracts.Arr(contracts.Union([]contracts.Schema{contracts.Str(), contracts.Num()})),
	}),
	events: 1024, links: 256,
})

// metricRow is a worker's metric row: one series' aggregate of one 10 s interval.
var metricRow = contracts.Obj(
	contracts.Prop("intervalMs", contracts.Int(contracts.Min(0))),
	contracts.Prop("name", text(256, 1)),
	contracts.Prop("kind", contracts.Union([]contracts.Schema{
		contracts.Lit("counter"), contracts.Lit("upDownCounter"), contracts.Lit("histogram"),
	})),
	contracts.Prop("unit", contracts.Opt(text(64))),
	contracts.Prop("attributes", attributes(64, contracts.Union([]contracts.Schema{
		contracts.Str(contracts.Max(4096)), contracts.Num(), contracts.Bool(),
	}))),
	contracts.Prop("count", contracts.Int(contracts.Min(0))),
	contracts.Prop("sum", contracts.Num()),
	contracts.Prop("min", contracts.Num()),
	contracts.Prop("max", contracts.Num()),
)

// receipt is what `ingest` answers: the spans the store queued and the ones it dropped (refused, over the rate, over its
// bound).
var receipt = contracts.Obj(contracts.Prop("written", contracts.Int()), contracts.Prop("dropped", contracts.Int()))

// PageBatch is a page's batch (spans only: a page sends no metrics).
var PageBatch = contracts.Obj(contracts.Prop("spans", contracts.Arr(pageSpan, contracts.Max(PageBatchSpans))))

// WorkerBatch is a worker's batch: its spans and its metric rows.
var WorkerBatch = contracts.Obj(
	contracts.Prop("spans", contracts.Arr(workerSpan, contracts.Max(WorkerBatchRows))),
	contracts.Prop("metrics", contracts.Arr(metricRow, contracts.Max(WorkerBatchRows))),
)

// serverKind is the server's host kind as the app kit names it.
var serverKind = func() *contracts.HostKind {
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		panic(err)
	}
	return kind
}()

func serverAddress(*delta.JsonObject) (contracts.Address, error) { return serverKind.Address(nil), nil }

// PageTelemetry is the server's telemetry object for pages: any principal's page sends its batches to it, untrusted.
var PageTelemetry = mustRemote("chord.telemetry.page", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: serverAddress,
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{contracts.Meth("ingest", contracts.NewMethod(PageBatch, receipt))},
	})},
})

// WorkerTelemetry is the server's telemetry object for workers: `worker` for a worker's principal (its batches written as
// they are), `other` for everyone else (nothing to call).
var WorkerTelemetry = mustRemote("chord.telemetry.worker", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: serverAddress,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{contracts.Meth("ingest", contracts.NewMethod(WorkerBatch, receipt))},
		}),
		contracts.Var("other", contracts.VariantDeclaration{}),
	},
})

func mustRemote(name string, declaration contracts.RemoteDeclaration) *contracts.Contract {
	contract, err := contracts.Remote(name, declaration)
	if err != nil {
		panic(err)
	}
	return contract
}
