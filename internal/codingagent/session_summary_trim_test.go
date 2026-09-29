package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestSessionSummaryNameUsesJavaScriptTrim(t *testing.T) {
	// session-manager.ts:830 trims the latest session_info name for listing, preserving NEL and lone UTF-16 units just like getSessionName.
	for _, tc := range []struct{ nameJSON, want string }{
		{`"\ufeff"`, ""},
		{`" \u0085 "`, "\u0085"},
		{`"\ufeffname\ufeff"`, "name"},
		{`"\u0085name\u0085"`, "\u0085name\u0085"},
		{`" \ud800 "`, jsstring.FromUTF16([]uint16{0xd800})},
	} {
		t.Run(tc.nameJSON, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			data := `{"type":"session","version":3,"id":"session","timestamp":"2026-07-01T00:00:00.000Z","cwd":"/work"}` + "\n" +
				`{"type":"session_info","id":"first","name":"old"}` + "\n" +
				`{"type":"session_info","id":"second","name":` + tc.nameJSON + "}\n"
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := summarizeSessionFile(path)
			if err != nil || info.Name != tc.want {
				t.Fatalf("summary name=%q err=%v want=%q", info.Name, err, tc.want)
			}
		})
	}
}
