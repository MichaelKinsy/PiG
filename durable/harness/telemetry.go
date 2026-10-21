// Ports packages/durable/src/harness/telemetry.ts.

package harness

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// rootTelemetry is `options.telemetry ?? NOOP_TELEMETRY_CONTEXT`: the Harness's root adapter.
func rootTelemetry(adapter telemetry.TelemetryContext) telemetry.TelemetryContext {
	if adapter == nil {
		return telemetry.NoopTelemetryContext
	}
	return adapter
}

// metricsOf is `telemetry.metrics ?? NOOP_TELEMETRY_METRICS`.
func metricsOf(adapter telemetry.TelemetryContext) telemetry.TelemetryMetrics {
	if metrics := adapter.Metrics(); metrics != nil {
		return metrics
	}
	return telemetry.NoopTelemetryMetrics
}

// telemetryReport wraps the contained host callback with the report counter and, under ctx's span, the report event
// (harness.ts safeReport). Without a span the NOOP span drops the event; no root report span is created. Only the fixed
// fields are recorded, never the error.
func telemetryReport(onReport func(err error, info durable.ReportInfo), adapter telemetry.TelemetryContext) func(ctx context.Context, err error, info durable.ReportInfo) {
	reportMetrics := metricsOf(adapter)
	reports := telemetry.CreateTypedMetrics(reportMetrics, DurableTelemetrySchema).Counter("pi.durable.reports")
	metricsOn := reportMetrics != telemetry.NoopTelemetryMetrics
	return func(ctx context.Context, err error, info durable.ReportInfo) {
		onReport(err, info)
		span := chordctx.Telemetry(ctx)
		if !metricsOn && span == telemetry.NoopTelemetryContext {
			return
		}
		attributes := telemetry.SpanAttributes{telemetry.Attr("type", string(info.Type))}
		taskId := func() {
			if info.Task != nil {
				attributes = attributes.Set("task.id", float64(info.Task.Id))
			}
		}
		extension := func() {
			if info.Extension != "" {
				attributes = attributes.Set("extension", info.Extension)
			}
		}
		switch info.Type {
		case durable.ReportTaskFaulted, durable.ReportTaskOutdated:
			if info.Reason != "" {
				attributes = attributes.Set("reason", info.Reason)
			}
			extension()
			taskId()
		case durable.ReportTaskParked, durable.ReportMigrationFailed, durable.ReportTaskError, durable.ReportExtensionError:
			extension()
			taskId()
		case durable.ReportHostError:
			taskId()
		}
		if metricsOn {
			reports.Add(1, telemetry.SpanAttributes{telemetry.Attr("type", string(info.Type))})
		}
		span.AddEvent("pi.durable.report", attributes)
	}
}

func attribute(attributeType telemetry.TelemetryAttributeType, required bool, description string, values ...string) telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{
		TelemetryAttributeDefinition: telemetry.TelemetryAttributeDefinition{
			TelemetryAttributeMetadata: telemetry.TelemetryAttributeMetadata{Description: description},
			Type:                       attributeType,
			Values:                     values,
		},
		Required: required,
	}
}

func highCardinality(definition telemetry.TelemetryStartAttributeDefinition) telemetry.TelemetryStartAttributeDefinition {
	definition.Cardinality = telemetry.TelemetryAttributeCardinalityHigh
	return definition
}

func endAttribute(definition telemetry.TelemetryStartAttributeDefinition) telemetry.TelemetryAttributeDefinition {
	return definition.TelemetryAttributeDefinition
}

const (
	attrString  = telemetry.TelemetryAttributeTypeString
	attrNumber  = telemetry.TelemetryAttributeTypeNumber
	attrBoolean = telemetry.TelemetryAttributeTypeBoolean
)

var durableEntryEvent = telemetry.TelemetryEventDefinition{
	Description: "A transcript entry was committed; content remains in the transcript.",
	Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
		"entry.id":   highCardinality(attribute(attrNumber, true, "Transcript entry ID.")),
		"entry.kind": attribute(attrString, true, "Entry kind."),
	},
}

var durableReportAttributes = map[string]telemetry.TelemetryStartAttributeDefinition{
	"type": attribute(attrString, true, "ReportInfo type.",
		"session_failed", "task_faulted", "task_parked", "migration_failed", "task_outdated", "task_error",
		"extension_error", "host_error", "listener_error"),
	"reason":    attribute(attrString, false, "Fixed report reason, when present.", "threw", "restart_limit", "missing_task", "incompatible_task"),
	"extension": attribute(attrString, false, "Registered extension name, when present."),
	"task.id":   highCardinality(attribute(attrNumber, false, "Task ID, when present.")),
}

var durableReportEvent = telemetry.TelemetryEventDefinition{
	Description: "A report was raised; no error payload is recorded.",
	Attributes:  durableReportAttributes,
}

var durableSubmissionEvent = telemetry.TelemetryEventDefinition{
	Description: "A submission was settled.",
	Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
		"submission.id":   highCardinality(attribute(attrNumber, true, "Submission ID.")),
		"status":          attribute(attrString, true, "Settlement.", "done", "unanswered"),
		"answer.entry_id": highCardinality(attribute(attrNumber, false, "Answer entry ID.")),
	},
}

// DurableTelemetrySchema is DURABLE_TELEMETRY_SCHEMA: Durable's content-free span, event, and metric vocabulary. IDs
// address durable records, never content.
var DurableTelemetrySchema = telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
	Version: 1,
	Spans: map[string]telemetry.TelemetrySpanDefinition{
		"pi.durable.submit": {
			Description: "Public submission admission and its commit. Inputs start root traces linked to their callers; writes are children. Internal admissions emit phase events only.",
			Parents:     telemetry.TelemetryParentDefinition{Kind: telemetry.TelemetryParentKindAny},
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"submission.type": attribute(attrString, true, "Submission type.", "input", "write"),
				"conversation.id": highCardinality(attribute(attrNumber, true, "Conversation ID.")),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"submission.id": endAttribute(highCardinality(attribute(attrNumber, false, "Admitted submission ID."))),
				"result":        endAttribute(attribute(attrString, false, "Admission result; stale writes report done.", "queued", "placed", "done")),
			},
			Events: map[string]telemetry.TelemetryEventDefinition{
				"pi.durable.entry":      durableEntryEvent,
				"pi.durable.submission": durableSubmissionEvent,
				"pi.durable.report":     durableReportEvent,
			},
			Status: telemetry.TelemetrySpanStatusDefinition{Default: telemetry.SpanStatusCodeOK, ErrorWhen: "Admission failed."},
		},
		"pi.durable.phase": {
			Description: "One task phase invocation, including its decision commit, never a wait or restart.",
			Parents:     telemetry.TelemetryParentDefinition{Kind: telemetry.TelemetryParentKindAny},
			StartAttributes: map[string]telemetry.TelemetryStartAttributeDefinition{
				"task.id":   highCardinality(attribute(attrNumber, true, "Task ID.")),
				"task.name": attribute(attrString, true, "Registered task name."),
				"phase":     attribute(attrString, true, "Invoked checkpoint phase."),
				"kind":      attribute(attrString, true, "Invocation kind.", "run", "abort"),
				"attempt":   attribute(attrNumber, false, "Checkpoint attempt, when present."),
				"resumed":   attribute(attrBoolean, true, "First invocation of a task loaded in this process."),
			},
			EndAttributes: map[string]telemetry.TelemetryAttributeDefinition{
				"outcome":      endAttribute(attribute(attrString, false, "next=<phase>, waiting, sleeping, done, faulted, or aborted.")),
				"tool.name":    endAttribute(attribute(attrString, false, "Called tool name.")),
				"tool.call_id": endAttribute(highCardinality(attribute(attrString, false, "Transcript tool call ID."))),
			},
			Events: map[string]telemetry.TelemetryEventDefinition{
				"pi.durable.entry":      durableEntryEvent,
				"pi.durable.submission": durableSubmissionEvent,
				"pi.durable.report":     durableReportEvent,
				"pi.durable.checkpoint": {
					Description: "The running checkpoint's phase changed while the invocation continued.",
					Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
						"phase": attribute(attrString, true, "Committed checkpoint phase."),
					},
				},
				"pi.durable.replay": {
					Description: "A nested tool call was answered from its stored result.",
					Attributes: map[string]telemetry.TelemetryEventAttributeDefinition{
						"tool.name": attribute(attrString, true, "Called tool name."),
					},
				},
			},
			Status: telemetry.TelemetrySpanStatusDefinition{
				Default:   telemetry.SpanStatusCodeOK,
				ErrorWhen: "Faulted, failed, and orphaned outcomes use task_faulted, task_failed, and task_orphaned with a diagnostic capped at 500 characters. Aborted outcomes keep status ok.",
			},
		},
	},
	Metrics: map[string]telemetry.TelemetryMetricDefinition{
		"pi.durable.reports": {
			Kind:        telemetry.TelemetryMetricKindCounter,
			Description: "Reports raised, grouped by their fixed ReportInfo type.",
			Attributes:  map[string]telemetry.TelemetryMetricAttributeDefinition{"type": durableReportAttributes["type"]},
		},
		"pi.durable.phase.duration": {
			Kind:        telemetry.TelemetryMetricKindHistogram,
			Unit:        "ms",
			Description: "Duration of a phase invocation and its decision commit.",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"task":    attribute(attrString, true, "Registered task name."),
				"phase":   attribute(attrString, true, "Invoked checkpoint phase."),
				"outcome": attribute(attrString, true, "Phase outcome."),
			},
		},
		"pi.durable.tasks.active": {
			Kind:        telemetry.TelemetryMetricKindUpDownCounter,
			Description: "Live tasks held by this Harness; decremented at terminal settlement or close.",
			Attributes: map[string]telemetry.TelemetryMetricAttributeDefinition{
				"task": attribute(attrString, true, "Registered task name."),
			},
		},
	},
})
