package telemetryingest

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The stored spans and metric rows a sender sends, as the JSON values of a batch (the inverse of decode.go).

func encodeSpans(spans []telemetry.StoredSpan) []any {
	rows := make([]any, len(spans))
	for i, span := range spans {
		row := delta.NewJsonObject(10)
		row.Set("id", span.ID)
		row.Set("traceId", span.TraceID)
		if span.ParentID != nil {
			row.Set("parentId", *span.ParentID)
		}
		row.Set("name", span.Name)
		row.Set("startedMs", span.StartedMs)
		row.Set("endedMs", span.EndedMs)
		row.Set("attributes", encodeAttributes(span.Attributes))
		events := make([]any, len(span.Events))
		for j, event := range span.Events {
			events[j] = delta.JsonObjectOf("name", event.Name, "timeMs", event.TimeMs, "attributes", encodeAttributes(event.Attributes))
		}
		row.Set("events", events)
		links := make([]any, len(span.Links))
		for j, link := range span.Links {
			encoded := delta.JsonObjectOf("traceId", link.TraceID, "spanId", link.SpanID)
			if link.Attributes != nil {
				encoded.Set("attributes", encodeAttributes(link.Attributes))
			}
			links[j] = encoded
		}
		row.Set("links", links)
		status := delta.JsonObjectOf("status", string(span.Status.Status))
		if span.Status.Status == telemetry.SpanStatusCodeError && span.Status.Error != nil {
			status.Set("error", delta.JsonObjectOf("name", span.Status.Error.Name, "message", span.Status.Error.Message))
		}
		row.Set("status", status)
		rows[i] = row
	}
	return rows
}

func encodeMetrics(metrics []telemetry.StoredMetric) []any {
	rows := make([]any, len(metrics))
	for i, stored := range metrics {
		row := delta.JsonObjectOf("intervalMs", stored.IntervalMs, "name", stored.Name, "kind", string(stored.Kind))
		if stored.Unit != nil {
			row.Set("unit", *stored.Unit)
		}
		row.Set("attributes", encodeAttributes(stored.Attributes))
		row.Set("count", stored.Count)
		row.Set("sum", stored.Sum)
		row.Set("min", stored.Min)
		row.Set("max", stored.Max)
		rows[i] = row
	}
	return rows
}

func encodeAttributes(attributes telemetry.SpanAttributes) *delta.JsonObject {
	object := delta.NewJsonObject(len(attributes))
	for _, attribute := range attributes {
		object.Set(attribute.Name, encodeAttribute(attribute.Value))
	}
	return object
}

// encodeAttribute is an attribute value as JSON: numbers as float64, arrays as []any.
func encodeAttribute(value any) any {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Slice:
		items := make([]any, reflected.Len())
		for i := range items {
			items[i] = encodeAttribute(reflected.Index(i).Interface())
		}
		return items
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint())
	case reflect.Float32:
		return reflected.Float()
	}
	return value
}
