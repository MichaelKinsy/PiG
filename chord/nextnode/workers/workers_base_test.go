package workers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Pi's base.test.ts:221 and :249 (`nodeBaseInputs` records the entry's own code with its bytes' hash; what cannot be scanned is no
// record) as the Go entry, a program: one file (packages/chord/src/node/workers/base.ts:107).
func TestNodeBaseInputsRecordsTheEntrysCodeWithItsBytesHash(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(entry, []byte("program one"), 0o755); err != nil {
		t.Fatal(err)
	}
	inputs, err := NodeBaseInputs(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (plugins.BaseInputs{entry: plugins.ContentHash([]byte("program one"))}); len(inputs) != 1 || inputs[entry] != want[entry] {
		t.Fatalf("inputs = %v, want %v", inputs, want)
	}
	// An edit of the entry is a different record: the base it outdates is detected by comparing them.
	if err := os.WriteFile(entry, []byte("program two"), 0o755); err != nil {
		t.Fatal(err)
	}
	edited, err := NodeBaseInputs(entry, []string{"node"})
	if err != nil || edited[entry] == inputs[entry] {
		t.Fatalf("edited = %v, %v; want another hash than %v", edited, err, inputs)
	}
}

func TestNodeBaseInputsFailsForWhatCannotBeReadWhole(t *testing.T) {
	dir := t.TempDir()
	if _, err := NodeBaseInputs(filepath.Join(dir, "gone"), nil); err == nil {
		t.Fatal("a missing entry gave a record")
	}
	if _, err := NodeBaseInputs(dir, nil); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a directory: %v", err)
	}
}
