package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStderrCauseKeepsPackedMemberOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	log := `extension "first" failed to load: Error: first factory failed
    at default (first.ts:1:1)
extension "second" failed to load: Error: second factory failed
    at default (second.ts:1:1)
(node:123) ExperimentalWarning: stripTypeScriptTypes is an experimental feature
(Use ` + "`node --trace-warnings ...`" + ` to show where the warning was created)
`
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if got, want := stderrCause(path, name), "Error: "+name+" factory failed"; got != want {
			t.Fatalf("%s cause=%q; want %q", name, got, want)
		}
	}
}
