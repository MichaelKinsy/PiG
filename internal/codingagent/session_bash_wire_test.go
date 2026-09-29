package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkBashExecutionMessageWire(b *testing.B) {
	for _, size := range []int{0, 256, 50 * 1024} {
		entry := BashExecutionEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", ID: "entry", Timestamp: "2026-09-01T00:00:02.000Z"},
			Command:          "command", Output: strings.Repeat("x", size), Cancelled: true, MessageTimestamp: 1788220800000,
		}
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := json.Marshal(entry); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Pi 0.87.1 messages.ts:30-43 and agent-session.ts:3498-3508 keep the inner
// timestamp independent of the enclosing entry time, including after a deferred append.
func TestBashExecutionMessageTimestampRoundTrip(t *testing.T) {
	input := `{"type":"message","id":"entry","parentId":null,"timestamp":"2026-09-01T00:00:02.000Z","message":{"role":"bashExecution","command":"echo hi","output":"hi\n","cancelled":true,"truncated":false,"timestamp":1788220800000}}`
	var entry BashExecutionEntry
	if err := json.Unmarshal([]byte(input), &entry); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(input), &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("round trip=%s; want %s", gotJSON, wantJSON)
	}
}
