package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendModelAfterReplacementLog appends text to path the way a writer in several write calls does.
func appendModelAfterReplacementLog(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadModelAfterReplacementRecordsWaitsForTheNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	const record = `{"event":"session_start","reason":"new"}`

	appendModelAfterReplacementLog(t, path, record[:12])
	if got := readModelAfterReplacementRecords(t, path); len(got) != 0 {
		t.Fatalf("a partial record was read: %+v", got)
	}
	appendModelAfterReplacementLog(t, path, record[12:])
	if got := readModelAfterReplacementRecords(t, path); len(got) != 0 {
		t.Fatalf("a record without its newline was read: %+v", got)
	}
	appendModelAfterReplacementLog(t, path, "\n")
	got := readModelAfterReplacementRecords(t, path)
	if len(got) != 1 || got[0].Event != "session_start" || got[0].Reason != "new" {
		t.Fatalf("records = %+v, want the one session_start", got)
	}

	appendModelAfterReplacementLog(t, path, `{"event":"model_sel`)
	if got := readModelAfterReplacementRecords(t, path); len(got) != 1 {
		t.Fatalf("the unfinished second record changed the result: %+v", got)
	}
}

func TestParseModelAfterReplacementRecordsLeavesTheUnfinishedTail(t *testing.T) {
	records, err := parseModelAfterReplacementRecords([]byte("{\"event\":\"a\"}\n\n{\"event\":\"b\"}\n{\"eve"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Event != "a" || records[1].Event != "b" {
		t.Fatalf("records = %+v", records)
	}
}

func TestParseModelAfterReplacementRecordsRejectsACompleteMalformedLine(t *testing.T) {
	_, err := parseModelAfterReplacementRecords([]byte("{\"event\":\"a\"}\nnot json\n"))
	if err == nil || !strings.Contains(err.Error(), `"not json"`) {
		t.Fatalf("err = %v, want one naming the line", err)
	}
}
