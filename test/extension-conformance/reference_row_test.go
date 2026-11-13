package extensionconformance

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// referenceRow carries what the reference row of a comparison test recorded to the rows compared with it.
//
// The reference row is a subtest like the others. `-run` can leave it out, and a reference row that failed before it recorded leaves nothing. A row that reads a
// plain variable the reference row sets then compares with the variable's zero value: it passes whatever its SDK does, or fails with a diff against nothing that
// does not say why. So every row asks get. The reference row records through set; a row that finds no recording records the reference itself, by the code the
// reference row runs, so a reference that `-run` left out still runs and one that fails fails the row that needs it with its own error. A recording that is
// empty fails at once, naming the reference.
type referenceRow[T any] struct {
	name     string // the reference SDK in a message: "Go", "Node (Pi)"
	recorded bool
	value    T
}

// set keeps what the reference row recorded.
func (r *referenceRow[T]) set(t testing.TB, recording T) {
	t.Helper()
	requireRecorded(t, r.name, recording)
	r.value, r.recorded = recording, true
}

// get returns the reference's recording. When no row has recorded it, it records it with record, which runs in the calling row.
func (r *referenceRow[T]) get(t testing.TB, record func() T) T {
	t.Helper()
	if !r.recorded {
		t.Logf("the %s reference row has not recorded (-run did not select it, or it failed before it recorded), so this row records the reference first", r.name)
		r.set(t, record())
	}
	return r.value
}

// requireRecorded fails t when a reference recorded nothing: no element, no entry, or a value no field of which was set.
func requireRecorded(t testing.TB, reference string, recording any) {
	t.Helper()
	value := reflect.ValueOf(recording)
	empty := !value.IsValid()
	if value.IsValid() {
		switch value.Kind() {
		case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
			empty = value.Len() == 0
		default:
			empty = value.IsZero()
		}
	}
	if empty {
		t.Fatalf("the %s reference recorded nothing (%#v); a comparison with it would pass whatever the candidate does or report a diff against an empty value, so the reference process or the code that records it produced no data", reference, recording)
	}
}

// recordingTB is a testing.TB that records what a referenceRow tells it and stops the caller at Fatalf the way a test does.
type recordingTB struct {
	testing.TB
	logs  []string
	fatal string
}

type stoppedByFatalf struct{}

func (*recordingTB) Helper() {}

func (r *recordingTB) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	panic(stoppedByFatalf{})
}

// runUntilFatalf runs body and reports whether it stopped at Fatalf.
func (r *recordingTB) runUntilFatalf(body func()) (stopped bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(stoppedByFatalf); !ok {
				panic(recovered)
			}
			stopped = true
		}
	}()
	body()
	return false
}

func TestReferenceRowRecordsWhenNoRowDid(t *testing.T) {
	reference := referenceRow[[]string]{name: "Node (Pi)"}
	tb := &recordingTB{TB: t}
	records := 0
	record := func() []string {
		records++
		return []string{"echo"}
	}
	for range 2 {
		if got := reference.get(tb, record); len(got) != 1 || got[0] != "echo" {
			t.Fatalf("get = %v, want the recorded value", got)
		}
	}
	if records != 1 {
		t.Errorf("the reference was recorded %d times, want once for every row that follows", records)
	}
	if len(tb.logs) != 1 || !strings.Contains(tb.logs[0], "Node (Pi) reference row has not recorded") {
		t.Errorf("the row that recorded the reference says so: logs = %q", tb.logs)
	}
}

func TestReferenceRowKeepsWhatTheReferenceRowRecorded(t *testing.T) {
	reference := referenceRow[map[string]int]{name: "Go"}
	tb := &recordingTB{TB: t}
	reference.set(tb, map[string]int{"calls": 3})
	got := reference.get(tb, func() map[string]int {
		t.Error("a row recorded the reference again after its own row had")
		return nil
	})
	if got["calls"] != 3 || len(tb.logs) != 0 {
		t.Errorf("get = %v with logs %q, want the recording the reference row set and no note", got, tb.logs)
	}
}

func TestReferenceRowFailsWhenTheReferenceRecordedNothing(t *testing.T) {
	type recording struct {
		Ping   bool
		Frames []string
	}
	for _, tc := range []struct {
		name string
		set  func(tb testing.TB)
		want string
	}{
		{"zero struct", func(tb testing.TB) { (&referenceRow[recording]{name: "Go"}).set(tb, recording{}) }, "the Go reference recorded nothing"},
		{"nil slice", func(tb testing.TB) { (&referenceRow[[]int]{name: "Node (Pi)"}).set(tb, nil) }, "the Node (Pi) reference recorded nothing"},
		{"empty slice", func(tb testing.TB) { (&referenceRow[[]int]{name: "Node (Pi)"}).set(tb, []int{}) }, "the Node (Pi) reference recorded nothing"},
		{"empty map", func(tb testing.TB) { (&referenceRow[map[string]int]{name: "Go"}).set(tb, map[string]int{}) }, "the Go reference recorded nothing"},
		{"empty string", func(tb testing.TB) { (&referenceRow[string]{name: "Go"}).set(tb, "") }, "the Go reference recorded nothing"},
		{"recorded by the row that needed it", func(tb testing.TB) {
			(&referenceRow[recording]{name: "Go"}).get(tb, func() recording { return recording{} })
		}, "the Go reference recorded nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := &recordingTB{TB: t}
			if !tb.runUntilFatalf(func() { tc.set(tb) }) {
				t.Fatal("an empty reference recording was accepted")
			}
			if !strings.Contains(tb.fatal, tc.want) {
				t.Errorf("failure = %q, want it to contain %q", tb.fatal, tc.want)
			}
		})
	}

	t.Run("one field set is a recording", func(t *testing.T) {
		tb := &recordingTB{TB: t}
		reference := referenceRow[recording]{name: "Go"}
		reference.set(tb, recording{Frames: []string{"state:started:"}})
		if got := reference.get(tb, func() recording { return recording{} }); got.Ping || len(got.Frames) != 1 {
			t.Errorf("get = %+v", got)
		}
	})
	t.Run("a failed reference is recorded again by the next row", func(t *testing.T) {
		tb := &recordingTB{TB: t}
		reference := referenceRow[[]int]{name: "Go"}
		if !tb.runUntilFatalf(func() { reference.set(tb, nil) }) {
			t.Fatal("an empty reference recording was accepted")
		}
		if got := reference.get(tb, func() []int { return []int{1} }); len(got) != 1 {
			t.Errorf("get = %v, want the recording of the row that asked", got)
		}
	})
}
