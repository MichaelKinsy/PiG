package storage_test

// Ports packages/durable/test/memory-storage.test.ts

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func TestMemoryStorageConformance(t *testing.T) {
	durabletest.RegisterStorageConformance(t, "MemoryStorage", func(use func(durable.Storage) error) error {
		return use(storage.NewMemoryStorage())
	})
}

// packages/durable/src/storage/memory.ts:209 PreparedMemoryCommit.writes. Upstream also expects mutating the exposed write to throw because it is frozen. Go has no frozen values, so the
// exposed writes are a detached copy instead; the case checks that mutating them cannot reach retained state.
// Pi source: packages/durable/src/storage/memory.ts
// mutation-checked: zeroing the results of MemoryStorage.PrepareCommit, PreparedMemoryCommit.Apply, PreparedMemoryCommit.Seq, PreparedMemoryCommit.Writes fails it
// Pi: packages/durable/src/storage/memory.ts:246 (prepareCommit)
// Pi: packages/durable/src/storage/memory.ts:210 (apply)
// Pi: packages/durable/src/storage/memory.ts:39 (seq)
// Pi: packages/durable/src/storage/memory.ts:208 (writes)
// packages/durable/src/storage/memory.ts:206-263: prepareCommit returns a PreparedMemoryCommit whose apply() commits the writes and returns the sequence.
func TestMemoryStorageDoesNotExposeRetainedStateThroughAPreparedCommit(t *testing.T) {
	ctx := context.Background()
	memory := storage.NewMemoryStorage()
	if _, err := memory.Commit(ctx, []durable.StorageWrite{
		durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
	}); err != nil {
		t.Fatal(err)
	}
	entryId := durable.IdFromNumber[durable.EntryId](2)
	prepared, err := memory.PrepareCommit([]durable.StorageWrite{durable.EntryWrite{Value: durable.EntryRecord{
		Id:             entryId,
		ConversationId: durable.ROOT_CONVERSATION_ID,
		Kind:           "test",
		Data:           map[string]any{"nested": []any{1}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Seq() != durable.SeqFromNumber(2) {
		t.Fatalf("prepared seq = %d, want the next commit sequence 2", prepared.Seq())
	}
	exposed, ok := prepared.Writes()[0].(durable.EntryWrite)
	if !ok {
		t.Fatal("Expected an entry write")
	}
	nested := exposed.Value.Data.(map[string]any)["nested"].([]any)
	nested[0] = 2
	exposed.Value.Data.(map[string]any)["nested"] = append(nested, 2)

	if prepared.Seq() != durable.SeqFromNumber(2) {
		t.Fatalf("prepared seq = %d, want 2 (memory.ts:250 defaults to the next sequence)", prepared.Seq())
	}
	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("apply = %d, want 2", seq)
	}
	if seq := prepared.Apply(); seq != durable.SeqFromNumber(2) {
		t.Fatalf("second apply = %d, want 2", seq)
	}
	if prepared.Seq() != durable.SeqFromNumber(2) {
		t.Fatalf("applied seq = %d, want 2", prepared.Seq())
	}
	found, err := memory.Entry(ctx, entryId)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"nested": []any{1}}; found == nil || !reflect.DeepEqual(found.Entry.Data, want) {
		t.Fatalf("entry data = %#v, want %#v", found, want)
	}
}

type failingTB struct {
	testing.TB
	failure string
}

type stopCase struct{}

func (f *failingTB) Fatalf(format string, args ...any) {
	f.failure = fmt.Sprintf(format, args...)
	panic(stopCase{})
}
func (f *failingTB) Helper() {}

// brokenMint makes the storage violate the numeric-ID namespace the first conformance case checks.
type brokenMint struct{ durable.Storage }

func (brokenMint) MintId() (int64, error) { return 1000, nil }

// CreateStorageConformance (testing/runner.ts createStorageConformance) returns runner-independent cases: every case
// passes against a conforming storage and the first fails against one that mints the wrong ID, so a broken storage is
// detected by the cases themselves.
// packages/durable/src/testing/types.ts:13-26: the options carry assertions and withStorage, and every case has a name and a run.
func TestCreateStorageConformanceDetectsABrokenStorage(t *testing.T) {
	cases := durabletest.CreateStorageConformance(durabletest.StorageConformanceOptions{
		Assertions:  durabletest.CreateTestingAssertions(t),
		WithStorage: func(use func(durable.Storage) error) error { return use(storage.NewMemoryStorage()) },
	})
	if len(cases) == 0 {
		t.Fatal("no conformance cases")
	}
	seen := map[string]bool{}
	for _, testCase := range cases {
		if seen[testCase.Name] {
			t.Fatalf("duplicate case name %q", testCase.Name)
		}
		seen[testCase.Name] = true
	}
	if err := cases[0].Run(); err != nil {
		t.Fatalf("%q failed against a conforming storage: %v", cases[0].Name, err)
	}

	recorder := &failingTB{TB: t}
	broken := durabletest.CreateStorageConformance(durabletest.StorageConformanceOptions{
		Assertions:  durabletest.CreateTestingAssertions(recorder),
		WithStorage: func(use func(durable.Storage) error) error { return use(brokenMint{storage.NewMemoryStorage()}) },
	})
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if _, ok := recovered.(stopCase); !ok {
					panic(recovered)
				}
			}
		}()
		_ = broken[0].Run()
	}()
	if recorder.failure == "" {
		t.Fatalf("case %q passed against a storage that mints the wrong ID", broken[0].Name)
	}
}
