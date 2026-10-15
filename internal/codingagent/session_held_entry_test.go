package codingagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// recordDecodeHeldEntry is the entry a session holds for entry by decoding its record: the decode, or entry itself when the decode holds what
// entry holds (decodesToAppended). heldMessageEntry must choose the same entry without decoding when it can prove the outcome.
func recordDecodeHeldEntry(entry MessageEntry, raw json.RawMessage) SessionEntry {
	decoded := sessionentry.DecodeSessionEntry(raw)
	if shared := sessionentry.WithRecord(entry, raw); decodesToAppended(decoded, shared) {
		return shared
	}
	return decoded
}

// heldEntryOnly is heldMessageEntry's entry; it reports holding entry itself exactly when that entry is the appended one with its record.
func heldEntryOnly(entry MessageEntry, raw json.RawMessage) SessionEntry {
	held, appended := heldMessageEntry(entry, raw)
	if appended != reflect.DeepEqual(held, SessionEntry(sessionentry.WithRecord(entry, raw))) {
		panic("heldMessageEntry reports holding the appended entry when it does not, or the reverse")
	}
	return held
}

type nullDetails struct{ Note string }

func (nullDetails) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

type objectDetails struct{ Note string }

func (d objectDetails) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"note": d.Note})
}

type textDetails struct{ Note string }

func (d *textDetails) MarshalText() ([]byte, error) { return []byte(d.Note), nil }

type nestedDetails struct {
	Lines   []string           `json:"lines"`
	Counts  map[string]int     `json:"counts"`
	When    time.Time          `json:"when"`
	Raw     []byte             `json:"raw"`
	Inner   *tools.ReadDetails `json:"inner,omitempty"`
	private string
}

func heldEntryCase(result agent.ToolResultMessage) (MessageEntry, json.RawMessage, error) {
	parent := "0a1b2c3d"
	entry := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "4e5f6a7b", ParentID: &parent, Timestamp: "2026-10-13T08:00:00.000Z"},
		Message: agent.AgentMessage{ToolResult: &result}}
	raw, err := marshalSessionLine(entry)
	return entry, raw, err
}

func readResult(details any) agent.ToolResultMessage {
	return agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_1", ToolName: "read", Timestamp: 1760000000000,
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package main\n\tfunc main() {}\n"}}, Details: details}
}

// A tool result whose decoded form is proven apart from its Details is held as the session held it by decoding its record: the appended
// object when the decode holds what it holds, the decode otherwise. Rows marked shared take the path that does not decode.
func TestHeldMessageEntryMatchesTheRecordDecode(t *testing.T) {
	text := "package main\n\tfunc main() {\"<&>\"}\n"
	truncation := &tools.TruncationResult{Content: text, Truncated: true, TruncatedBy: "lines", TotalLines: 9, OutputLines: 2, MaxLines: 2}
	var nilRead *tools.ReadDetails
	read := readResult(&tools.ReadDetails{Path: "x.go", Truncation: truncation})
	cases := []struct {
		name   string
		result agent.ToolResultMessage
		shared bool
	}{
		{"read details with truncation", read, true},
		{"read details without truncation", readResult(&tools.ReadDetails{Path: "x.go"}), true},
		{"read details value", readResult(tools.ReadDetails{Truncation: truncation}), true},
		{"bash details", readResult(&tools.BashDetails{Truncation: truncation, FullOutputPath: "/tmp/out.txt"}), true},
		{"map details", readResult(map[string]any{"truncation": map[string]any{"truncated": false, "lines": 2500.0}}), true},
		{"integer-like keys", readResult(map[string]any{"b": "x", "2": 1.0, "1": []any{true, nil}}), true},
		{"slice details", readResult([]any{1.0, "x", map[string]any{}}), true},
		{"typed slice", readResult([]string{"a", "b"}), true},
		{"array details", readResult([2]int{1, 2}), true},
		{"byte slice", readResult([]byte{1, 2, 3}), true},
		{"empty struct", readResult(struct{}{}), true},
		{"pointer to pointer", readResult(&[]*tools.ReadDetails{{Path: "x"}}[0]), true},
		{"nested typed details", readResult(nestedDetails{Lines: []string{"a\u2028b", "\x00"}, Counts: map[string]int{"10": 1, "a": 2},
			When: time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC), Raw: []byte("raw"), Inner: &tools.ReadDetails{Truncation: truncation}, private: "p"}), true},
		{"ill-formed text in details", readResult(map[string]any{"text": "a\xffb\xed\xa0\x80c"}), true},
		{"usage, duration and error", func() agent.ToolResultMessage {
			r := read
			ms := int64(42)
			r.Usage, r.DurationMs, r.IsError = &ai.Usage{Input: 3, Output: 4, TotalTokens: 7}, &ms, true
			return r
		}(), true},
		{"nil typed pointer", readResult(nilRead), false},
		{"nil map", readResult(map[string]any(nil)), false},
		{"nil slice", readResult([]string(nil)), false},
		{"marshaler writing null", readResult(nullDetails{Note: "n"}), false},
		{"marshaler writing an object", readResult(objectDetails{Note: "n"}), false},
		{"pointer text marshaler", readResult(&textDetails{Note: "n"}), false},
		{"time value", readResult(time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)), false},
		{"details null flag beside details", func() agent.ToolResultMessage { r := read; r.DetailsNull = true; return r }(), false},
		{"ill-formed content text", func() agent.ToolResultMessage {
			r := read
			r.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: "a\xffb"}}
			return r
		}(), false},
		{"decoded with its source", func() agent.ToolResultMessage {
			var m agent.AgentMessage
			if err := json.Unmarshal([]byte(`{"role":"toolResult","toolCallId":"c","toolName":"read","content":[{"type":"text","text":"x"}],"extra":1,"isError":false,"timestamp":1}`), &m); err != nil {
				panic(err)
			}
			r := *m.ToolResult
			r.Details = &tools.ReadDetails{Path: "x.go"}
			return r
		}(), false},
		{"nil content", func() agent.ToolResultMessage { r := read; r.Content = nil; return r }(), false},
		{"timestamp beyond 2^53", func() agent.ToolResultMessage { r := read; r.Timestamp = 1 << 54; return r }(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entry, raw, err := heldEntryCase(c.result)
			if err != nil {
				t.Fatal(err)
			}
			got, want := heldEntryOnly(entry, raw), recordDecodeHeldEntry(entry, raw)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("held %#v, the record decode holds %#v", got, want)
			}
			_, proven := decodedFormBesideDetails(entry.Message)
			shared := reflect.DeepEqual(got, SessionEntry(sessionentry.WithRecord(entry, raw)))
			if c.shared && (!proven || !shared) {
				t.Fatalf("proven beside details %v, held the appended entry %v: want both", proven, shared)
			}
		})
	}
}

func FuzzHeldMessageEntryMatchesTheRecordDecode(f *testing.F) {
	f.Add("package main\n", "x.go", "lines", uint8(0), int64(1760000000000))
	f.Add("a\xffb", "\xed\xa0\x80", "", uint8(1), int64(1<<54))
	f.Add("", "", "bytes", uint8(2), int64(-1))
	f.Add("<&>\u2028", "2", "1", uint8(3), int64(0))
	f.Fuzz(func(t *testing.T, content, detail, key string, shape uint8, timestamp int64) {
		var details any
		switch shape % 6 {
		case 0:
			details = &tools.ReadDetails{Path: detail, Truncation: &tools.TruncationResult{Content: content, TruncatedBy: key, Truncated: shape&8 != 0}}
		case 1:
			details = map[string]any{key: detail, "n": float64(timestamp)}
		case 2:
			details = []any{detail, key, shape&8 != 0}
		case 3:
			details = &tools.BashDetails{FullOutputPath: detail}
		case 4:
			details = map[string]string{key: strings.Repeat(detail, 3)}
		case 5:
			details = (*tools.ReadDetails)(nil)
		}
		result := readResult(details)
		result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: content}}
		result.Timestamp = timestamp
		result.DetailsNull = shape&16 != 0
		entry, raw, err := heldEntryCase(result)
		if err != nil {
			t.Skip(err)
		}
		if got, want := heldEntryOnly(entry, raw), recordDecodeHeldEntry(entry, raw); !reflect.DeepEqual(got, want) {
			t.Fatalf("held %#v, the record decode holds %#v", got, want)
		}
	})
}
