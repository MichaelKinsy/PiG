package codingagent

import (
	"bytes"
	"encoding/json"
	"path/filepath"
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
	held, appended := heldMessageEntry(entry, raw, proveHeldEntry(entry))
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
	entry := MessageEntry{Type: "message", ID: "4e5f6a7b", ParentID: &parent, Timestamp: "2026-10-13T08:00:00.000Z",
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

// A message whose decoded form is proven is held as the session held it by decoding its record: the appended object when the decode holds what
// it holds (an assistant message with its usage, a user message with content), the decode otherwise (an assistant message without usage, a user
// message without content). One proof, made before the record exists, decides both that and whether a session without a file encodes a record
// at all: it does not for exactly the entries held as appended, and a session with a file always does.
func TestHeldMessageEntryOfAProvenFormMatchesTheRecordDecode(t *testing.T) {
	// The corpus without its ninth message, a custom message, which is not a message entry; then BenchmarkRuntimeInteractiveRebind's message
	// (content, no usage). decodeHeld are the messages held as their record decodes: the two filelessEncodes names, and the benchmark's.
	messages := filelessEncodeCorpus()[:8]
	messages = append(messages, agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "restored answer"}}}})
	decodeHeld := map[int]bool{2: true, 4: true, 8: true}
	filed := NewSession("filed", "/project")
	filed.SetPath(filepath.Join(t.TempDir(), "s.jsonl"))
	fileless := NewSession("fileless", "/project")
	parent := "0a1b2c3d"
	for i, message := range messages {
		entry := MessageEntry{Type: "message", ID: "4e5f6a7b", ParentID: &parent, Timestamp: "2026-10-13T08:00:00.000Z", Message: message}
		raw, err := marshalSessionLine(entry)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		proof := proveHeldEntry(entry)
		got, appended := heldMessageEntry(entry, raw, proof)
		if want := recordDecodeHeldEntry(entry, raw); !reflect.DeepEqual(got, want) {
			t.Errorf("message %d: held %#v, the record decode holds %#v", i, got, want)
		}
		if appended == decodeHeld[i] {
			t.Errorf("message %d: held as appended %v, want %v", i, appended, !decodeHeld[i])
		}
		if appended != reflect.DeepEqual(got, SessionEntry(sessionentry.WithRecord(entry, raw))) {
			t.Errorf("message %d: held as appended %v, but the held entry says the reverse", i, appended)
		}
		if !appended && !bytes.Equal(got.Raw(), raw) {
			t.Errorf("message %d: the held decode's record is %s, want the record %s", i, got.Raw(), raw)
		}
		if got := fileless.holdsWithoutRecord(entry, proof); got != appended {
			t.Errorf("message %d: a session without a file holds it without a record: %v, want %v", i, got, appended)
		}
		if filed.holdsWithoutRecord(entry, proof) {
			t.Errorf("message %d: a session with a file holds it without a record", i)
		}
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
