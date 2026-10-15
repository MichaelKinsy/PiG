package codingagent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// withoutSnapshots makes every session of the test read its records back from its session file.
func withoutSnapshots(t *testing.T) {
	t.Helper()
	clone, create := cloneSessionSnapshot, newSessionSnapshot
	cloneSessionSnapshot = func(*os.File, string) (*sessionSnapshot, bool) { return nil, false }
	newSessionSnapshot = func(string) (*sessionSnapshot, error) { return nil, errors.New("no snapshot") }
	t.Cleanup(func() { cloneSessionSnapshot, newSessionSnapshot = clone, create })
}

// snapshotModes runs fn with a copied snapshot and, where the file system clones, a cloned one.
func snapshotModes(t *testing.T, fn func(t *testing.T)) {
	t.Run("copy", func(t *testing.T) {
		clone := cloneSessionSnapshot
		cloneSessionSnapshot = func(*os.File, string) (*sessionSnapshot, bool) { return nil, false }
		t.Cleanup(func() { cloneSessionSnapshot = clone })
		fn(t)
	})
	t.Run("clone", func(t *testing.T) {
		probe := filepath.Join(t.TempDir(), "probe")
		if err := os.WriteFile(probe, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(probe)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		snapshot, ok := cloneSessionSnapshot(file, probe)
		if !ok {
			t.Skip("the file system of the temporary directory does not clone files")
		}
		snapshot.close()
		fn(t)
	})
}

// changeSessionFile changes the session file at path behind a session's back, as another program may.
func changeSessionFile(t *testing.T, path, change string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	switch change {
	case "rewritten":
		// Every record keeps its length and place; its bytes change.
		err = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), `"role":"`, `"role":"X`)[:len(data)]), 0o600)
	case "truncated":
		err = os.WriteFile(path, data[:len(data)/3], 0o600)
	case "replaced":
		next := path + ".next"
		if err = os.WriteFile(next, []byte("{}\n"), 0o600); err == nil {
			err = os.Rename(next, path)
		}
	case "removed":
		err = os.Remove(path)
	}
	if err != nil {
		t.Fatal(err)
	}
}

var sessionFileChanges = []string{"rewritten", "truncated", "replaced", "removed"}

// sessionReads is what a session's reads return: every entry decoded, the records of the whole-history handle read, and the branch.
type sessionReads struct {
	entries, branch []byte
	raws            []json.RawMessage
}

func readSession(t *testing.T, session *Session) sessionReads {
	t.Helper()
	entries, err := json.Marshal(session.GetEntries())
	if err != nil {
		t.Fatal(err)
	}
	branch, err := json.Marshal(session.GetBranch())
	if err != nil {
		t.Fatal(err)
	}
	raws, err := sessionentry.ReadRaws(HandleReads{Session: session}.GetEntries())
	if err != nil {
		t.Fatal(err)
	}
	return sessionReads{entries: entries, branch: branch, raws: raws}
}

func sameReads(t *testing.T, got, want sessionReads) {
	t.Helper()
	if !bytes.Equal(got.entries, want.entries) || !bytes.Equal(got.branch, want.branch) {
		t.Fatal("the entries or the branch changed with the session file")
	}
	if !slices.EqualFunc(got.raws, want.raws, func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
		t.Fatal("the records changed with the session file")
	}
}

// Pi reads a session file into memory once (session-manager.ts setSessionFile, loadEntriesFromFile), so a change another program makes to
// the file afterwards reaches none of its reads. A resumed session reads the records it does not keep from its snapshot, so no change to the
// file (bytes rewritten in place, the file cut short, replaced by a rename or removed) reaches its reads either.
func TestResumedSessionReadsAsLoadedAfterItsFileChanges(t *testing.T) {
	snapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 3, 200)
				session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				if session.backing == "" || len(HandleReads{Session: session}.GetEntries()) == 0 {
					t.Fatal("the session did not take the indexed load")
				}
				want := readSession(t, session)
				changeSessionFile(t, session.Path(), change)
				sameReads(t, readSession(t, session), want)
			})
		}
	})
}

// The entries a session writes after it loads, and every entry of a session it created, are read from its snapshot too: an entry that a
// later compaction evicts reads as written after the file changes.
func TestWrittenEntriesReadAsWrittenAfterTheFileChanges(t *testing.T) {
	snapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 5, 120)
				session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range []*Session{live, session} {
					for i := range 6 {
						if _, err := s.AppendMessage(userMsg(strings.Repeat("later ", i+1))); err != nil {
							t.Fatal(err)
						}
						if _, err := s.AppendMessage(mkAssistantMsg(strings.Repeat("answer ", i+1))); err != nil {
							t.Fatal(err)
						}
					}
					kept, err := s.AppendMessage(userMsg("kept"))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := s.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
						t.Fatal(err)
					}
				}
				wantLive, want := readSession(t, live), readSession(t, session)
				changeSessionFile(t, live.Path(), change)
				sameReads(t, readSession(t, live), wantLive)
				sameReads(t, readSession(t, session), want)
			})
		}
	})
}

// The snapshot leaves no file behind: none beside the session file and none in the temporary directory.
func TestSessionSnapshotLeavesNoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	live, _ := randomStoreSession(t, 3, 60)
	dir := filepath.Dir(live.Path())
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	if session.snapshot == nil || live.snapshot == nil {
		t.Fatal("a session has no snapshot")
	}
	for _, d := range []string{dir, tmp} {
		after, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if d == dir && len(after) != len(before) || d == tmp && len(after) != 0 {
			t.Fatalf("%s holds %d files after the load, %d before", d, len(after), len(before))
		}
	}
}
