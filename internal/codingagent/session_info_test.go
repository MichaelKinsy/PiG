package codingagent

import (
	"encoding/json"
	"testing"
)

// Pi session-manager.ts:1302 replaces CR/LF runs, trims, and persists the sanitized name.
func TestAppendSessionInfoSanitizesPersistedNames(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"hello\nworld\r\nagain", "hello world again"},
		{"from\nextension", "from extension"},
		{"\r\n  first\n\n\rsecond  \r\n", "first second"},
		{"\r\n\r", ""},
		{"  \t  ", ""},
		{"a\n \nb", "a   b"},
		{"\ufeffname\ufeff", "name"},
		{"\u0085name\u0085", "\u0085name\u0085"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			s := NewSession("name-test", t.TempDir())
			if _, err := s.AppendSessionInfo(tc.input); err != nil {
				t.Fatal(err)
			}
			entries := s.Entries()
			if len(entries) != 1 {
				t.Fatalf("entries=%d", len(entries))
			}
			var entry SessionInfoEntry
			if err := json.Unmarshal(entries[0].Raw(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Name != tc.want || s.GetSessionName() != tc.want {
				t.Fatalf("stored=%q effective=%q want=%q", entry.Name, s.GetSessionName(), tc.want)
			}
		})
	}
}
