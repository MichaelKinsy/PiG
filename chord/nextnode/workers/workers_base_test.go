package workers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
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
	if want := (model.BaseInputs{entry: model.ContentHash([]byte("program one"))}); len(inputs) != 1 || inputs[entry] != want[entry] {
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

func TestNodeBaseInputsNamesTheEntryByItsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server"), []byte("program"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	inputs, err := NodeBaseInputs("server", nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("server")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[want] != model.ContentHash([]byte("program")) {
		t.Fatalf("inputs = %v, want %s with the program's hash", inputs, want)
	}
}

func TestNodeBaseInputsFailsForAFileItsBytesCannotBeReadFrom(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the file's mode does not keep this user from reading it")
	}
	entry := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(entry, []byte("program"), 0o000); err != nil {
		t.Fatal(err)
	}
	if inputs, err := NodeBaseInputs(entry, nil); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("an unreadable entry: %v, %v", inputs, err)
	}
}
