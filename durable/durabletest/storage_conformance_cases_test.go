package durabletest

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/testing/storage-conformance.ts createStorageConformance: the same 23 named cases, each Run returning the provider's failure when the provider fails.
// mutation-checked: dropping the reads and writes of StorageConformanceCase.Name, StorageConformanceCase.Run, StorageConformanceOptions.WithStorage fails it
// Pi: packages/durable/src/testing/runner.ts:7 (name)
// Pi: packages/durable/src/testing/runner.ts:23 (run)
// Pi: packages/durable/src/testing/runner.ts:16 (withStorage)
// Pi packages/durable/src/testing/storage-conformance.ts createStorageConformance (cases are packages/durable/src/testing/types.ts:20 StorageConformanceCase, each with a name and a run): the same 23 named cases, each Run returning the provider's failure when the provider fails.
func TestCreateStorageConformanceCasesMatchUpstream(t *testing.T) {
	want := []string{
		"commits mixed table writes atomically and rolls all of them back on failure",
		"continues an entry cursor below its last item after a newer commit",
		"copies stored document bases independently and rejects ambiguous sources",
		"detaches prototype-like JSON keys without changing object prototypes",
		"detaches retained writes and every returned record",
		"filters and pages conversations by durable owner edges",
		"indexes entries committed out of ID order",
		"indexes logical addresses and exact-scope scans independently",
		"indexes request IDs per conversation and replaces complete submission records",
		"keeps document lifecycle failures atomic and gives create-plus-retire an empty lifetime",
		"keeps indexed string identities lossless",
		"keeps one global record ID namespace and rejects exhausted ID minting",
		"paginates conversations by opaque cursor in ascending ID order",
		"reconstructs rewindable documents and preserves half-open incarnations",
		"rejects every operation after close",
		"replaces complete task records and pages filtered task scans",
		"reserves ID 1 for the immutable root conversation",
		"rolls back record tables and secondary indexes when a document command fails",
		"scans deep fork history newest-first through every ancestor cap",
		"scans tables in either ID order and continues a cursor in its order",
		"stores owners and scans waiting and completing tasks by status",
		"stores passive write submissions without input-only lifecycle states",
		"streams long document tails across root replacement deltas",
		"uses bases for version transitions and rejects historical reads of current-only documents",
	}
	providerFailure := errors.New("provider failed")
	cases := CreateStorageConformance(StorageConformanceOptions{
		Assertions:  CreateTestingAssertions(t),
		WithStorage: func(func(durable.Storage) error) error { return providerFailure },
	})
	if reflect.TypeOf(cases).Elem() != reflect.TypeFor[StorageConformanceCase]() {
		t.Fatalf("cases are %T, want StorageConformanceCase", cases)
	}
	var got []string
	for _, testCase := range cases {
		named := testCase
		got = append(got, named.Name)
		if err := named.Run(); !errors.Is(err, providerFailure) {
			t.Errorf("%s: Run returned %v, want the provider's error", named.Name, err)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("cases %q, want %q", got, want)
	}
}
