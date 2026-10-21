package telemetrysqlite

// Ports packages/chord/src/telemetry-sqlite/store.ts.
//
// Upstream's `Promise<T>` reads are blocking calls returning the value and an error; findTraces' transaction callback
// runs on the caller's goroutine through SqliteDatabase.Transaction.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// defaultTraceLimit is `query.limit ?? 20`.
const defaultTraceLimit = 20

var errRowColumn = errors.New("telemetry-sqlite: unexpected column type")

// rowText reads a TEXT column; NULL is "" and ok=false.
func rowText(row chordsqlite.SqliteRow, column string) (text string, ok bool, err error) {
	switch value := row[column].(type) {
	case nil:
		return "", false, nil
	case string:
		return value, true, nil
	case []byte:
		return string(value), true, nil
	default:
		return "", false, fmt.Errorf("%w: %s is %T", errRowColumn, column, row[column])
	}
}

// rowNumber reads a numeric column as a JavaScript number.
func rowNumber(row chordsqlite.SqliteRow, column string) (float64, error) {
	switch value := row[column].(type) {
	case float64:
		return value, nil
	case int64:
		return float64(value), nil
	case int:
		return float64(value), nil
	default:
		return 0, fmt.Errorf("%w: %s is %T", errRowColumn, column, row[column])
	}
}

type spanRow struct {
	id, traceID, name, attributes, events, links, status string
	parentID                                             *string
	startedMs, endedMs                                   float64
}

func readSpanRow(row chordsqlite.SqliteRow) (spanRow, error) {
	var result spanRow
	var err error
	for _, field := range []struct {
		column string
		target *string
	}{
		{"id", &result.id}, {"trace_id", &result.traceID}, {"name", &result.name}, {"attributes", &result.attributes},
		{"events", &result.events}, {"links", &result.links}, {"status", &result.status},
	} {
		text, _, textErr := rowText(row, field.column)
		if textErr != nil {
			return spanRow{}, textErr
		}
		*field.target = text
	}
	parent, present, err := rowText(row, "parent_id")
	if err != nil {
		return spanRow{}, err
	}
	if present {
		result.parentID = &parent
	}
	if result.startedMs, err = rowNumber(row, "started_ms"); err != nil {
		return spanRow{}, err
	}
	if result.endedMs, err = rowNumber(row, "ended_ms"); err != nil {
		return spanRow{}, err
	}
	return result, nil
}

// decodeAttributeValue is JSON.parse of one attribute value: a string, number, boolean, or array of those. An array
// keeps the element type of its first element ([]string when empty); null is upstream's undefined and is dropped by
// the caller.
func decodeAttributeValue(raw json.RawMessage) (telemetry.AttributeValue, error) {
	if len(raw) == 0 {
		return nil, errors.New("telemetry-sqlite: empty attribute value")
	}
	switch raw[0] {
	case 'n':
		return nil, nil
	case '"':
		var text string
		return text, json.Unmarshal(raw, &text)
	case 't', 'f':
		var flag bool
		return flag, json.Unmarshal(raw, &flag)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		switch {
		case len(items) > 0 && items[0][0] == '"':
			values := make([]string, len(items))
			return values, unmarshalAll(items, func(i int, raw json.RawMessage) error { return json.Unmarshal(raw, &values[i]) })
		case len(items) > 0 && (items[0][0] == 't' || items[0][0] == 'f'):
			values := make([]bool, len(items))
			return values, unmarshalAll(items, func(i int, raw json.RawMessage) error { return json.Unmarshal(raw, &values[i]) })
		case len(items) > 0:
			values := make([]float64, len(items))
			return values, unmarshalAll(items, func(i int, raw json.RawMessage) error { values[i] = jsnumber.FromJSON(raw); return nil })
		}
		return []string{}, nil
	default:
		return jsnumber.FromJSON(raw), nil
	}
}

func unmarshalAll(items []json.RawMessage, decode func(int, json.RawMessage) error) error {
	for i, item := range items {
		if err := decode(i, item); err != nil {
			return err
		}
	}
	return nil
}

// decodeAttributes is JSON.parse of an attribute object: members in the order written, with array-index names listed
// first as a JavaScript object lists them.
func decodeAttributes(text string) (telemetry.SpanAttributes, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	opening, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("telemetry-sqlite: attributes are not an object")
	}
	attributes := telemetry.SpanAttributes{}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		value, err := decodeAttributeValue(raw)
		if err != nil {
			return nil, err
		}
		if value != nil {
			attributes = attributes.Set(name.(string), value)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return attributes, nil
}

func storedSpan(row spanRow) (telemetry.StoredSpan, error) {
	attributes, err := decodeAttributes(row.attributes)
	if err != nil {
		return telemetry.StoredSpan{}, err
	}
	var events []struct {
		Name       string          `json:"name"`
		TimeMs     float64         `json:"time_ms"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(row.events), &events); err != nil {
		return telemetry.StoredSpan{}, err
	}
	var links []struct {
		TraceID    string          `json:"trace_id"`
		SpanID     string          `json:"span_id"`
		Attributes json.RawMessage `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(row.links), &links); err != nil {
		return telemetry.StoredSpan{}, err
	}
	var status struct {
		Status string `json:"status"`
		Error  *struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(row.status), &status); err != nil {
		return telemetry.StoredSpan{}, err
	}
	span := telemetry.StoredSpan{
		ID: row.id, TraceID: row.traceID, ParentID: row.parentID, Name: row.name, StartedMs: row.startedMs,
		EndedMs: row.endedMs, Attributes: attributes, Events: make([]telemetry.StoredSpanEvent, len(events)),
		Links: make([]telemetry.StoredSpanLink, len(links)), Status: telemetry.SpanStatus{Status: telemetry.SpanStatusCode(status.Status)},
	}
	if status.Error != nil {
		span.Status.Error = &telemetry.SpanStatusError{Name: status.Error.Name, Message: status.Error.Message}
	}
	for i, event := range events {
		eventAttributes, err := decodeAttributes(string(event.Attributes))
		if err != nil {
			return telemetry.StoredSpan{}, err
		}
		span.Events[i] = telemetry.StoredSpanEvent{Name: event.Name, TimeMs: event.TimeMs, Attributes: eventAttributes}
	}
	for i, link := range links {
		stored := telemetry.StoredSpanLink{TraceID: link.TraceID, SpanID: link.SpanID}
		if len(link.Attributes) > 0 {
			if stored.Attributes, err = decodeAttributes(string(link.Attributes)); err != nil {
				return telemetry.StoredSpan{}, err
			}
		}
		span.Links[i] = stored
	}
	return span, nil
}

func storedSpans(rows []chordsqlite.SqliteRow) ([]telemetry.StoredSpan, error) {
	spans := make([]telemetry.StoredSpan, len(rows))
	for i, row := range rows {
		decoded, err := readSpanRow(row)
		if err != nil {
			return nil, err
		}
		if spans[i], err = storedSpan(decoded); err != nil {
			return nil, err
		}
	}
	return spans, nil
}

// attributesSQL is attributesSql: JSON equality must distinguish boolean true from numeric 1, and json_each also
// handles arbitrary attribute keys. Upstream's Object.entries order is insertion order; a Go map has none, so the keys
// are sorted (the conditions are conjunctive, so the order is unobservable).
func attributesSQL(attributes map[string]telemetry.AttributeValue, column string, params *[]chordsqlite.SqliteValue) (string, error) {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	conditions := make([]string, 0, len(keys))
	for _, key := range keys {
		var entryType string
		var bound chordsqlite.SqliteValue
		switch value := attributes[key].(type) {
		case bool:
			entryType = "entry.type = 'false'"
			bound = int64(0)
			if value {
				entryType = "entry.type = 'true'"
				bound = int64(1)
			}
		case string:
			entryType, bound = "entry.type = 'text'", value
		default:
			number, ok := attributeNumber(value)
			if !ok {
				return "", fmt.Errorf("telemetry-sqlite: attribute %s must be a string, number or boolean, not %T", strconv.Quote(key), value)
			}
			entryType, bound = "entry.type IN ('integer', 'real')", number
		}
		*params = append(*params, key, bound)
		conditions = append(conditions, "EXISTS (SELECT 1 FROM json_each("+column+") entry WHERE entry.key = ? AND "+entryType+" AND entry.value = ?)")
	}
	if len(conditions) == 0 {
		return "1", nil
	}
	return strings.Join(conditions, " AND "), nil
}

func attributeNumber(value telemetry.AttributeValue) (float64, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint()), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return 0, false
	}
}

func getTrace(db chordsqlite.SqliteDatabase, traceID string) (*telemetry.StoredTrace, error) {
	rows, err := db.All("SELECT * FROM telemetry_spans WHERE trace_id = ? ORDER BY started_ms, id", traceID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	spans, err := storedSpans(rows)
	if err != nil {
		return nil, err
	}
	return &telemetry.StoredTrace{TraceID: traceID, Spans: spans}, nil
}

// traceCursor is the `[startedMs, traceId]` tuple the cursor JSON holds.
type traceCursor struct {
	startedMs float64
	traceID   string
}

func parseCursor(cursor string) (traceCursor, error) {
	var tuple []json.RawMessage
	if err := json.Unmarshal([]byte(cursor), &tuple); err != nil {
		return traceCursor{}, err
	}
	if len(tuple) != 2 {
		return traceCursor{}, errors.New("telemetry-sqlite: invalid trace cursor")
	}
	var traceID string
	if err := json.Unmarshal(tuple[1], &traceID); err != nil {
		return traceCursor{}, err
	}
	return traceCursor{startedMs: jsnumber.FromJSON(tuple[0]), traceID: traceID}, nil
}

func formatCursor(startedMs float64, traceID string) (string, error) {
	quoted, err := jsstring.MarshalJSON(traceID)
	if err != nil {
		return "", err
	}
	return "[" + jsnumber.String(startedMs) + "," + string(quoted) + "]", nil
}

func findTraces(db chordsqlite.SqliteDatabase, query telemetry.TraceQuery) (telemetry.FindTracesResult, error) {
	limit := defaultTraceLimit
	if query.Limit != nil {
		limit = *query.Limit
	}
	var conditions []string
	var params []chordsqlite.SqliteValue
	if query.Name != nil {
		conditions = append(conditions, "(SELECT name FROM telemetry_spans WHERE trace_id = heads.trace_id ORDER BY started_ms, id LIMIT 1) = ?")
		params = append(params, *query.Name)
	}
	if query.StartMin != nil {
		conditions = append(conditions, "started_ms >= ?")
		params = append(params, *query.StartMin)
	}
	if query.StartMax != nil {
		conditions = append(conditions, "started_ms <= ?")
		params = append(params, *query.StartMax)
	}
	if query.Errors {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM telemetry_spans WHERE trace_id = heads.trace_id AND json_extract(status, '$.status') = 'error')")
	}
	if query.Attributes != nil {
		attributeCondition, err := attributesSQL(query.Attributes, "candidate.attributes", &params)
		if err != nil {
			return telemetry.FindTracesResult{}, err
		}
		conditions = append(conditions, "EXISTS (SELECT 1 FROM telemetry_spans candidate WHERE candidate.trace_id = heads.trace_id AND "+attributeCondition+")")
	}
	if query.Cursor != nil {
		cursor, err := parseCursor(*query.Cursor)
		if err != nil {
			return telemetry.FindTracesResult{}, err
		}
		conditions = append(conditions, "(started_ms < ? OR (started_ms = ? AND trace_id > ?))")
		params = append(params, cursor.startedMs, cursor.startedMs, cursor.traceID)
	}
	where := "1"
	if len(conditions) > 0 {
		where = strings.Join(conditions, " AND ")
	}
	var result telemetry.FindTracesResult
	err := db.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		result = telemetry.FindTracesResult{}
		heads, err := tx.All(`WITH heads AS (
			SELECT trace_id, min(started_ms) started_ms FROM telemetry_spans GROUP BY trace_id
		) SELECT * FROM heads WHERE `+where+" ORDER BY started_ms DESC, trace_id LIMIT ?", append(slices.Clone(params), int64(limit)+1)...)
		if err != nil {
			return err
		}
		more := len(heads) > limit
		if more {
			heads = heads[:len(heads)-1]
		}
		if len(heads) == 0 {
			return nil
		}
		traceIDs := make([]string, len(heads))
		for i, head := range heads {
			if traceIDs[i], _, err = rowText(head, "trace_id"); err != nil {
				return err
			}
		}
		encoded, err := jsstring.MarshalJSON(traceIDs)
		if err != nil {
			return err
		}
		rows, err := tx.All("SELECT * FROM telemetry_spans WHERE trace_id IN (SELECT value FROM json_each(?)) ORDER BY started_ms, id", string(encoded))
		if err != nil {
			return err
		}
		grouped := map[string][]telemetry.StoredSpan{}
		for _, row := range rows {
			decoded, err := readSpanRow(row)
			if err != nil {
				return err
			}
			span, err := storedSpan(decoded)
			if err != nil {
				return err
			}
			grouped[decoded.traceID] = append(grouped[decoded.traceID], span)
		}
		result.Traces = make([]telemetry.StoredTrace, len(heads))
		for i, traceID := range traceIDs {
			result.Traces[i] = telemetry.StoredTrace{TraceID: traceID, Spans: grouped[traceID]}
		}
		if more {
			startedMs, err := rowNumber(heads[len(heads)-1], "started_ms")
			if err != nil {
				return err
			}
			cursor, err := formatCursor(startedMs, traceIDs[len(traceIDs)-1])
			if err != nil {
				return err
			}
			result.Cursor = &cursor
		}
		return nil
	})
	if err != nil {
		return telemetry.FindTracesResult{}, err
	}
	return result, nil
}

func findMetrics(db chordsqlite.SqliteDatabase, query telemetry.MetricQuery) ([]telemetry.StoredMetric, error) {
	params := []chordsqlite.SqliteValue{query.Name}
	conditions := []string{"name = ?"}
	if query.StartMin != nil {
		conditions = append(conditions, "interval_ms >= ?")
		params = append(params, *query.StartMin)
	}
	if query.StartMax != nil {
		conditions = append(conditions, "interval_ms <= ?")
		params = append(params, *query.StartMax)
	}
	attributeCondition, err := attributesSQL(query.Attributes, "attributes", &params)
	if err != nil {
		return nil, err
	}
	conditions = append(conditions, attributeCondition)
	rows, err := db.All("SELECT * FROM telemetry_metrics WHERE "+strings.Join(conditions, " AND ")+" ORDER BY interval_ms, series_key", params...)
	if err != nil {
		return nil, err
	}
	metrics := make([]telemetry.StoredMetric, len(rows))
	for i, row := range rows {
		metric := telemetry.StoredMetric{}
		var text string
		var present bool
		if text, _, err = rowText(row, "name"); err != nil {
			return nil, err
		}
		metric.Name = text
		if text, _, err = rowText(row, "kind"); err != nil {
			return nil, err
		}
		metric.Kind = telemetry.TelemetryMetricKind(text)
		if text, present, err = rowText(row, "unit"); err != nil {
			return nil, err
		}
		if present {
			metric.Unit = new(text)
		}
		if text, _, err = rowText(row, "attributes"); err != nil {
			return nil, err
		}
		if metric.Attributes, err = decodeAttributes(text); err != nil {
			return nil, err
		}
		for _, field := range []struct {
			column string
			target *float64
		}{{"interval_ms", &metric.IntervalMs}, {"count", &metric.Count}, {"sum", &metric.Sum}, {"min", &metric.Min}, {"max", &metric.Max}} {
			if *field.target, err = rowNumber(row, field.column); err != nil {
				return nil, err
			}
		}
		metrics[i] = metric
	}
	return metrics, nil
}
