package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts (readLines decodes each line with TextDecoder.decode; recover writes jsonLine(line.value) for every retained sidecar record it reclaims)

import (
	"os"
	"path/filepath"
	"testing"

	chorddelta "github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

const (
	rewriteMainLog = `{"format":1,"type":"commit","seq":1,"writes":[{"type":"conversation","value":{"id":1}}]}` + "\n" +
		`{"format":1,"type":"commit","seq":2,"writes":[{"type":"document.create","record":{"id":5,"kind":"k","scope":{"kind":"session"}},"ordinal":0}]}` + "\n" +
		`{"format":1,"type":"commit","seq":3,"writes":[{"type":"document.change","id":5,"ordinal":0}]}` + "\n"
	supersededBase = `{"format":1,"type":"record","seq":2,"ordinal":0,"payload":{"type":"document","id":5,"content":{"version":1,"kind":"base","value":{"a":1}}}}` + "\n"
)

func writeDirectory(t *testing.T, files map[string]string) string {
	t.Helper()
	directory := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

// Pi decodes every line with its own TextDecoder.decode call, which drops one leading byte order mark, so a line that starts with one still parses. Probed against Pi 1.1.0 with the oracle in durable/storage/testdata/memory_oracle.mjs.
func TestJsonlRecoveryIgnoresALeadingByteOrderMarkOnEachLine(t *testing.T) {
	const bom = "\ufeff"
	directory := writeDirectory(t, map[string]string{
		"main.jsonl": bom + `{"format":1,"type":"commit","seq":1,"writes":[{"type":"conversation","value":{"id":1}}]}` + "\n" + bom + `{"format":1,"type":"commit","seq":2,"writes":[{"type":"conversation","value":{"id":2}}]}` + "\n",
	})
	storage := open(t, directory)
	conversation, err := storage.Conversation(testContext, 2)
	if err != nil || conversation == nil || conversation.Id != 2 {
		t.Fatalf("conversation 2 = %+v, %v", conversation, err)
	}

	twice := writeDirectory(t, map[string]string{"main.jsonl": bom + bom + `{"format":1,"type":"commit","seq":1,"writes":[]}` + "\n"})
	if _, err := openJsonl(twice); err == nil || err.Error() != "Malformed complete main.jsonl line 1" {
		t.Fatalf("two byte order marks: %v", err)
	}
}

// A crash between publishing a commit and reclaiming a current-only document's sidecar leaves its superseded records in place; recovery rewrites the file with the records it keeps serialized again, as JSON.stringify writes them, not with the bytes it read. Probed against Pi 1.1.0: the kept line below comes back exactly as want.
func TestJsonlRecoveryRewritesReclaimedSidecarRecordsCanonically(t *testing.T) {
	kept := "\ufeff" + `{"format":1,"type":"record","seq":3,"ordinal":0,"payload":{"type":"document","id":5,"content":{"version":3,"kind":"base","value": {"\u0061" : 3, "s":"<&>` + "\u2028" + `"}}}}` + "\n"
	want := `{"format":1,"type":"record","seq":3,"ordinal":0,"payload":{"type":"document","id":5,"content":{"version":3,"kind":"base","value":{"a":3,"s":"<&>` + "\u2028" + `"}}}}` + "\n"
	directory := writeDirectory(t, map[string]string{"main.jsonl": rewriteMainLog, "doc-5.jsonl": supersededBase + kept})
	storage := open(t, directory)
	got, err := os.ReadFile(filepath.Join(directory, "doc-5.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("doc-5.jsonl after recovery\n got %q\nwant %q", got, want)
	}
	document, err := storage.Document(testContext, 5, durable.CurrentPoint)
	if err != nil || document == nil {
		t.Fatalf("document 5 = %v, %v", document, err)
	}
	expectEqual(t, document.Value, chorddelta.JsonObjectOf("a", 3, "s", "<&>\u2028"))
}
