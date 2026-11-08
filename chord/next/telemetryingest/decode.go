package telemetryingest

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The batches' JSON values (checked by their schemas already) as the stored spans and metric rows a recorder takes.

func decodeSpans(rows []any) []telemetry.StoredSpan {
	spans := make([]telemetry.StoredSpan, len(rows))
	for i, row := range rows {
		spans[i] = decodeSpan(row.(*delta.JsonObject))
	}
	return spans
}

func decodeSpan(row *delta.JsonObject) telemetry.StoredSpan {
	span := telemetry.StoredSpan{
		ID: row.Value("id").(string), TraceID: row.Value("traceId").(string), Name: row.Value("name").(string),
		StartedMs: row.Value("startedMs").(float64), EndedMs: row.Value("endedMs").(float64),
		Attributes: decodeAttributes(row.Value("attributes").(*delta.JsonObject)),
		Events:     []telemetry.StoredSpanEvent{}, Links: []telemetry.StoredSpanLink{},
	}
	if parent, ok := row.Get("parentId"); ok {
		span.ParentID = new(parent.(string))
	}
	for _, item := range row.Value("events").([]any) {
		event := item.(*delta.JsonObject)
		span.Events = append(span.Events, telemetry.StoredSpanEvent{
			Name: event.Value("name").(string), TimeMs: event.Value("timeMs").(float64),
			Attributes: decodeAttributes(event.Value("attributes").(*delta.JsonObject)),
		})
	}
	for _, item := range row.Value("links").([]any) {
		link := item.(*delta.JsonObject)
		stored := telemetry.StoredSpanLink{TraceID: link.Value("traceId").(string), SpanID: link.Value("spanId").(string)}
		if attributes, ok := link.Get("attributes"); ok {
			stored.Attributes = decodeAttributes(attributes.(*delta.JsonObject))
		}
		span.Links = append(span.Links, stored)
	}
	status := row.Value("status").(*delta.JsonObject)
	span.Status = telemetry.SpanStatus{Status: telemetry.SpanStatusCode(status.Value("status").(string))}
	if failure, ok := status.Get("error"); ok {
		details := failure.(*delta.JsonObject)
		span.Status.Error = &telemetry.SpanStatusError{Name: details.Value("name").(string), Message: details.Value("message").(string)}
	}
	return span
}

func decodeMetrics(rows []any) []telemetry.StoredMetric {
	metrics := make([]telemetry.StoredMetric, len(rows))
	for i, row := range rows {
		object := row.(*delta.JsonObject)
		metrics[i] = telemetry.StoredMetric{
			IntervalMs: object.Value("intervalMs").(float64), Name: object.Value("name").(string),
			Kind:       telemetry.TelemetryMetricKind(object.Value("kind").(string)),
			Attributes: decodeAttributes(object.Value("attributes").(*delta.JsonObject)),
			Count:      object.Value("count").(float64), Sum: object.Value("sum").(float64),
			Min: object.Value("min").(float64), Max: object.Value("max").(float64),
		}
		if unit, ok := object.Get("unit"); ok {
			metrics[i].Unit = new(unit.(string))
		}
	}
	return metrics
}

func decodeAttributes(object *delta.JsonObject) telemetry.SpanAttributes {
	attributes := make(telemetry.SpanAttributes, 0, object.Len())
	for key, value := range object.All() {
		attributes = attributes.Set(key, decodeAttribute(value))
	}
	return attributes
}

// decodeAttribute is an attribute value: a string, number, boolean or an array of strings or numbers (a worker's): all
// strings a []string, all numbers a []float64, a mix a []any.
func decodeAttribute(value any) telemetry.AttributeValue {
	items, ok := value.([]any)
	if !ok {
		return value
	}
	strings, numbers := make([]string, 0, len(items)), make([]float64, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			strings = append(strings, typed)
		case float64:
			numbers = append(numbers, typed)
		}
	}
	switch {
	case len(strings) == len(items):
		return strings
	case len(numbers) == len(items):
		return numbers
	}
	return slicesClone(items)
}

func slicesClone(items []any) []any { return append([]any(nil), items...) }
