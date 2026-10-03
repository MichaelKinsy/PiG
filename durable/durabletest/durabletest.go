// Package durabletest holds runner-independent conformance cases and benchmark workloads for durable.Storage
// implementations.
package durabletest

// Ports packages/durable/src/testing/types.ts
// Ports packages/durable/src/testing/assertions.ts
// Ports packages/durable/src/testing/runner.ts
// Ports packages/durable/src/testing/index.ts

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// StorageConformanceAssertions are the checks a conformance case makes. A failing check ends the case.
//
// Values compare by their JSON form, as upstream's Vitest matchers compare plain data: absent optional fields equal
// missing ones, and every Go number type compares by value.
type StorageConformanceAssertions interface {
	Ok(value bool, message string)
	StrictEqual(actual, expected any)
	DeepEqual(actual, expected any)
	PartialDeepEqual(actual, expected any)
	GreaterThan(actual, expected float64)
	// Rejects checks that a call failed with an error whose message includes messageIncludes.
	Rejects(err error, messageIncludes string)
}

// StorageConformanceProvider supplies fresh storage to use and returns use's error. It must call use exactly once.
type StorageConformanceProvider func(use func(storage durable.Storage) error) error

// StorageConformanceOptions configures CreateStorageConformance.
type StorageConformanceOptions struct {
	Assertions  StorageConformanceAssertions
	WithStorage StorageConformanceProvider
}

// StorageConformanceCase is one named conformance case.
type StorageConformanceCase struct {
	Name string
	Run  func() error
}

// CreateTestingAssertions adapts a Go test to StorageConformanceAssertions; a failure stops the test.
func CreateTestingAssertions(tb testing.TB) StorageConformanceAssertions {
	return testingAssertions{tb: tb}
}

type testingAssertions struct{ tb testing.TB }

func (assertions testingAssertions) Ok(value bool, message string) {
	assertions.tb.Helper()
	if !value {
		assertions.tb.Fatalf("expected a truthy value: %s", message)
	}
}

func (assertions testingAssertions) StrictEqual(actual, expected any) {
	assertions.tb.Helper()
	if !reflect.DeepEqual(Normalize(actual), Normalize(expected)) {
		assertions.tb.Fatalf("toBe: got %s, want %s", Describe(actual), Describe(expected))
	}
}

func (assertions testingAssertions) DeepEqual(actual, expected any) {
	assertions.tb.Helper()
	if !reflect.DeepEqual(Normalize(actual), Normalize(expected)) {
		assertions.tb.Fatalf("toEqual: got %s, want %s", Describe(actual), Describe(expected))
	}
}

func (assertions testingAssertions) PartialDeepEqual(actual, expected any) {
	assertions.tb.Helper()
	if !MatchObject(Normalize(actual), Normalize(expected)) {
		assertions.tb.Fatalf("toMatchObject: got %s, want %s", Describe(actual), Describe(expected))
	}
}

func (assertions testingAssertions) GreaterThan(actual, expected float64) {
	assertions.tb.Helper()
	if !(actual > expected) {
		assertions.tb.Fatalf("toBeGreaterThan: got %v, want > %v", actual, expected)
	}
}

func (assertions testingAssertions) Rejects(err error, messageIncludes string) {
	assertions.tb.Helper()
	if err == nil {
		assertions.tb.Fatalf("rejects.toThrow: call succeeded, want an error including %q", messageIncludes)
	}
	if !strings.Contains(err.Error(), messageIncludes) {
		assertions.tb.Fatalf("rejects.toThrow: error %q does not include %q", err.Error(), messageIncludes)
	}
}

// Normalize returns the JSON form of value with JavaScript string semantics: nil, bool, float64, string, []any, or
// map[string]any. A value without a JSON form is returned unchanged.
func Normalize(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value
	}
	return decoded
}

// Describe renders value as JSON for failure messages.
func Describe(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(bytes.TrimSpace(encoded))
}

// MatchObject reports whether actual contains expected: objects match recursively on expected's keys, arrays match
// element-wise with equal length, and scalars match exactly.
func MatchObject(actual, expected any) bool {
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range want {
			item, present := got[key]
			if !present || !MatchObject(item, value) {
				return false
			}
		}
		return true
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			return false
		}
		for index := range want {
			if !MatchObject(got[index], want[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(actual, expected)
	}
}

// RegisterStorageConformance runs every conformance case as a subtest of a test named name.
func RegisterStorageConformance(t *testing.T, name string, withStorage StorageConformanceProvider) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		names := caseNames()
		for index, caseName := range names {
			t.Run(caseName, func(t *testing.T) {
				cases := CreateStorageConformance(StorageConformanceOptions{
					Assertions:  CreateTestingAssertions(t),
					WithStorage: withStorage,
				})
				if err := cases[index].Run(); err != nil {
					t.Fatal(err)
				}
			})
		}
	})
}

func caseNames() []string {
	cases := CreateStorageConformance(StorageConformanceOptions{})
	names := make([]string, len(cases))
	for index, testCase := range cases {
		names[index] = testCase.Name
	}
	return names
}
