package codingagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// refsOfEntries is the BranchRef of each entry, read from the entry itself: the oracle BranchRefs is compared with.
func refsOfEntries(t *testing.T, entries []SessionEntry) []BranchRef {
	t.Helper()
	var refs []BranchRef
	for _, entry := range entries {
		base := entry.Base()
		role := ""
		if base.Type == "message" {
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			var probe struct {
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
			}
			if err := json.Unmarshal(data, &probe); err != nil {
				t.Fatal(err)
			}
			role = probe.Message.Role
		}
		refs = append(refs, BranchRef{ID: base.ID, ParentID: base.ParentID, Type: base.Type, Role: role, Timestamp: base.Timestamp})
	}
	return refs
}

func sameRefs(a, b []BranchRef) bool {
	return slices.EqualFunc(a, b, func(x, y BranchRef) bool {
		return x.ID == y.ID && x.Type == y.Type && x.Role == y.Role && x.Timestamp == y.Timestamp &&
			(x.ParentID == nil) == (y.ParentID == nil) && (x.ParentID == nil || *x.ParentID == *y.ParentID)
	})
}

// requireBranchRefsAreGetBranch fails when BranchRefs(fromID...) is not GetBranch(fromID...) as references, and returns the path's length.
func requireBranchRefsAreGetBranch(t *testing.T, session *Session, fromID ...string) int {
	t.Helper()
	want := refsOfEntries(t, session.GetBranch(fromID...))
	got := session.BranchRefs(fromID...)
	if !sameRefs(got, want) {
		t.Fatalf("BranchRefs(%q) = %+v\nwant (GetBranch) %+v", fromID, got, want)
	}
	return len(want)
}

// reopened is the session of live's file opened anew, so its message entries are not resident.
func reopened(t *testing.T, live *Session) *Session {
	t.Helper()
	session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// lazyIDs are the ids of the entries of session that are not resident, in append order.
func lazyIDs(session *Session) []string {
	var ids []string
	for _, entry := range (HandleReads{Session: session}).GetEntries() {
		if lazy, ok := entry.(*sessionentry.LazyEntry); ok {
			ids = append(ids, lazy.Base().ID)
		}
	}
	return ids
}

// BranchRefs is GetBranch's path as references: the same entries in the same order for the current leaf and for any fromID, in an empty
// session, a linear one, a forked tree, after a leaf move, with compaction, custom and other entries, and with an id that two entries carry.
func TestBranchRefsAreGetBranch(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		if n := requireBranchRefsAreGetBranch(t, session); n != 0 {
			t.Fatalf("an empty session has a branch of %d", n)
		}
		requireBranchRefsAreGetBranch(t, session, "nothing")
	})
	t.Run("linear", func(t *testing.T) {
		dir := t.TempDir()
		session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		for i := range 10 {
			if _, err := session.AppendMessage(userMsg(fmt.Sprintf("m%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		if n := requireBranchRefsAreGetBranch(t, session); n != 10 {
			t.Fatalf("branch of %d, want 10", n)
		}
	})
	t.Run("forked tree", func(t *testing.T) {
		live, ids := randomStoreSession(t, 1, 300)
		for _, session := range []*Session{live, reopened(t, live)} {
			requireBranchRefsAreGetBranch(t, session)
			for i := 0; i < len(ids); i += 5 {
				requireBranchRefsAreGetBranch(t, session, ids[i])
			}
			requireBranchRefsAreGetBranch(t, session, "")
			requireBranchRefsAreGetBranch(t, session, "no such entry")
		}
	})
	t.Run("after a leaf move", func(t *testing.T) {
		live, ids := randomStoreSession(t, 2, 200)
		session := reopened(t, live)
		before := session.BranchRefs()
		for _, at := range []int{len(ids) / 3, 1, len(ids) - 2} {
			if err := session.Branch(ids[at]); err != nil {
				t.Fatal(err)
			}
			requireBranchRefsAreGetBranch(t, session)
		}
		if err := session.SetLeafID(nil); err != nil {
			t.Fatal(err)
		}
		if n := requireBranchRefsAreGetBranch(t, session); n != 0 {
			t.Fatalf("a leaf set to nothing has a branch of %d", n)
		}
		if sameRefs(before, session.BranchRefs()) {
			t.Fatal("the leaf moves did not change the branch")
		}
	})
	t.Run("compaction and custom entries", func(t *testing.T) {
		live, _ := randomStoreSession(t, 3, 120)
		if _, err := live.AppendCustomEntry("note", map[string]any{"k": 1}); err != nil {
			t.Fatal(err)
		}
		appendLater(t, live)
		if _, err := live.AppendCustomMessageEntry("memo", "text", true, nil); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, ref := range live.BranchRefs() {
			seen[ref.Type+"/"+ref.Role] = true
		}
		for _, want := range []string{"compaction/", "custom/", "custom_message/", "message/user", "message/assistant"} {
			if !seen[want] {
				t.Fatalf("the branch has no %s entry (has %v)", want, seen)
			}
		}
		for _, session := range []*Session{live, reopened(t, live)} {
			requireBranchRefsAreGetBranch(t, session)
		}
	})
	t.Run("a repeated id", func(t *testing.T) {
		// Two records carry the id b; the entries that name b as parent follow the newest, as GetBranch does.
		lines := []string{
			`{"type":"session","version":3,"id":"s1","timestamp":"2025-01-01T00:00:00.000Z","cwd":"/tmp"}`,
			`{"type":"message","id":"a","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"user","content":"a","timestamp":1}}`,
			`{"type":"message","id":"b","parentId":"a","timestamp":"2025-01-01T00:00:02.000Z","message":{"role":"user","content":"b1","timestamp":2}}`,
			`{"type":"message","id":"c","parentId":"b","timestamp":"2025-01-01T00:00:03.000Z","message":{"role":"user","content":"c","timestamp":3}}`,
			`{"type":"message","id":"b","parentId":"a","timestamp":"2025-01-01T00:00:04.000Z","message":{"role":"user","content":"b2","timestamp":4}}`,
			`{"type":"message","id":"d","parentId":"b","timestamp":"2025-01-01T00:00:05.000Z","message":{"role":"user","content":"d","timestamp":5}}`,
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "dup.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		session, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, from := range []string{"", "a", "b", "c", "d"} {
			if from == "" {
				requireBranchRefsAreGetBranch(t, session)
				continue
			}
			requireBranchRefsAreGetBranch(t, session, from)
		}
	})
}

// BranchRefs reads no entry body: it makes no read of the records, and it answers after the records can no longer be read.
func TestBranchRefsReadNoBody(t *testing.T) {
	snapshotModes(t, func(t *testing.T) {
		live, _ := randomStoreSession(t, 4, 300)
		session := reopened(t, live)
		lazy := lazyIDs(session)
		if len(lazy) == 0 || session.snapshot == nil {
			t.Fatalf("%d entries are not resident, snapshot %v", len(lazy), session.snapshot)
		}
		if err := session.snapshot.file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(live.Path()); err != nil {
			t.Fatal(err)
		}
		reads := session.EntryReads()
		refs := session.BranchRefs()
		if len(refs) == 0 || session.EntryReads() != reads {
			t.Fatalf("BranchRefs returned %d refs and made %d reads", len(refs), session.EntryReads()-reads)
		}
		var changed *sessionentry.RecordChangedError
		if entries, err := session.ReadEntries(context.Background(), lazy[:1]); !errors.As(err, &changed) || entries != nil {
			t.Fatalf("ReadEntries of a record that cannot be read: %v, %v", entries, err)
		}
	})
}

// BranchRefs is the cheap half of a page read: it costs the walk, where GetBranch reads and decodes the branch's records.
func TestBranchRefsCostNoReads(t *testing.T) {
	session := sessionOf(t, 1000, 4096)
	reads := session.EntryReads()
	session.BranchRefs()
	if session.EntryReads() != reads {
		t.Fatalf("BranchRefs made %d reads", session.EntryReads()-reads)
	}
	session.GetBranch()
	if session.EntryReads()-reads < 500 {
		t.Fatalf("GetBranch made %d reads: the session has too few entries that are not resident for this comparison", session.EntryReads()-reads)
	}
}

// ReadEntries returns the entries it was asked for in the order it was asked, an id given twice twice, each as GetEntry returns it, and reads
// only those entries' records: a page of a long branch costs the page.
func TestReadEntriesKeepsTheOrderAndReadsOnlyTheGivenEntries(t *testing.T) {
	live, _ := randomStoreSession(t, 6, 600)
	session := reopened(t, live)
	lazy := lazyIDs(session)
	if len(lazy) < 50 {
		t.Fatalf("only %d entries are not resident", len(lazy))
	}
	rng := rand.New(rand.NewPCG(1, 2))
	ids := make([]string, 0, 12)
	for range 10 {
		ids = append(ids, lazy[rng.IntN(len(lazy))])
	}
	ids = append(ids, ids[2], ids[0]) // duplicates
	reads := session.EntryReads()
	got, err := session.ReadEntries(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if delta := session.EntryReads() - reads; delta != int64(len(ids)) {
		t.Fatalf("a read of %d ids made %d reads", len(ids), delta)
	}
	if len(got) != len(ids) {
		t.Fatalf("%d entries for %d ids", len(got), len(ids))
	}
	for k, id := range ids {
		want, ok := live.GetEntry(id)
		if !ok {
			t.Fatalf("no entry %s", id)
		}
		a, _ := json.Marshal(got[k])
		b, _ := json.Marshal(want)
		if got[k].Base().ID != id || !bytes.Equal(a, b) {
			t.Fatalf("entry %d of the result is %s, want %s", k, a, b)
		}
		if _, ok := got[k].(*sessionentry.LazyEntry); ok {
			t.Fatalf("entry %d is a handle", k)
		}
	}

	// A page of the first five entries of the branch (the compacted prefix of a long branch) costs five reads, where the branch costs its length.
	onBranch := map[string]bool{}
	var page []string
	for _, ref := range session.BranchRefs() {
		onBranch[ref.ID] = true
	}
	for _, id := range lazy {
		if onBranch[id] && len(page) < 5 {
			page = append(page, id)
		}
	}
	reads = session.EntryReads()
	if entries, err := session.ReadEntries(context.Background(), page); err != nil || len(entries) != 5 || session.EntryReads()-reads != 5 {
		t.Fatalf("a page of 5: %d entries, %v, %d reads", len(entries), err, session.EntryReads()-reads)
	}
	if entries, err := session.ReadEntries(context.Background(), nil); err != nil || len(entries) != 0 {
		t.Fatalf("no ids: %v, %v", entries, err)
	}
}

// An id no entry has is an error that names it, and no entries come with it: the entries before it in the list are not returned, and none is read.
func TestReadEntriesOfAnUnknownIDIsAnErrorAndNoPartialResult(t *testing.T) {
	live, _ := randomStoreSession(t, 7, 200)
	session := reopened(t, live)
	lazy := lazyIDs(session)
	reads := session.EntryReads()
	got, err := session.ReadEntries(context.Background(), []string{lazy[0], "no-such-entry", lazy[1]})
	if err == nil || got != nil || !strings.Contains(err.Error(), "no-such-entry") {
		t.Fatalf("got %v, %v", got, err)
	}
	var changed *sessionentry.RecordChangedError
	if errors.As(err, &changed) || session.EntryReads() != reads {
		t.Fatalf("an unknown id is no changed record (%v) and reads nothing (%d reads)", changed, session.EntryReads()-reads)
	}
}

// A record that changed under the session is the *sessionentry.RecordChangedError of the entry, with no entries, and never a panic.
func TestReadEntriesOfAChangedRecordIsTheRecordChangedError(t *testing.T) {
	snapshotModes(t, func(t *testing.T) {
		for _, change := range []string{"rewritten", "truncated", "removed"} {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 3, 200)
				session := reopened(t, live)
				var target *sessionentry.LazyEntry
				for _, entry := range (HandleReads{Session: session}).GetEntries() {
					if lazy, ok := entry.(*sessionentry.LazyEntry); ok {
						target = lazy
						break
					}
				}
				if target == nil || session.snapshot == nil {
					t.Fatal("no lazy entry or no snapshot")
				}
				id := target.Base().ID
				snapshot := session.snapshot
				data, err := io.ReadAll(io.NewSectionReader(snapshot.file, 0, snapshot.size))
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
					_, err = snapshot.file.WriteAt([]byte(strings.Replace(string(raw), `"role":"`, `"role":"X`, 1)[:len(raw)]), int64(at))
				case "truncated":
					err = snapshot.file.Truncate(int64(at + len(raw)/2))
				case "removed":
					err = snapshot.file.Close()
				}
				if err != nil {
					t.Fatal(err)
				}
				got, err := session.ReadEntries(context.Background(), []string{id})
				var changed *sessionentry.RecordChangedError
				if !errors.As(err, &changed) || got != nil || change != "removed" && changed.ID != id {
					t.Fatalf("ReadEntries: %v, %v; want the RecordChangedError of %s", got, err, id)
				}
			})
		}
	})
}

// A ctx that is done before the read returns its error and reads nothing.
func TestReadEntriesOfACancelledContextReadsNothing(t *testing.T) {
	live, _ := randomStoreSession(t, 8, 200)
	session := reopened(t, live)
	lazy := lazyIDs(session)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads := session.EntryReads()
	if got, err := session.ReadEntries(ctx, lazy[:3]); !errors.Is(err, context.Canceled) || got != nil || session.EntryReads() != reads {
		t.Fatalf("got %v, %v after %d reads", got, err, session.EntryReads()-reads)
	}
	// The same ctx is checked before an unknown id is looked for.
	if _, err := session.ReadEntries(ctx, []string{"nothing"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// Appends during reads make no read fail and change no entry ReadEntries returns for an id (run with -race).
func TestReadEntriesDuringAppends(t *testing.T) {
	live, _ := randomStoreSession(t, 9, 300)
	session := reopened(t, live)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := session.AppendMessage(userMsg(fmt.Sprintf("later %d", i))); err != nil {
				t.Error(err)
				return
			}
		}
	})
	defer func() { close(stop); wg.Wait() }()
	for i := range 200 {
		refs := session.BranchRefs()
		var ids []string
		for _, ref := range refs {
			if ref.Type == "message" && len(ids) < 8 {
				ids = append(ids, ref.ID)
			}
		}
		got, err := session.ReadEntries(context.Background(), ids)
		if err != nil || len(got) != len(ids) {
			t.Fatalf("iteration %d: %d entries for %d ids, %v", i, len(got), len(ids), err)
		}
		for k, id := range ids {
			if got[k].Base().ID != id {
				t.Fatalf("iteration %d: entry %d is %s, want %s", i, k, got[k].Base().ID, id)
			}
		}
	}
}

// A session that could make no snapshot of its file holds its entries, so ReadEntries returns them whole after the file is replaced or gone,
// where an unreadable record would have been an error.
func TestReadEntriesOfASessionWithoutASnapshot(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		live, _ := randomStoreSession(t, 10, 120)
		session := reopened(t, live)
		if session.snapshot != nil {
			t.Skip("the session made a snapshot")
		}
		var ids []string
		for _, ref := range session.BranchRefs() {
			ids = append(ids, ref.ID)
		}
		want := session.GetBranch()
		if err := os.Remove(live.Path()); err != nil {
			t.Fatal(err)
		}
		got, err := session.ReadEntries(context.Background(), ids)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%d entries, %v; want %d", len(got), err, len(want))
		}
		for k := range got {
			a, _ := json.Marshal(got[k])
			b, _ := json.Marshal(want[k])
			if _, raw := got[k].(RawEntry); raw && got[k].Base().Type == "message" || !bytes.Equal(a, b) {
				t.Fatalf("entry %d is %s, want %s", k, a, b)
			}
		}
	})
}

// sessionOf is a file-backed session of n message entries of size bytes, opened anew so its records are read back on demand.
func sessionOf(tb testing.TB, n, size int) *Session {
	tb.Helper()
	dir := tb.TempDir()
	live, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		tb.Fatal(err)
	}
	body := strings.Repeat("x", size)
	for i := range n {
		var id string
		if i%2 == 0 {
			id, err = live.AppendMessage(userMsg(fmt.Sprintf("%d %s", i, body)))
		} else {
			id, err = live.AppendMessage(mkAssistantMsg(fmt.Sprintf("%d %s", i, body)))
		}
		if err != nil || id == "" {
			tb.Fatal(id, err)
		}
	}
	session, err := NewSessionManagerWithDir(tb.TempDir(), dir).Open(live.Path())
	if err != nil {
		tb.Fatal(err)
	}
	return session
}

// BenchmarkBranchRefsOf10000 is the point of BranchRefs: the ids, order and types of a branch of 10,000 entries without reading its records.
func BenchmarkBranchRefsOf10000(b *testing.B) {
	session := sessionOf(b, 10000, 1024)
	b.Run("BranchRefs", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if len(session.BranchRefs()) != 10000 {
				b.Fatal("short branch")
			}
		}
	})
	b.Run("GetBranch", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if len(session.GetBranch()) != 10000 {
				b.Fatal("short branch")
			}
		}
	})
}

// BenchmarkReadEntries50Of10000 is 50 entries of a branch of 10,000: one batch against 50 GetEntry calls, for the 50 entries of one page (the
// next 50 on the branch) and for 50 entries spread over the branch (every seventh).
func BenchmarkReadEntries50Of10000(b *testing.B) {
	session := sessionOf(b, 10000, 1024)
	refs := session.BranchRefs()
	for _, shape := range []struct {
		name string
		at   func(k int) int
	}{{"Page", func(k int) int { return 100 + k }}, {"Spread", func(k int) int { return 100 + k*7 }}} {
		ids := make([]string, 50)
		for k := range ids {
			ids[k] = refs[shape.at(k)].ID
		}
		b.Run(shape.name+"/ReadEntries", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got, err := session.ReadEntries(context.Background(), ids); err != nil || len(got) != 50 {
					b.Fatal(len(got), err)
				}
			}
		})
		b.Run(shape.name+"/GetEntry", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, id := range ids {
					if _, ok := session.GetEntry(id); !ok {
						b.Fatal("no entry")
					}
				}
			}
		})
	}
}
