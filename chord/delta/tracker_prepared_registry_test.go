package delta

import "testing"

// Pi measured (chord 1.1.0): a prepared change leaves the tracker's registry (materializePrepared → clearContext), so a
// later adoption does not mark it stale. Aborting it afterwards therefore takes effect, and adopting it reports the
// abort; a preparation that is not aborted reports that it is stale.
func TestAPreparedChangeOutlivesAnotherAdoptionUntilItIsAdopted(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*Tracker) (*Prepared, func())
		abort   bool
		want    string
	}{
		{name: "aborted through the preparation", abort: true, want: "Prepared change has been aborted", prepare: func(tracker *Tracker) (*Prepared, func()) {
			prepared := preparedChange(t, tracker, 2.0)
			return prepared, prepared.Abort
		}},
		{name: "aborted through the change", abort: true, want: "Prepared change has been aborted", prepare: func(tracker *Tracker) (*Prepared, func()) {
			change := tracker.BeginChange()
			if err := change.State().Set("a", 2.0); err != nil {
				t.Fatal(err)
			}
			prepared, err := change.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			return prepared, change.Abort
		}},
		{name: "not aborted", want: "Prepared change is stale", prepare: func(tracker *Tracker) (*Prepared, func()) {
			return preparedChange(t, tracker, 2.0), nil
		}},
		{name: "replacement aborted", abort: true, want: "Prepared change has been aborted", prepare: func(tracker *Tracker) (*Prepared, func()) {
			prepared, err := tracker.PrepareReplaceRoot(JsonObjectOf("a", 5.0))
			if err != nil {
				t.Fatal(err)
			}
			return prepared, prepared.Abort
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tracker := Track(JsonObjectOf("a", 1.0))
			prepared, abort := test.prepare(tracker)
			if err := tracker.Adopt(preparedChange(t, tracker, 3.0)); err != nil {
				t.Fatal(err)
			}
			if test.abort {
				abort()
			}
			err := tracker.Adopt(prepared)
			if err == nil || err.Error() != test.want {
				t.Fatalf("Adopt = %v, want %q", err, test.want)
			}
		})
	}
}

func preparedChange(t *testing.T, tracker *Tracker, value float64) *Prepared {
	t.Helper()
	change := tracker.BeginChange()
	if err := change.State().Set("a", value); err != nil {
		t.Fatal(err)
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}
