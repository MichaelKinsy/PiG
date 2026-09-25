package session

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

func jsonlInteger(fields map[string]json.RawMessage, key string, minimum int64) bool {
	var value float64
	raw, exists := fields[key]
	return exists && string(raw) != "null" && json.Unmarshal(raw, &value) == nil && value >= float64(minimum) && value <= maxSafeInteger && math.Trunc(value) == value
}

func jsonlString(fields map[string]json.RawMessage, key string, optional bool) bool {
	raw, exists := fields[key]
	if !exists {
		return optional
	}
	var value string
	return string(raw) != "null" && json.Unmarshal(raw, &value) == nil
}

func parseLegacyTimestamp(value string) (int64, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02", "2006-01-02T15:04:05", time.RFC1123, time.RFC1123Z} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("Invalid legacy v3 timestamp: %s", value)
}

// ParseJsonlSessionHeader recognizes the current and upstream legacy header shapes.
func ParseJsonlSessionHeader(line string) (JsonlParsedSessionHeader, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return JsonlParsedSessionHeader{}, fmt.Errorf("Invalid JSONL session header: not valid JSON: %w", err)
	}
	var header JsonlStorageHeader
	if json.Unmarshal([]byte(line), &header) == nil && header.V == JSONLFormatVersion && header.Kind == "header" &&
		jsonlString(fields, "id", false) && jsonlString(fields, "cwd", false) && jsonlInteger(fields, "storageVersion", 1) && jsonlInteger(fields, "createdAt", 0) &&
		(fields["nextSeq"] == nil || jsonlInteger(fields, "nextSeq", 1)) && jsonlString(fields, "parentSessionId", true) && jsonlString(fields, "legacyParentSessionPath", true) {
		return JsonlParsedSessionHeader{Header: &header}, nil
	}
	var legacy LegacyV3SessionHeader
	if json.Unmarshal([]byte(line), &legacy) == nil && legacy.Type == "session" && legacy.Version == 3 && jsonlString(fields, "id", false) && jsonlString(fields, "cwd", false) && jsonlString(fields, "timestamp", false) && jsonlString(fields, "parentSession", true) {
		if _, err := parseLegacyTimestamp(legacy.Timestamp); err == nil {
			return JsonlParsedSessionHeader{Legacy: &legacy}, nil
		}
	}
	return JsonlParsedSessionHeader{}, fmt.Errorf("Unsupported JSONL session header")
}

// ParseJsonlTransaction decodes one complete transaction without applying it.
func ParseJsonlTransaction(line string) ([]CommittedWrite, error) {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("Invalid JSONL transaction: not valid JSON: %w", err)
	}
	items := []json.RawMessage{raw}
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
	}
	writes := make([]CommittedWrite, 0, len(items))
	for _, item := range items {
		write, err := parseCommittedWrite(item)
		if err != nil {
			return nil, err
		}
		writes = append(writes, write)
	}
	return writes, nil
}

func parseCommittedWrite(raw json.RawMessage) (CommittedWrite, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("Invalid JSONL transaction write")
	}
	if !jsonlInteger(fields, "seq", 1) {
		return nil, fmt.Errorf("Invalid JSONL write seq")
	}
	var value struct {
		Kind      string `json:"kind"`
		Op        string `json:"op"`
		Seq       int64  `json:"seq"`
		Namespace string `json:"namespace"`
		Key       string `json:"key"`
		Value     any    `json:"value"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	switch value.Kind {
	case "entry":
		if !jsonlInteger(fields, "timestamp", 0) {
			return nil, fmt.Errorf("Invalid JSONL entry timestamp")
		}
		var entry Entry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		return CommittedEntryWrite{Entry: entry}, nil
	case "usage":
		var row UsageRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		return CommittedUsageWrite{Row: row}, nil
	case "value":
		switch value.Op {
		case "set":
			return CommittedValueSetWrite{Seq: value.Seq, Namespace: value.Namespace, Key: value.Key, Value: value.Value}, nil
		case "delete":
			return CommittedValueDeleteWrite{Seq: value.Seq, Namespace: value.Namespace, Key: value.Key}, nil
		}
		return nil, fmt.Errorf("Invalid JSONL value operation: %s", value.Op)
	case "list":
		switch value.Op {
		case "append":
			return CommittedListAppendWrite{Seq: value.Seq, Namespace: value.Namespace, Key: value.Key, Value: value.Value}, nil
		case "delete":
			return CommittedListDeleteWrite{Seq: value.Seq, Namespace: value.Namespace, Key: value.Key}, nil
		}
		return nil, fmt.Errorf("Invalid JSONL list operation: %s", value.Op)
	default:
		return nil, fmt.Errorf("Invalid JSONL write kind: %s", value.Kind)
	}
}

func committedWriteJSON(write CommittedWrite) (json.RawMessage, error) {
	var value any
	switch w := write.(type) {
	case CommittedEntryWrite:
		raw, err := json.Marshal(w.Entry)
		if err != nil {
			return nil, err
		}
		return append([]byte(`{"kind":"entry",`), raw[1:]...), nil
	case CommittedUsageWrite:
		raw, err := json.Marshal(w.Row)
		if err != nil {
			return nil, err
		}
		return append([]byte(`{"kind":"usage",`), raw[1:]...), nil
	case CommittedValueSetWrite:
		value = struct {
			Kind      string `json:"kind"`
			Op        string `json:"op"`
			Seq       int64  `json:"seq"`
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     any    `json:"value"`
		}{"value", "set", w.Seq, w.Namespace, w.Key, w.Value}
	case CommittedListAppendWrite:
		value = struct {
			Kind      string `json:"kind"`
			Op        string `json:"op"`
			Seq       int64  `json:"seq"`
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     any    `json:"value"`
		}{"list", "append", w.Seq, w.Namespace, w.Key, w.Value}
	case CommittedValueDeleteWrite:
		value = struct {
			Kind      string `json:"kind"`
			Op        string `json:"op"`
			Seq       int64  `json:"seq"`
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
		}{"value", "delete", w.Seq, w.Namespace, w.Key}
	case CommittedListDeleteWrite:
		value = struct {
			Kind      string `json:"kind"`
			Op        string `json:"op"`
			Seq       int64  `json:"seq"`
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
		}{"list", "delete", w.Seq, w.Namespace, w.Key}
	default:
		return nil, fmt.Errorf("unknown committed write %T", write)
	}
	return json.Marshal(value)
}

// SerializeJsonlTransaction uses the single-write form for one write and arrays otherwise.
func SerializeJsonlTransaction(writes []CommittedWrite) (string, error) {
	items := make([]json.RawMessage, 0, len(writes))
	for _, write := range writes {
		raw, err := committedWriteJSON(write)
		if err != nil {
			return "", err
		}
		items = append(items, raw)
	}
	if len(items) == 1 {
		return string(items[0]), nil
	}
	raw, err := json.Marshal(items)
	return string(raw), err
}
