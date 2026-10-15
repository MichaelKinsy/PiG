package store_test

// pi: packages/durable/src/storage/memory.ts prepareCommitAt validation (:743-813) and storage/sqlite/storage.ts (:694-728)

import (
	"slices"
	"testing"
)

// The store refuses each invalid batch of badScript with the message pi-durable's storage throws (TestInvalidCommitsMatchPi compares the same script with pi-durable main when CONTRACT_PI_SOURCE is set; without it this
// test pins the messages read from the pinned Pi source): an ID written twice (memory.ts:744), two record types for one ID (:749), a document with two content commands (:770), an unknown document (:792),
// a retired document, a delta whose version needs a base (:799), a second current incarnation of an address (:813), the ID of an existing document (:743-747), a document retired twice (:780) and copies
// whose source changed in the batch or cannot be read (sqlite/storage.ts:728, memory.ts:325). Refused batches leave the store as it was: the next valid commit gets the next sequence.
// mutation-checked: each validation (known-ID claims, two-type check, content-command count, base requirement, incarnation count, retired and copy checks) mutated to accept or to refuse the wrong batch fails an entry.
func TestInvalidCommitsAreRefusedWithPisMessages(t *testing.T) {
	script := badScript()
	h, _, results := runScript(t, script)
	h.close()
	want := []string{
		"seq=1", "seq=2", "seq=3", "seq=4", "seq=5", "seq=6",
		"error=ID 20 is written more than once",
		"error=ID 21 is written as two record types",
		"error=Document 30 has more than one content command",
		"error=Unknown document: 999",
		"error=Document 10 is retired",
		"error=Document 14 version transition requires a base",
		"error=Document address already has a current incarnation",
		"error=ID 14 already belongs to document",
		"error=Document 14 is retired more than once",
		"error=Document copy 32 source is changed in the copy batch",
		"error=Document copy 33 was rejected",
		"error=Document copy 34 was rejected",
		"seq=7",
	}
	if !slices.Equal(results, want) {
		for i := range max(len(results), len(want)) {
			var got, exp string
			if i < len(results) {
				got = results[i]
			}
			if i < len(want) {
				exp = want[i]
			}
			if got != exp {
				t.Errorf("commit %d: got %q, want %q", i, got, exp)
			}
		}
	}
}
