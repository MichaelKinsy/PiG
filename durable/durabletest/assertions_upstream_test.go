package durabletest

import (
	"errors"
	"math"
	"testing"
)

// failedCheck is what the recording testing.TB panics with so a failing check stops its caller, as testing.TB.Fatalf stops a test.
type failedCheck struct{ message string }

type recordingTB struct{ testing.TB }

func (recordingTB) Helper() {}
func (recordingTB) Fatalf(format string, args ...any) {
	panic(failedCheck{message: format})
}

func fails(check func(StorageConformanceAssertions)) (failed bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(failedCheck); !ok {
				panic(recovered)
			}
			failed = true
		}
	}()
	check(CreateTestingAssertions(recordingTB{}))
	return false
}

// Pi durable/src/testing/assertions.ts createExpectAssertions maps ok/strictEqual/deepEqual/partialDeepEqual/greaterThan and rejects.toThrow to Vitest matchers: each passes on the matching value and ends the case on a mismatch. Values compare by JSON form, so an int equals the same float64 and an absent optional field equals a missing one.
// Pi: packages/durable/src/testing/types.ts:7 (deepEqual).
// Pi: packages/durable/src/testing/types.ts:9 (greaterThan).
// Pi: packages/durable/src/testing/types.ts:5 (ok).
// Pi: packages/durable/src/testing/types.ts:8 (partialDeepEqual).
// Pi: packages/durable/src/testing/types.ts:10 (rejects).
// Pi: packages/durable/src/testing/types.ts:6 (strictEqual).
func TestStorageConformanceAssertionsPassAndFail(t *testing.T) {
	type record struct {
		A int    `json:"a"`
		B string `json:"b,omitempty"`
	}
	for _, tc := range []struct {
		name  string
		check func(StorageConformanceAssertions)
		fail  bool
	}{
		{"ok true", func(a StorageConformanceAssertions) { a.Ok(true, "m") }, false},
		{"ok false", func(a StorageConformanceAssertions) { a.Ok(false, "m") }, true},
		// assertions.ts ok is expect(value, message).toBeTruthy(): JavaScript truthiness over unknown, and the message is optional.
		{"ok without a message", func(a StorageConformanceAssertions) { a.Ok(1) }, false},
		{"ok non-empty string", func(a StorageConformanceAssertions) { a.Ok("x") }, false},
		{"ok empty slice is truthy", func(a StorageConformanceAssertions) { a.Ok([]string{}) }, false},
		{"ok map is truthy", func(a StorageConformanceAssertions) { a.Ok(map[string]any{}) }, false},
		{"ok nil", func(a StorageConformanceAssertions) { a.Ok(nil) }, true},
		{"ok zero", func(a StorageConformanceAssertions) { a.Ok(0, "m") }, true},
		{"ok NaN", func(a StorageConformanceAssertions) { a.Ok(math.NaN(), "m") }, true},
		{"ok empty string", func(a StorageConformanceAssertions) { a.Ok("", "m") }, true},
		{"ok nil pointer", func(a StorageConformanceAssertions) { var p *int; a.Ok(p, "m") }, true},
		{"ok nil slice", func(a StorageConformanceAssertions) { var s []string; a.Ok(s, "m") }, true},
		// assertions.ts:20-22 ok(value, message) is toBeTruthy() over any value, with an optional message.
		{"ok without a message", func(a StorageConformanceAssertions) { a.Ok(true) }, false},
		{"ok nil", func(a StorageConformanceAssertions) { a.Ok(nil) }, true},
		{"ok zero", func(a StorageConformanceAssertions) { a.Ok(0) }, true},
		{"ok zero float", func(a StorageConformanceAssertions) { a.Ok(float64(0)) }, true},
		{"ok NaN", func(a StorageConformanceAssertions) { a.Ok(math.NaN()) }, true},
		{"ok empty string", func(a StorageConformanceAssertions) { a.Ok("") }, true},
		{"ok nil pointer", func(a StorageConformanceAssertions) { a.Ok((*int)(nil)) }, true},
		{"ok nonzero", func(a StorageConformanceAssertions) { a.Ok(-1) }, false},
		{"ok text", func(a StorageConformanceAssertions) { a.Ok("0") }, false},
		{"ok empty map", func(a StorageConformanceAssertions) { a.Ok(map[string]int{}) }, false},
		{"ok empty slice", func(a StorageConformanceAssertions) { a.Ok([]int{}) }, false},
		{"ok struct", func(a StorageConformanceAssertions) { a.Ok(struct{}{}) }, false},
		{"strictEqual number types", func(a StorageConformanceAssertions) { a.StrictEqual(3, float64(3)) }, false},
		{"strictEqual differs", func(a StorageConformanceAssertions) { a.StrictEqual("x", "y") }, true},
		{"deepEqual omitted field", func(a StorageConformanceAssertions) { a.DeepEqual(record{A: 1}, map[string]any{"a": 1}) }, false},
		{"deepEqual extra field", func(a StorageConformanceAssertions) { a.DeepEqual(record{A: 1, B: "x"}, map[string]any{"a": 1}) }, true},
		{"partialDeepEqual subset", func(a StorageConformanceAssertions) { a.PartialDeepEqual(record{A: 1, B: "x"}, map[string]any{"a": 1}) }, false},
		{"partialDeepEqual missing", func(a StorageConformanceAssertions) { a.PartialDeepEqual(record{A: 1}, map[string]any{"b": "x"}) }, true},
		{"greaterThan", func(a StorageConformanceAssertions) { a.GreaterThan(2, 1) }, false},
		{"greaterThan equal", func(a StorageConformanceAssertions) { a.GreaterThan(1, 1) }, true},
		{"rejects match", func(a StorageConformanceAssertions) { a.Rejects(errors.New("boom happened"), "boom") }, false},
		{"rejects other message", func(a StorageConformanceAssertions) { a.Rejects(errors.New("other"), "boom") }, true},
		{"rejects success", func(a StorageConformanceAssertions) { a.Rejects(nil, "boom") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fails(tc.check); got != tc.fail {
				t.Fatalf("check failed = %v, want %v", got, tc.fail)
			}
		})
	}
}
