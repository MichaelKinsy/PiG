package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts (latestBases :601-626, isBeforeLatestBase :628-631, retained sidecar lines :745-749)

import (
	"os"
	"path/filepath"
	"testing"
)

func baseRecord(seq, ordinal, value int) string {
	const id = 5
	digits := func(n int) string { return string(rune('0' + n)) }
	return `{"format":1,"type":"record","seq":` + digits(seq) + `,"ordinal":` + digits(ordinal) + `,"payload":{"type":"document","id":` + digits(id) + `,"content":{"version":1,"kind":"base","value":{"a":` + digits(value) + `}}}}` + "\n"
}

func commitLine(seq int, writes string) string {
	return `{"format":1,"type":"commit","seq":` + string(rune('0'+seq)) + `,"writes":[` + writes + `]}` + "\n"
}

const (
	createFive = `{"type":"document.create","record":{"id":5,"kind":"k","scope":{"kind":"session"}},"ordinal":0}`
)

func recoveredSidecar(t *testing.T, files map[string]string) string {
	t.Helper()
	directory := writeDirectory(t, files)
	open(t, directory)
	got, err := os.ReadFile(filepath.Join(directory, "doc-5.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// storage.ts:601-631, :745-749: of a current-only document's confirmed sidecar records, recovery keeps those from the latest base on, ordered by (seq, ordinal): a later ordinal in the same commit
// supersedes an earlier one.
// mutation-checked: `!=` for `==` in the equal-seq ordinal comparisons (lines of the latest-base scan and of isBeforeLatestBase)  fail it.
func TestJsonlRecoveryKeepsACurrentOnlyDocumentFromItsLatestBaseOn(t *testing.T) {
	t.Run("a later ordinal of the same commit supersedes an earlier base", func(t *testing.T) {
		main := commitLine(1, `{"type":"conversation","value":{"id":1}}`) +
			commitLine(2, createFive) +
			commitLine(3, `{"type":"document.change","id":5,"ordinal":0},{"type":"document.change","id":5,"ordinal":1}`)
		got := recoveredSidecar(t, map[string]string{"main.jsonl": main, "doc-5.jsonl": baseRecord(2, 0, 1) + baseRecord(3, 0, 2) + baseRecord(3, 1, 3)})
		if want := baseRecord(3, 1, 3); got != want {
			t.Errorf("doc-5.jsonl = %q, want only the latest base %q", got, want)
		}
	})
}
