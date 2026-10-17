package delta

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Behaviour pins for the Tracker's emission and competing-adoption rules that the ported tracker.test.ts cases leave open
// (found by mutating tracker.go: each test below kills a mutant the existing suite let through).

func emittedOps(t *testing.T, tracker *Tracker, edit func(root *Object) error) []any {
	t.Helper()
	change := tracker.BeginChange()
	if err := edit(change.State()); err != nil {
		t.Fatal(err)
	}
	var ops []any
	if err := json.Unmarshal([]byte(mustJSON(t, mustPrepare(t, change).Ops())), &ops); err != nil {
		t.Fatal(err)
	}
	return ops
}

func trackedFixture() *Tracker {
	return Track(JsonObjectOf(
		"a", JsonObjectOf("b", 0.0, "c", 1.0),
		"arr", []any{JsonObjectOf("x", 1.0, "y", JsonObjectOf("k", 1.0))},
		"s", "ab", "n", 7.0))
}

// tracker.ts:1666: a batch of more than MAX_DELTA_OPERATIONS (4096) operations folds into one root replacement; exactly 4096 stays.
func TestTrackerFoldsOnlyBeyondMaxDeltaOperations(t *testing.T) {
	for _, tc := range []struct {
		keys    int
		wantOps int
		verb    string
	}{{4095, 4095, "s"}, {4096, 4096, "s"}, {4097, 1, "r"}} {
		t.Run(fmt.Sprint(tc.keys), func(t *testing.T) {
			tracker := Track(JsonObjectOf())
			ops := emittedOps(t, tracker, func(root *Object) error {
				for i := range tc.keys {
					if err := root.Set(fmt.Sprintf("k%05d", i), float64(i)); err != nil {
						return err
					}
				}
				return nil
			})
			if len(ops) != tc.wantOps || ops[0].([]any)[0] != tc.verb {
				t.Fatalf("%d keys: %d ops starting %v, want %d starting %s", tc.keys, len(ops), ops[0], tc.wantOps, tc.verb)
			}
		})
	}
}

// tracker.ts:1955: element overrides of an array stop emitting only once the batch already has more than MAX_DELTA_OPERATIONS operations, so 4097 scattered
// element writes (every third element, too sparse for a dense-region splice) still fold into one root replacement; exactly 4096 stay as sets.
func TestTrackerFoldsArrayElementOverridesOnlyBeyondMaxDeltaOperations(t *testing.T) {
	for _, tc := range []struct {
		sets    int
		wantOps int
		verb    string
	}{{4096, 4096, "s"}, {4097, 1, "r"}} {
		t.Run(fmt.Sprint(tc.sets), func(t *testing.T) {
			items := make([]any, 3*tc.sets)
			for i := range items {
				items[i] = float64(i)
			}
			ops := emittedOps(t, Track(JsonObjectOf("arr", items)), func(root *Object) error {
				arr := root.Get("arr").(*Array)
				for i := range tc.sets {
					if err := arr.Set(3*i, -1.0); err != nil {
						return err
					}
				}
				return nil
			})
			if len(ops) != tc.wantOps || ops[0].([]any)[0] != tc.verb {
				t.Fatalf("%d element writes: %d ops starting %v, want %d starting %s", tc.sets, len(ops), ops[0], tc.wantOps, tc.verb)
			}
		})
	}
}

// A draft with no writes prepares no operations and keeps the base revision's identity (materializePrepared on an empty batch).
func TestTrackerPreparesAnUnchangedDraftAsTheBaseRevision(t *testing.T) {
	tracker := trackedFixture()
	change := tracker.BeginChange()
	prepared := mustPrepare(t, change)
	if len(prepared.Ops()) != 0 || prepared.Value() != tracker.Value() {
		t.Fatalf("ops=%v sameValue=%v", prepared.Ops(), prepared.Value() == tracker.Value())
	}
}

// A write supersedes earlier edits inside the value it replaces or deletes: only the write or delete is emitted, at every depth
// and under array elements (collectEmissions skips keys in writes/deletes; modified/editsNearestArray ignore them).
func TestTrackerEmitsOnlyTheWriteOrDeleteOverModifiedDescendants(t *testing.T) {
	cases := []struct {
		name string
		edit func(root *Object) error
		want []any
	}{
		{"overwrite after modify", func(r *Object) error {
			return errors.Join(r.Object("a").Set("b", 5.0), r.Set("a", JsonObjectOf("z", 1.0)))
		}, []any{[]any{"s", []any{"a"}, map[string]any{"z": 1.0}}}},
		{"delete after modify", func(r *Object) error {
			err := r.Object("a").Set("b", 5.0)
			r.Delete("a")
			return err
		}, []any{[]any{"d", []any{"a"}}}},
		{"replace by scalar after modify", func(r *Object) error {
			return errors.Join(r.Object("a").Set("b", 9.0), r.Set("a", "gone"))
		}, []any{[]any{"s", []any{"a"}, "gone"}}},
		{"overwrite under an array element", func(r *Object) error {
			element := r.Array("arr").Get(0).(*Object)
			return errors.Join(element.Object("y").Set("k", 2.0), element.Set("y", JsonObjectOf("q", 1.0)))
		}, []any{[]any{"s", []any{"arr", 0.0, "y"}, map[string]any{"q": 1.0}}}},
		{"delete under an array element", func(r *Object) error {
			element := r.Array("arr").Get(0).(*Object)
			err := element.Object("y").Set("k", 2.0)
			element.Delete("y")
			return err
		}, []any{[]any{"d", []any{"arr", 0.0, "y"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := emittedOps(t, trackedFixture(), tc.edit)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ops = %v, want %v", got, tc.want)
			}
		})
	}
}

// A value whose type changes is a set, never a string append or trim: number to string and string to number both emit one "s".
func TestTrackerSetsAValueWhoseTypeChanged(t *testing.T) {
	got := emittedOps(t, trackedFixture(), func(r *Object) error { return r.Set("n", "ab") })
	if want := []any{[]any{"s", []any{"n"}, "ab"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("number to string: %v, want %v", got, want)
	}
	got = emittedOps(t, trackedFixture(), func(r *Object) error { return r.Set("s", 3.0) })
	if want := []any{[]any{"s", []any{"s"}, 3.0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("string to number: %v, want %v", got, want)
	}
}

// tracker.ts:254-276 (adopt) and 301-313 (#invalidate): after one change is adopted, a competing prepared change is stale (its base
// revision is behind, :261-264), a prepared change that was aborted stays aborted (:258), the winner is consumed (:257), and an open
// competitor can no longer prepare (assertWritable :184, :519-520 → assertReadable :516 throws "Cannot use a settled overlay"). Pi 1.1.0 probe: the same four
// messages. Prepare and abort deregister their context (clearContext, :2163), so these outcomes do not depend on #invalidate's
// open/prepared status guard; mutating that guard alone is equivalent.
func TestTrackerAdoptStalesOnlyOpenAndPreparedCompetitors(t *testing.T) {
	tracker := trackedFixture()
	winner := tracker.BeginChange()
	mustNoErr(t, winner.State().Set("n", 8.0))
	winnerPrepared := mustPrepare(t, winner)

	competingPrepared := tracker.BeginChange()
	mustNoErr(t, competingPrepared.State().Set("n", 9.0))
	staleCandidate := mustPrepare(t, competingPrepared)

	open := tracker.BeginChange()
	mustNoErr(t, open.State().Set("n", 10.0))

	aborted := tracker.BeginChange()
	mustNoErr(t, aborted.State().Set("n", 11.0))
	abortedPrepared := mustPrepare(t, aborted)
	aborted.Abort()

	mustNoErr(t, tracker.Adopt(winnerPrepared))

	requireErr(t, tracker.Adopt(staleCandidate), "Prepared change is stale")
	requireErr(t, tracker.Adopt(abortedPrepared), "Prepared change has been aborted")
	requireErr(t, tracker.Adopt(winnerPrepared), "Prepared change has already been used")
	_, err := open.Prepare()
	requireErr(t, err, "Cannot use a settled overlay")
}

func requireErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
