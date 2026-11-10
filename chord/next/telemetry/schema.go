package telemetry

import (
	pitelemetry "github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/telemetry.ts (Pi main c23b9ec1), the schema part: every span, event and metric Chord records,
// with their attributes. Ids (objects, holds, calls, address keys) are span attributes only.
//
// Forced Go difference (language mechanics): upstream's typed spans (`ChordSpan<N>`, `SchemaTelemetrySpan`) and typed metrics generics
// have no Go form; a span is a pitelemetry.TelemetrySpan and an attribute is a pitelemetry.Attr. The schema is the same data.

type (
	attribute = pitelemetry.TelemetryStartAttributeDefinition
)

func str(description string, required bool, high bool, values ...string) attribute {
	definition := pitelemetry.TelemetryAttributeDefinition{
		Description: description,
		Type:        pitelemetry.TelemetryAttributeTypeString,
		Values:      values,
	}
	if high {
		definition.Cardinality = pitelemetry.TelemetryAttributeCardinalityHigh
	}
	return attribute{TelemetryAttributeDefinition: definition, Required: required}
}

func num(description string, required bool) attribute {
	return attribute{
		Description: description,
		Type:        pitelemetry.TelemetryAttributeTypeNumber,
		Required:    required,
	}
}

func end(a attribute) pitelemetry.TelemetryAttributeDefinition { return a.TelemetryAttributeDefinition }

func outcome(values ...string) pitelemetry.TelemetryAttributeDefinition {
	return end(str("How it ended; a cancel is no error (status ok).", false, false, values...))
}

var (
	typeAttribute      = str("The object type's name.", true, false)
	hostAttributeType  = str("The host that recorded the span: its address key (`list|groceries`), `client` without an address.", true, true)
	principalAttribute = str("Whom the call was for: its principal key, unless the host's `principalAttributes` hook records other attributes.", false, true)
	objectAttribute    = str("The object: its type, and its params key when it has params.", true, true)
	memberAttribute    = str("The method's name.", true, false)
)

func attributes(entries ...any) map[string]attribute {
	result := make(map[string]attribute, len(entries))
	for i := 0; i < len(entries); i += 2 {
		result[entries[i].(string)] = entries[i+1].(attribute)
	}
	return result
}

func endAttributes(entries ...any) map[string]pitelemetry.TelemetryAttributeDefinition {
	result := make(map[string]pitelemetry.TelemetryAttributeDefinition, len(entries))
	for i := 0; i < len(entries); i += 2 {
		result[entries[i].(string)] = entries[i+1].(pitelemetry.TelemetryAttributeDefinition)
	}
	return result
}

var commitEvent = pitelemetry.TelemetryEventDefinition{
	Description: "A commit of the span's object (documents and tables) inside the span's work.",
	Attributes: attributes(
		"documents", num("Documents the commit adopted.", true),
		"ops", num("Ops of the documents' prepared changes.", true),
		"rows", num("Table rows its writes changed: a `put` one, an update or delete the rows its statement changed.", true),
		"duration", num("Milliseconds from the commit's start to its adoption.", true),
	),
}

var retryEvent = pitelemetry.TelemetryEventDefinition{
	Description: "A transient failure: the next attempt waits for its backoff.",
	Attributes:  attributes("backoff", num("Milliseconds until the next attempt.", true)),
}

var parkEvent = pitelemetry.TelemetryEventDefinition{
	Description: "The attempt parked on a dependency that is not live.",
	Attributes:  attributes("cause", str("`loading`, or the waiting cause's message.", true, false)),
}

var fateEvent = pitelemetry.TelemetryEventDefinition{
	Description: "One object's fate in a reload.",
	Attributes: attributes(
		"object", objectAttribute,
		"fate", str("What the object did.", true, false, "restarted", "ended", "parked", "retried"),
	),
}

// errorsDescription is the failed-spans text: `status: "error"` with one of these categories and the error's message.
const errorsDescription = "The error's category (`call_failed`, `method_failed`, `relay_failed`, `hold_failed`, `activation_failed`, " +
	"`open_failed`, `close_failed`, `load_failed`, `reload_failed`, `resolve_failed`, `routine_failed`, `job_failed`, " +
	"`http_failed`, `trace_failed` for `ctx.trace`) and its message, " +
	"at most 500 characters; a cancel keeps status ok."

var spanStatus = pitelemetry.TelemetrySpanStatusDefinition{Default: pitelemetry.SpanStatusCodeOK, ErrorWhen: errorsDescription}

var anyParent = pitelemetry.TelemetryParentDefinition{Kind: pitelemetry.TelemetryParentKindAny}
var rootParent = pitelemetry.TelemetryParentDefinition{Kind: pitelemetry.TelemetryParentKindRootOrExternal}

func counter(description string) pitelemetry.TelemetryMetricDefinition {
	return pitelemetry.TelemetryMetricDefinition{
		Kind: pitelemetry.TelemetryMetricKindCounter, Description: description,
		Attributes: map[string]pitelemetry.TelemetryMetricAttributeDefinition{},
	}
}

func upDown(description string) pitelemetry.TelemetryMetricDefinition {
	definition := counter(description)
	definition.Kind = pitelemetry.TelemetryMetricKindUpDownCounter
	return definition
}

func histogram(description string) pitelemetry.TelemetryMetricDefinition {
	definition := counter(description)
	definition.Kind = pitelemetry.TelemetryMetricKindHistogram
	definition.Unit = "ms"
	return definition
}

// Schema is Chord's span, event and metric vocabulary (§29), upstream's CHORD_TELEMETRY_SCHEMA.
var Schema = pitelemetry.DefineTelemetrySchema(&pitelemetry.TelemetrySchemaDefinition{
	Version: 1,
	Spans: map[string]pitelemetry.TelemetrySpanDefinition{
		"chord.call": {
			Description: "A consumer's call of a remote object's method, from the call until its reply.",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute, "member", memberAttribute, "object", objectAttribute,
				"address.kind", str("The kind of the provider's host.", true, false),
				"chord.principal", principalAttribute,
			),
			EndAttributes: endAttributes("outcome", outcome("ok", "error", "aborted")),
			Status:        spanStatus,
		},
		"chord.method": {
			Description: "A provider running a method: under the call's carrier (over a link) or the caller's span (same host).",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute, "member", memberAttribute, "object", objectAttribute,
				"principal.kind", str("The ctx's principal kind, or `none`.", true, false),
				"chord.principal", principalAttribute,
			),
			EndAttributes: endAttributes("outcome", outcome("ok", "error", "aborted")),
			Events:        map[string]pitelemetry.TelemetryEventDefinition{"chord.commit": commitEvent},
			Status:        spanStatus,
		},
		"chord.relay": {
			Description: "A relay forwarding a hold (until it is held upstream) or a call (until its reply came back), under the arriving carrier.",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute,
				"member", str("`hold`, or a call's method as `#<index>` (a relay never knows names).", true, false),
				"upstream.kind", str("The kind of the upstream host.", true, false),
			),
			EndAttributes: endAttributes("outcome", outcome("ok", "error", "aborted")),
			Status:        spanStatus,
		},
		"chord.hold": {
			Description: "A consumer's hold of a remote object, until its first `bound` or failure.",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute, "object", objectAttribute,
				"address.kind", str("The kind of the provider's host.", true, false),
			),
			EndAttributes: endAttributes("outcome", outcome("bound", "failed", "ended")),
			Status:        spanStatus,
		},
		"chord.activate": {
			Description: "One activation attempt until it is live, failed, parked or ended (a parked one resumes in a new span); linked to its object's first trigger.",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute, "object", objectAttribute,
				"attempt", num("The object's attempt, from 1.", true),
			),
			EndAttributes: endAttributes("outcome", outcome("live", "failed", "parked", "ended")),
			Events:        map[string]pitelemetry.TelemetryEventDefinition{"chord.park": parkEvent, "chord.retry": retryEvent},
			Status:        spanStatus,
		},
		"chord.open": {
			Description: "One native's open, under its attempt's span.",
			Parents:     pitelemetry.TelemetryParentDefinition{Kind: pitelemetry.TelemetryParentKindSpans, Spans: []string{"chord.activate"}},
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute,
				"native", num("The native's position, from 1.", true),
			),
			EndAttributes: endAttributes(),
			Status:        spanStatus,
		},
		"chord.close": {
			Description: "One native's close (a root: it runs at its attempt's teardown).",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute,
				"native", num("The native's position, from 1.", true),
				"reason", str("Why.", true, false, "end", "close", "reload"),
			),
			EndAttributes: endAttributes(),
			Status:        spanStatus,
		},
		"chord.load": {
			Description: "A host's `load`: the setups, the publication and the start barrier.",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "plugins", num("Plugins given.", true),
			),
			EndAttributes: endAttributes("failed", end(num("Plugins left out.", false))),
			Status:        spanStatus,
		},
		"chord.reload": {
			Description: "A host's `reload`: staging, the commit and every object's fate.",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "plugins", num("Plugins given.", true),
			),
			EndAttributes: endAttributes(
				"added", end(num("Plugins added.", false)),
				"changed", end(num("Plugins changed.", false)),
				"removed", end(num("Plugins removed.", false)),
				"failed", end(num("Plugins whose new code failed.", false)),
				"fates", end(num("Objects that took a fate.", false)),
			),
			Events: map[string]pitelemetry.TelemetryEventDefinition{"chord.reload.fate": fateEvent},
			Status: spanStatus,
		},
		"chord.resolve": {
			Description: "A route resolving an address (a worker start included), linked to its first trigger.",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType,
				"kind", str("The host kind.", true, false),
				"address", str("The address key.", true, true),
			),
			EndAttributes: endAttributes("outcome", outcome("linked", "failed")),
			Status:        spanStatus,
		},
		"chord.revoke": {
			Description:     "A revocation on this host.",
			Parents:         anyParent,
			StartAttributes: attributes("chord.host", hostAttributeType),
			EndAttributes:   endAttributes("count", end(num("Objects it ended and child links it closed.", false))),
			Status:          spanStatus,
		},
		"chord.routine": {
			Description: "One routine call (`self.on`, `self.onLive`), a root linked to the span of the commit that changed its source (when known); a throw's backoff a `chord.retry` event.",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType, "type", typeAttribute, "object", objectAttribute,
				"routine", str("Its kind.", true, false, "on", "onLive"),
				"position", num("Its position among its kind, from 1.", true),
			),
			EndAttributes: endAttributes("outcome", outcome("ok", "error", "aborted")),
			Events:        map[string]pitelemetry.TelemetryEventDefinition{"chord.commit": commitEvent, "chord.retry": retryEvent},
			Status:        spanStatus,
		},
		"chord.job": {
			Description: "One phase of a job run: the intent (the start's transaction), the run, the finish; under the stored carrier (persisted) or the starter's span.",
			Parents:     anyParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType,
				"job", str("The job's name.", true, false),
				"phase", str("The phase.", true, false, "intent", "run", "finish"),
				"object", objectAttribute,
				"job.id", str("The run's id.", false, true),
			),
			EndAttributes: endAttributes(
				"outcome", outcome("ok", "error", "aborted"),
				"job.id", end(str("The run's id, once the intent made it.", false, true)),
			),
			Events: map[string]pitelemetry.TelemetryEventDefinition{"chord.commit": commitEvent, "chord.retry": retryEvent},
			Status: spanStatus,
		},
		"chord.http": {
			Description: "An HTTP request routed to a contributed route (`chord/http`); its path is never recorded.",
			Parents:     rootParent,
			StartAttributes: attributes(
				"chord.host", hostAttributeType,
				"method", str("The request's method.", true, false),
				"route", str("The route's pattern.", true, false),
			),
			EndAttributes: endAttributes("status", end(num("The response's status.", false))),
			Events:        map[string]pitelemetry.TelemetryEventDefinition{"chord.commit": commitEvent},
			Status:        spanStatus,
		},
	},
	Metrics: map[string]pitelemetry.TelemetryMetricDefinition{
		"chord.data.compilations":   counter("Schemas compiled."),
		"chord.data.validations":    counter("Values validated against a schema."),
		"chord.data.walks":          counter("Full-value walks."),
		"chord.link.checks":         counter("Messages checked against the message schema."),
		"chord.link.deflates":       counter("Parts deflated."),
		"chord.link.inflates":       counter("Parts inflated."),
		"chord.link.encodes":        counter("Member values and ops encoded for a hold."),
		"chord.store.loads":         counter("Persisted documents loaded."),
		"chord.store.commits":       counter("Store commits."),
		"chord.store.statements":    counter("SQL statements of store commits."),
		"chord.query.runs":          counter("Live query runs."),
		"chord.cycle.visits":        counter("Objects a cycle check visited."),
		"chord.derive.computes":     counter("Derive computations."),
		"chord.derive.diffs":        counter("Derive diffs."),
		"chord.registry.composes":   counter("Registry compositions."),
		"chord.registry.adapts":     counter("Registry values mapped for an older reader."),
		"chord.contract.adapts":     counter("State mapped for an older consumer."),
		"chord.link.state_messages": counter("`state` messages this host sent of its own."),
		"chord.link.bytes": {
			Kind: pitelemetry.TelemetryMetricKindCounter, Unit: "By",
			Description: "Bytes of encoded messages on this host's links (WebSocket frames, IPC messages).",
			Attributes: map[string]pitelemetry.TelemetryMetricAttributeDefinition{
				"direction": str("Which way.", true, false, "sent", "received"),
			},
		},
		"chord.commit.duration":   histogram("Commits of an object's documents and tables, from their start to their adoption."),
		"chord.calls.in_flight":   upDown("Method calls running on this host."),
		"chord.holds.active":      upDown("Holds arriving over links and placed on this host."),
		"chord.links.open":        upDown("Open links of this host."),
		"chord.park.duration":     histogram("How long an activation attempt stayed parked on its dependencies."),
		"chord.backpressure.wait": histogram("How long a link origin's state waited for a slot of its window."),
	},
})
