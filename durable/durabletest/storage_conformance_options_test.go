package durabletest

// pi: packages/durable/src/testing/types.ts

// pi: packages/durable/src/testing/storage-conformance.ts

// pi: packages/durable/src/testing/runner.ts

// pi: packages/durable/src/testing/assertions.ts

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// recordingAssertions counts the checks a case routes through StorageConformanceOptions.Assertions.
type recordingAssertions struct {
	StorageConformanceAssertions
	checks int
}

func (r *recordingAssertions) StrictEqual(actual, expected any) {
	r.checks++
	r.StorageConformanceAssertions.StrictEqual(actual, expected)
}
func (r *recordingAssertions) DeepEqual(actual, expected any) {
	r.checks++
	r.StorageConformanceAssertions.DeepEqual(actual, expected)
}
func (r *recordingAssertions) PartialDeepEqual(actual, expected any) {
	r.checks++
	r.StorageConformanceAssertions.PartialDeepEqual(actual, expected)
}
func (r *recordingAssertions) Rejects(err error, messageIncludes string) {
	r.checks++
	r.StorageConformanceAssertions.Rejects(err, messageIncludes)
}

// StorageConformanceOptions (packages/durable/src/testing/types.ts) carries the assertions every case checks through and withStorage, which each case calls exactly once for fresh storage and whose result the case returns.
// packages/durable/src/testing/types.ts:13-26: the options carry assertions and withStorage, and every case has a name and a run.
func TestStorageConformanceOptionsSupplyAssertionsAndFreshStoragePerCase(t *testing.T) {
	assertions := &recordingAssertions{StorageConformanceAssertions: CreateTestingAssertions(t)}
	var provided []durable.Storage
	cases := CreateStorageConformance(StorageConformanceOptions{
		Assertions: assertions,
		WithStorage: func(use func(durable.Storage) error) error {
			store := storage.NewMemoryStorage()
			provided = append(provided, store)
			return use(store)
		},
	})
	if len(cases) == 0 {
		t.Fatal("no conformance cases")
	}
	for _, testCase := range cases[:2] {
		before := len(provided)
		if err := testCase.Run(); err != nil {
			t.Fatalf("%s: %v", testCase.Name, err)
		}
		if len(provided) != before+1 {
			t.Fatalf("%s: WithStorage called %d times, want once", testCase.Name, len(provided)-before)
		}
	}
	if provided[0] == provided[1] || assertions.checks == 0 {
		t.Fatalf("storage reused between cases (%v) or no check reached Assertions (%d)", provided[0] == provided[1], assertions.checks)
	}

	provideErr := errors.New("storage unavailable")
	failing := CreateStorageConformance(StorageConformanceOptions{
		Assertions:  assertions,
		WithStorage: func(func(durable.Storage) error) error { return provideErr },
	})
	if err := failing[0].Run(); !errors.Is(err, provideErr) {
		t.Fatalf("case error = %v, want the WithStorage error", err)
	}
}

// storage-conformance.ts withStorage "must call use exactly once": RegisterStorageConformance registers every conformance case as its own
// subtest, and each case asks the StorageConformanceProvider for fresh storage once, so a provider sees exactly one use per case.
func TestRegisterStorageConformanceUsesTheProviderOncePerCase(t *testing.T) {
	calls := 0
	var provider StorageConformanceProvider = func(use func(durable.Storage) error) error {
		calls++
		return use(storage.NewMemoryStorage())
	}
	t.Run("registered", func(t *testing.T) {
		RegisterStorageConformance(t, "counted", provider)
	})
	if cases := len(caseNames()); cases == 0 || calls != cases {
		t.Fatalf("provider used %d times for %d cases", calls, cases)
	}
}
