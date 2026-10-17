package codingagent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// checkHandleReads compares the lazy whole-history reads of s with its decoded ones: the same entries in the same order, the same base
// members without reading a record, the same records and JSON, and after decoding the same members; it returns how many were lazy.
func checkHandleReads(t *testing.T, what string, s *Session, ids []string) int {
	t.Helper()
	lazy := 0
	compare := func(read string, got, want []SessionEntry) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s %s: %d entries, want %d", what, read, len(got), len(want))
		}
		for i := range got {
			if _, ok := got[i].(*sessionentry.LazyEntry); ok {
				lazy++
			}
			if !reflect.DeepEqual(got[i].Base(), want[i].Base()) {
				t.Fatalf("%s %s: entry %d base %+v, want %+v", what, read, i, got[i].Base(), want[i].Base())
			}
		}
		raws, err := sessionentry.ReadRaws(got)
		if err != nil {
			t.Fatalf("%s %s: %v", what, read, err)
		}
		for i := range raws {
			if string(raws[i]) != string(want[i].Raw()) {
				t.Fatalf("%s %s: entry %d record %s, want %s", what, read, i, raws[i], want[i].Raw())
			}
		}
		gotJSON, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("%s %s: JSON differs", what, read)
		}
		materialized, err := sessionentry.MaterializeAll(got)
		if err != nil {
			t.Fatal(err)
		}
		sameEntries(t, what+" "+read, materialized, want)
	}
	reads := HandleReads{Session: s}
	compare("GetEntries", reads.GetEntries(), s.GetEntries())
	for _, id := range append(ids, "missing") {
		compare("GetBranch "+id, reads.GetBranch(id), s.GetBranch(id))
	}
	compare("GetBranch", reads.GetBranch(), s.GetBranch())
	for _, id := range append(ids, "missing") {
		got, gotOK := reads.GetEntry(id)
		want, wantOK := s.GetEntry(id)
		if gotOK != wantOK {
			t.Fatalf("%s GetEntry %s: found %v, want %v", what, id, gotOK, wantOK)
		}
		if gotOK {
			compare("GetEntry "+id, []SessionEntry{got}, []SessionEntry{want})
		}
	}
	tree := reads.GetTree()
	var flat func(nodes []*SessionTreeNode) []SessionEntry
	flat = func(nodes []*SessionTreeNode) []SessionEntry {
		var out []SessionEntry
		for _, n := range nodes {
			out = append(append(out, n.Entry), flat(n.Children)...)
		}
		return out
	}
	want := s.GetTree()
	compare("GetTree", flat(tree), flat(want))
	materializeTree(tree)
	if got, want := fmt.Sprint(treeShape(tree)), fmt.Sprint(treeShape(want)); got != want {
		t.Fatalf("%s: lazy tree differs", what)
	}
	return lazy
}

// The whole-history reads this module's serve paths use (HandleReads: RPC get_entries and get_tree, extension session reads, branch summary
// collection) return the entries, records and JSON the decoded reads return, with each message entry that is not resident lazy: in a
// resumed session, in the live session that wrote it, and in a session without a file whose history is compressed.
func TestHandleReadsMatchDecodedReads(t *testing.T) {
	for seed := range uint64(8) {
		live, ids := randomStoreSession(t, seed, 300)
		reopened, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		if lazy := checkHandleReads(t, fmt.Sprintf("seed %d reopened", seed), reopened, ids); lazy == 0 {
			t.Fatalf("seed %d: no read was lazy", seed)
		}
		checkHandleReads(t, fmt.Sprintf("seed %d live", seed), live, ids)
	}
	memory := NewSession("no-file", t.TempDir())
	var ids []string
	var kept string
	for i := range 120 {
		id, err := memory.AppendMessage(mkUserMsg(fmt.Sprintf("question %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if i%10 == 8 {
			kept = id
		}
		if id, err = memory.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if i%10 == 9 {
			if _, err := memory.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if lazy := checkHandleReads(t, "no file", memory, ids); lazy == 0 {
		t.Fatal("no read of the session without a file was lazy")
	}
}

// A lazy entry carries the timestamp its record holds, whatever its form: Pi's toISOString form, another form Date.parse reads, and one it
// does not.
func TestLazyEntriesKeepTheirTimestamps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{`{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`}
	stamps := []string{"2026-01-01T00:00:01.000Z", "2026-01-01T00:00:02Z", "yesterday", "2026-01-01T00:00:03.5Z", "+002026-01-01T00:00:04.000Z", ""}
	parent := "null"
	var ids []string
	for i, stamp := range stamps {
		id := fmt.Sprintf("m%d", i)
		lines = append(lines, fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":"user","content":"hi %d","timestamp":1}}`, id, parent, stamp, i))
		parent, ids = fmt.Sprintf("%q", id), append(ids, id)
	}
	lines = append(lines, `{"type":"compaction","id":"c","parentId":"m5","timestamp":"2026-01-01T00:01:00.000Z","summary":"s","firstKeptEntryId":"m5","tokensBefore":1}`)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if lazy := checkHandleReads(t, "timestamps", session, append(ids, "c")); lazy < len(stamps)-1 {
		t.Fatalf("%d lazy entries, want at least %d", lazy, len(stamps)-1)
	}
}

// A session that got no snapshot reads its records back from the session file. A lazy entry whose record changed there under the session
// (rewritten in place, cut short or removed) is an error of every read that returns its record or its message, naming the entry: no read
// returns other bytes or the base members alone.
func TestLazyEntryOfAChangedRecordIsAnError(t *testing.T) {
	withoutSnapshots(t)
	for _, change := range []string{"rewritten", "truncated", "removed"} {
		t.Run(change, func(t *testing.T) {
			live, _ := randomStoreSession(t, 3, 200)
			session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
			if err != nil {
				t.Fatal(err)
			}
			entries := HandleReads{Session: session}.GetEntries()
			var target *sessionentry.LazyEntry
			for _, entry := range entries {
				if lazy, ok := entry.(*sessionentry.LazyEntry); ok {
					target = lazy
					break
				}
			}
			if target == nil {
				t.Fatal("no lazy entry")
			}
			data, err := os.ReadFile(session.Path())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := sessionentry.ReadRaw(target)
			if err != nil {
				t.Fatal(err)
			}
			at := strings.Index(string(data), string(raw))
			switch change {
			case "rewritten":
				changed := strings.Replace(string(raw), `"role":"`, `"role":"X`, 1)[:len(raw)]
				data = append(append(data[:at:at], changed...), data[at+len(raw):]...)
				err = os.WriteFile(session.Path(), data, 0o600)
			case "truncated":
				err = os.WriteFile(session.Path(), data[:at+len(raw)/2], 0o600)
			case "removed":
				err = os.Remove(session.Path())
			}
			if err != nil {
				t.Fatal(err)
			}
			var changed *sessionentry.RecordChangedError
			if _, err := sessionentry.ReadRaws(entries); !errors.As(err, &changed) || change != "removed" && changed.ID != target.Base().ID {
				t.Fatalf("ReadRaws: %v, want the RecordChangedError of %s", err, target.Base().ID)
			}
			if _, _, err := sessionentry.AsMessage(target); !errors.As(err, &changed) {
				t.Fatalf("AsMessage: %v, want a RecordChangedError", err)
			}
			if _, err := json.Marshal(entries); !errors.As(err, &changed) {
				t.Fatalf("Marshal: %v, want a RecordChangedError", err)
			}
		})
	}
}

// Whole-history reads on several goroutines while the session appends and compacts each read a consistent log: every lazy entry they return
// decodes to the entry its id names.
func TestHandleReadsWhileAppending(t *testing.T) {
	live, _ := randomStoreSession(t, 5, 200)
	session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				entries := HandleReads{Session: session}.GetEntries()
				materialized, err := sessionentry.MaterializeAll(entries)
				if err != nil {
					errs <- err
					return
				}
				for i, entry := range materialized {
					if entry.Base().ID != entries[i].Base().ID {
						errs <- fmt.Errorf("entry %d is %s, want %s", i, entry.Base().ID, entries[i].Base().ID)
						return
					}
				}
			}
		})
	}
	var kept string
	for i := range 60 {
		id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("more %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if i%10 == 3 {
			kept = id
		}
		if i%10 == 9 {
			if _, err := session.AppendCompaction("summary", kept, 100, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(done)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// checkExtensionReads compares every extension session read of s, whose history is lazy, with the same read of the file loaded wholly
// decoded.
func checkExtensionReads(t *testing.T, what string, s *Session, ids []string) {
	t.Helper()
	resident := residentCopy(t, s.Path())
	lazy, err := newSessionReadLog(s)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := newSessionReadLogOf(resident, resident.GetEntries())
	if err != nil {
		t.Fatal(err)
	}
	probes := []*string{nil, new("missing")}
	for i := range ids {
		probes = append(probes, &ids[i])
	}
	for _, method := range []string{"getEntries", "getLeafEntry", "getEntry", "getLabel", "getChildren", "getBranch", "getTree"} {
		for _, id := range probes {
			got, gotErr := answerSessionRead(lazy, method, id, id, id)
			want, wantErr := answerSessionRead(whole, method, id, id, id)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(gotJSON, wantJSON) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("%s %s(%v): got %.300s, want %.300s", what, method, id, gotJSON, wantJSON)
			}
		}
	}
}

// The extension session reads of a session whose history is lazy answer what the same file loaded wholly decoded answers, siblings whose
// timestamps run against append order included (getTree sorts children by timestamp).
func TestExtensionSessionReadsOfLazyEntriesMatchTheDecodedLog(t *testing.T) {
	for seed := range uint64(3) {
		live, ids := randomStoreSession(t, 200+seed, 200)
		session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		checkExtensionReads(t, fmt.Sprintf("seed %d", seed), session, ids[len(ids)-20:])
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	message := func(id, parent, stamp string) string {
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":"user","content":"%s","timestamp":1}}`, id, parent, stamp, id)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`,
		message("m0", "null", "2026-01-01T00:00:01.000Z"),
		message("m1", `"m0"`, "2026-01-01T00:00:03.000Z"),
		message("m4", `"m1"`, "2026-01-01T00:00:05.000Z"),
		message("m2", `"m0"`, "2026-01-01T00:00:02.000Z"),
		message("m3", `"m2"`, "2026-01-01T00:00:04.000Z"),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := NewSessionManagerWithDir(dir, dir).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if lazy := checkHandleReads(t, "siblings", session, nil); lazy == 0 {
		t.Fatal("no entry of the off-branch sibling is lazy")
	}
	checkExtensionReads(t, "siblings", session, []string{"m0", "m1", "m2", "m3", "m4"})
}
