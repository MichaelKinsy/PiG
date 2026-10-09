package storage_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// recordingAssertions records the failures of the assertions a conformance case makes instead of stopping the test.
type recordingAssertions struct {
	testing.TB
	failure string
}

func (r *recordingAssertions) Helper() {}
func (r *recordingAssertions) Fatalf(format string, args ...any) {
	r.failure = fmt.Sprintf(format, args...)
	panic(stopCheck{})
}

type stopCheck struct{}

// testing/runner.ts StorageConformanceAssertions: ok, toBe, toEqual, toMatchObject, toBeGreaterThan and rejects.toThrow, compared by JSON form; each failing check reports its Vitest matcher name.
// packages/durable/src/testing/types.ts:4-11 declares these six assertions (ok, strictEqual, deepEqual, partialDeepEqual, greaterThan, rejects).
func TestStorageConformanceAssertionsPassOnEqualAndReportTheFailingMatcher(t *testing.T) {
	check := func(call func(durabletest.StorageConformanceAssertions)) (failure string) {
		recorder := &recordingAssertions{TB: t}
		defer func() {
			failure = recorder.failure
			if recovered := recover(); recovered != nil {
				if _, ok := recovered.(stopCheck); !ok {
					panic(recovered)
				}
			}
		}()
		call(durabletest.CreateTestingAssertions(recorder))
		return ""
	}
	for name, call := range map[string]func(durabletest.StorageConformanceAssertions){
		"ok":   func(a durabletest.StorageConformanceAssertions) { a.Ok(true, "fine") },
		"toBe": func(a durabletest.StorageConformanceAssertions) { a.StrictEqual(int64(3), 3.0) },
		"toEqual": func(a durabletest.StorageConformanceAssertions) {
			a.DeepEqual(map[string]any{"a": []int{1}}, map[string]any{"a": []any{1.0}})
		},
		"toMatch": func(a durabletest.StorageConformanceAssertions) {
			a.PartialDeepEqual(map[string]any{"a": 1, "b": 2}, map[string]any{"a": 1})
		},
		"greater": func(a durabletest.StorageConformanceAssertions) { a.GreaterThan(2, 1) },
		"throws":  func(a durabletest.StorageConformanceAssertions) { a.Rejects(fmt.Errorf("boom: details"), "boom") },
	} {
		if failure := check(call); failure != "" {
			t.Errorf("%s: a passing check failed: %s", name, failure)
		}
	}
	for want, call := range map[string]func(durabletest.StorageConformanceAssertions){
		"truthy":  func(a durabletest.StorageConformanceAssertions) { a.Ok(false, "nope") },
		"toBe":    func(a durabletest.StorageConformanceAssertions) { a.StrictEqual(1, 2) },
		"toEqual": func(a durabletest.StorageConformanceAssertions) { a.DeepEqual([]int{1}, []int{2}) },
		"toMatchObject": func(a durabletest.StorageConformanceAssertions) {
			a.PartialDeepEqual(map[string]any{"a": 1}, map[string]any{"a": 2})
		},
		"toBeGreaterThan":  func(a durabletest.StorageConformanceAssertions) { a.GreaterThan(1, 1) },
		"call succeeded":   func(a durabletest.StorageConformanceAssertions) { a.Rejects(nil, "x") },
		"does not include": func(a durabletest.StorageConformanceAssertions) { a.Rejects(fmt.Errorf("other"), "boom") },
	} {
		if failure := check(call); !strings.Contains(failure, want) {
			t.Errorf("want a failure mentioning %q, got %q", want, failure)
		}
	}
}

// RegisterStorageConformance registers one subtest per conformance case under the given name, each running against storage the provider supplies; a case that fails fails the registering test.
// packages/durable/src/testing/types.ts:13-20: StorageConformanceOptions.assertions and withStorage.
func TestRegisterStorageConformanceRegistersEveryCaseAndRunsThemAgainstTheProvidedStorage(t *testing.T) {
	provided := 0
	var provider durabletest.StorageConformanceProvider = func(use func(durable.Storage) error) error {
		provided++
		return use(storage.NewMemoryStorage())
	}
	if !t.Run("registered", func(t *testing.T) { durabletest.RegisterStorageConformance(t, "MemoryStorage", provider) }) {
		t.Fatal("a conforming storage failed a registered case")
	}
	cases := durabletest.CreateStorageConformance(durabletest.StorageConformanceOptions{Assertions: durabletest.CreateTestingAssertions(t), WithStorage: provider})
	if provided == 0 || provided < len(cases) {
		t.Fatalf("the provider supplied %d storages for %d cases", provided, len(cases))
	}
}
