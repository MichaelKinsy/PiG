package session_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// Golden outputs execute the pinned upstream session/fork.ts export directly.
// They cover branch selection, scalar filtering/resequencing, Map duplicate
// semantics, incomplete snapshots, and validation failures independently of
// the repository fork implementation.
func TestCreateForkSnapshotMatchesUpstream(t *testing.T) {
	data, err := os.ReadFile("testdata/source-fork-snapshots.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string
		Source struct {
			Entries         []session.Entry
			ScalarValues    []session.StoredValue[any]
			EntriesComplete *bool
		}
		Options session.ForkOptions
		Want    session.ForkDestinationSnapshot
		Error   string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := session.CreateForkSnapshot(session.ForkSourceSnapshot{
				Entries:           tc.Source.Entries,
				ScalarValues:      tc.Source.ScalarValues,
				EntriesIncomplete: tc.Source.EntriesComplete != nil && !*tc.Source.EntriesComplete,
			}, tc.Options)
			if tc.Error != "" {
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("error = %v, want %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(tc.Want.ScalarValues) == 0 {
				tc.Want.ScalarValues = nil
			}
			assertJSON(t, got, tc.Want)
		})
	}
}
