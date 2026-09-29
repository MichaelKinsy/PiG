package codingagent

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Pi's sessionEntryToContextMessages uses Date.getTime(): integer milliseconds,
// including for Go-persisted timestamps with more than three fractional digits.
func TestExtensionSessionTimestampUsesMilliseconds(t *testing.T) {
	for _, tc := range []struct {
		stamp string
		want  any
	}{
		{"1970-01-01T00:00:00.123456789Z", float64(123)},
		{"1969-12-31T23:59:59.999999999Z", float64(-1)},
		{"2500-01-01T00:00:00.000Z", float64(16725225600000)},
		{"invalid", nil},
	} {
		if got := jsTimestamp(tc.stamp); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.stamp, got, tc.want)
		}
	}
}

func TestExtensionSessionReadRejectsCorruptRetainedEntry(t *testing.T) {
	s := NewSession("corrupt", "/test")
	s.entries = []SessionEntry{{raw: json.RawMessage(`{"type":`)}}
	if _, err := ExtensionSessionRead(ExtensionSessionView{Session: s}, "getEntries", nil); err == nil {
		t.Fatal("corrupt retained entry was silently discarded")
	}
}

func BenchmarkExtensionSessionProjection(b *testing.B) {
	s := NewSession("benchmark", "/benchmark")
	for index := range 1000 {
		parent := any(nil)
		if index > 0 {
			parent = fmt.Sprint(index - 1)
		}
		entry := map[string]any{"id": fmt.Sprint(index), "parentId": parent, "type": "custom_message", "timestamp": "2026-01-01T00:00:00.123456789Z", "customType": "note", "content": "representative context entry", "display": false}
		data, err := json.Marshal(entry)
		if err != nil {
			b.Fatal(err)
		}
		if err := s.AppendEntry(json.RawMessage(data)); err != nil {
			b.Fatal(err)
		}
	}
	view := ExtensionSessionView{Session: s, CWD: "/benchmark"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ExtensionSessionRead(view, "buildSessionProjection", nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestExtensionSessionDirPreservesRelativePath(t *testing.T) {
	for _, path := range []string{"sessions", "./sessions", "../sessions"} {
		if got := ExtensionSessionDir(path); got != path {
			t.Errorf("%q became %q", path, got)
		}
	}
}
