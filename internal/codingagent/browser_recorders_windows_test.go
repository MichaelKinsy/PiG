package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installArgvRecorders puts copies of this test binary under the given launcher names first on PATH and returns the record file they append to.
func installArgvRecorders(t *testing.T, names ...string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), binary, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	record := filepath.Join(dir, "argv.jsonl")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(argvRecordEnv, record)
	return record
}

// readArgvRecords waits up to 20s for at least n complete launcher records.
func readArgvRecords(t *testing.T, record string, n int) []recordedArgv {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		data, err := os.ReadFile(record)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		var records []recordedArgv
		for line := range strings.SplitSeq(string(data), "\n") {
			var got recordedArgv
			if line != "" && json.Unmarshal([]byte(line), &got) == nil {
				records = append(records, got)
			}
		}
		if len(records) >= n {
			return records
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d launcher records after 20s: %q", len(records), n, data)
		}
	}
}
