package jsonl_test

// pi: packages/durable/src/storage/jsonl/storage.ts (isReclaimFileName :104 and its removal :552; main operation validation :126; sidecar recovery :718-733)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storage.ts:552 recovery removes the leftovers of an interrupted reclamation: only `doc-<n>.jsonl.reclaim` and `task-<n>.jsonl.reclaim` files, whose number has no leading zero.
// mutation-checked: removing files that are not reclaim files, or not removing reclaim files, fails it.
func TestJsonlRecoveryRemovesOnlyReclaimLeftovers(t *testing.T) {
	files := map[string]string{
		"main.jsonl":              `{"format":1,"type":"commit","seq":1,"writes":[{"type":"conversation","value":{"id":1}}]}` + "\n",
		"doc-5.jsonl.reclaim":     "leftover",
		"task-0.jsonl.reclaim":    "leftover",
		"doc-05.jsonl.reclaim":    "keep: leading zero",
		"doc-5.jsonl.reclaimx":    "keep: other suffix",
		"other-5.jsonl.reclaim":   "keep: other prefix",
		"doc-5.jsonl.reclaim.bak": "keep: other suffix",
	}
	directory := writeDirectory(t, files)
	open(t, directory)
	for name := range files {
		_, err := os.Stat(filepath.Join(directory, name))
		removed := os.IsNotExist(err)
		wantRemoved := name == "doc-5.jsonl.reclaim" || name == "task-0.jsonl.reclaim"
		if removed != wantRemoved {
			t.Errorf("%s removed = %v, want %v", name, removed, wantRemoved)
		}
	}
}

// storage.ts:718-733 recovery of a sidecar file: from the first record no commit confirmed, the tail is truncated; a confirmed record after an unconfirmed one is corruption, also when
// the unconfirmed record is the first line of the file.
// mutation-checked: `> 0` for `>= 0` on the unconfirmed offset, and a later unconfirmed record replacing the first offset, fail it.
func TestJsonlRecoveryTruncatesFromTheFirstUnconfirmedSidecarRecord(t *testing.T) {
	unconfirmed := func(seq int) string {
		return `{"format":1,"type":"record","seq":` + string(rune('0'+seq)) + `,"ordinal":0,"payload":{"type":"document","id":5,"content":{"version":1,"kind":"base","value":{"a":1}}}}` + "\n"
	}
	t.Run("two unconfirmed records are both truncated", func(t *testing.T) {
		directory := writeDirectory(t, map[string]string{"main.jsonl": `{"format":1,"type":"commit","seq":1,"writes":[{"type":"conversation","value":{"id":1}}]}` + "\n", "doc-5.jsonl": unconfirmed(8) + unconfirmed(9)})
		open(t, directory)
		got, err := os.ReadFile(filepath.Join(directory, "doc-5.jsonl"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("doc-5.jsonl after recovery = %q, want it truncated to nothing", got)
		}
	})
	t.Run("a confirmed record after an unconfirmed first line is corruption", func(t *testing.T) {
		directory := writeDirectory(t, map[string]string{"main.jsonl": strings.Join(strings.SplitAfter(rewriteMainLog, "\n")[:2], ""), "doc-5.jsonl": unconfirmed(1) + supersededBase})
		if _, err := openJsonl(directory); err == nil || err.Error() != "Confirmed record follows an unconfirmed tail in doc-5.jsonl" {
			t.Fatalf("open error = %v, want the confirmed-after-unconfirmed corruption", err)
		}
	})
}

// storage.ts:120-130 a main-log write must be an object with a string `type`, and a conversation, entry or submission write must carry a record with a safe integer `id`: anything else is
// `Invalid write in main.jsonl line N` (or `Invalid <type> write in ...`).
// mutation-checked: `&&` for `||` in either validity check lets a malformed write through.
func TestJsonlRecoveryRejectsMalformedMainWrites(t *testing.T) {
	for _, tc := range []struct{ name, write, want string }{
		{"not an object", `5`, "Invalid write in main.jsonl line 1"},
		{"object without a type", `{"value":{"id":1}}`, "Invalid write in main.jsonl line 1"},
		{"type is not a string", `{"type":7,"value":{"id":1}}`, "Invalid write in main.jsonl line 1"},
		{"record is not an object", `{"type":"conversation","value":5}`, "Invalid conversation write in main.jsonl line 1"},
		{"id is not an integer", `{"type":"entry","value":{"id":"x"}}`, "Invalid entry write in main.jsonl line 1"},
		{"id is fractional", `{"type":"submission","value":{"id":1.5}}`, "Invalid submission write in main.jsonl line 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := writeDirectory(t, map[string]string{"main.jsonl": `{"format":1,"type":"commit","seq":1,"writes":[` + tc.write + `]}` + "\n"})
			if _, err := openJsonl(directory); err == nil || err.Error() != tc.want {
				t.Errorf("open error = %v, want %q", err, tc.want)
			}
		})
	}
}
