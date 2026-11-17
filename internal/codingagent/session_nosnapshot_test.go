package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// noSnapshotModes runs fn in each way a session can end up with no private snapshot of its file: none can be made at all, or the write
// of the records into it fails.
func noSnapshotModes(t *testing.T, fn func(t *testing.T)) {
	t.Run("unavailable", func(t *testing.T) {
		withoutSnapshots(t)
		fn(t)
	})
	t.Run("write fails", func(t *testing.T) {
		clone, create := cloneSessionSnapshot, newSessionSnapshot
		cloneSessionSnapshot = func(*os.File, string) (*sessionSnapshot, bool) { return nil, false }
		// A snapshot whose file takes no write: the copy of a load and the first write of a created session both fail part-way.
		newSessionSnapshot = func(path string) (*sessionSnapshot, error) {
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			return &sessionSnapshot{file: file}, nil
		}
		t.Cleanup(func() { cloneSessionSnapshot, newSessionSnapshot = clone, create })
		fn(t)
	})
}

// failSnapshotWrites makes every later write to the session's snapshot fail (a negative offset is no place to write) and leaves what it
// holds readable, as a full disk does.
func failSnapshotWrites(t *testing.T, session *Session) {
	t.Helper()
	if session.snapshot == nil {
		t.Fatal("the session has no snapshot")
	}
	session.snapshot.mu.Lock()
	session.snapshot.size = -1
	session.snapshot.mu.Unlock()
}

// appendLater appends six exchanges and a compaction to session, and returns the ids it appended.
func appendLater(t *testing.T, session *Session) []string {
	t.Helper()
	var ids []string
	add := func(id string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for i := range 6 {
		add(session.AppendMessage(userMsg(strings.Repeat("later ", i+1))))
		add(session.AppendMessage(mkAssistantMsg(strings.Repeat("answer ", i+1))))
	}
	kept, err := session.AppendMessage(userMsg("kept"))
	add(kept, err)
	add(session.AppendCompaction("summary", kept, 1000, nil, false, nil))
	return ids
}

// everyRead is what each read of a session that the program can make returns, by the name of the read.
func everyRead(session *Session) map[string]string {
	out := map[string]string{}
	put := func(name string, value any) {
		data, err := json.Marshal(value)
		if err != nil {
			out[name] = "error: " + err.Error()
			return
		}
		out[name] = string(data)
	}
	put("GetEntries", session.GetEntries())
	put("GetBranch", session.GetBranch())
	var each []SessionEntry
	for _, entry := range session.GetEntries() {
		if got, ok := session.GetEntry(entry.Base().ID); ok {
			each = append(each, got)
		}
	}
	put("GetEntry", each)
	put("GetTree", session.GetTree())
	put("GetContextBranch", session.GetContextBranch())
	put("BuildContext", session.BuildContext(nil))
	put("BuildSessionContext", session.BuildSessionContext())
	put("BuildContextEntries", session.BuildContextEntries())
	put("GetBranchSystemMessages", session.GetBranchSystemMessages())
	put("GetMessageEntriesWithRole", session.GetMessageEntriesWithRole("user"))
	pages, _, _ := session.RawEntriesPage(0, 1<<30)
	put("RawEntriesPage", pages)
	raws, err := sessionentry.ReadRaws(HandleReads{Session: session}.GetEntries())
	if err != nil {
		out["HandleReads.ReadRaws"] = "error: " + err.Error()
	} else {
		put("HandleReads.ReadRaws", raws)
	}
	// RPC get_tree reads the tree through the handle reads and decodes every node.
	func() {
		defer func() {
			if r := recover(); r != nil {
				out["HandleReads.GetTree"] = fmt.Sprint("panic: ", r)
			}
		}()
		nodes := HandleReads{Session: session}.GetTree()
		materializeTree(nodes)
		put("HandleReads.GetTree", nodes)
	}()
	// The JSONL export writes the entries of the branch.
	if file, err := os.CreateTemp("", "export-*.jsonl"); err != nil {
		out["ExportSessionToJsonl"] = "error: " + err.Error()
	} else {
		_ = file.Close()
		defer func() { _ = os.Remove(file.Name()) }()
		if _, err := ExportSessionToJsonl(session, file.Name(), nil); err != nil {
			out["ExportSessionToJsonl"] = "error: " + err.Error()
		} else if data, err := os.ReadFile(file.Name()); err != nil {
			out["ExportSessionToJsonl"] = "error: " + err.Error()
		} else {
			// The first line is the export's header, which holds the time of the export.
			_, rest, _ := strings.Cut(string(data), "\n")
			out["ExportSessionToJsonl"] = rest
		}
	}
	return out
}

func sameEveryRead(t *testing.T, got, want map[string]string) {
	t.Helper()
	var wrong []string
	for name, w := range want {
		if got[name] != w {
			wrong = append(wrong, name)
		}
	}
	slices.Sort(wrong)
	if len(wrong) > 0 {
		t.Fatalf("after the session file changed, %d of %d reads differ from the reads of the loaded session (Pi keeps every entry in memory): %s", len(wrong), len(want), strings.Join(wrong, ", "))
	}
}

// entryJSONs is each entry of the session, in append order, as JSON.
func entryJSONs(t *testing.T, session *Session) []string {
	t.Helper()
	var out []string
	for _, entry := range session.GetEntries() {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(data))
	}
	return out
}

// A session that can make no snapshot of its file still keeps what it loaded, as Pi does: it reads the records it does not hold from no file that
// another program can change, so no change to the file (bytes rewritten in place, cut short, replaced by a rename or removed) reaches a read,
// whether the session read its entries before or appended to them after.
func TestResumedSessionWithoutASnapshotReadsAsLoadedAfterItsFileChanges(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 3, 700)
				session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				if session.snapshot != nil || session.backing != "" {
					t.Fatal("the session has a snapshot or a backing file")
				}
				appendLater(t, session)
				want := everyRead(session)
				changeSessionFile(t, session.Path(), change)
				sameEveryRead(t, everyRead(session), want)
			})
		}
	})
}

// The same for a session that wrote its file itself: the entries of its first write, and those it appends after, are read as written.
func TestCreatedSessionWithoutASnapshotReadsAsWrittenAfterItsFileChanges(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 5, 700)
				if live.snapshot != nil || live.backing != "" {
					t.Fatal("the session has a snapshot or a backing file")
				}
				appendLater(t, live)
				want := everyRead(live)
				changeSessionFile(t, live.Path(), change)
				sameEveryRead(t, everyRead(live), want)
			})
		}
	})
}

// Moving the leaf (/tree, a fork) after the file changed reads the entries of the new context back: they are the entries the loaded session
// holds, so the prompt it builds is the one the unchanged file gives.
func TestLeafMovedAfterTheFileChangesWithoutASnapshotBuildsTheLoadedContext(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 7, 700)
				dir := filepath.Dir(live.Path())
				reference, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				session, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				entries := session.GetEntries()
				target := entries[len(entries)/2].Base().ID
				if err := reference.SetLeafID(&target); err != nil {
					t.Fatal(err)
				}
				want := everyRead(reference)
				changeSessionFile(t, session.Path(), change)
				if err := session.SetLeafID(&target); err != nil {
					t.Fatal(err)
				}
				sameEveryRead(t, everyRead(session), want)
			})
		}
	})
}

// A session that has a snapshot and whose snapshot write fails later (a full disk): the entries the snapshot holds stay located in it and
// readable, the entries after the failure stay in memory, and a change to the file reaches no read, whichever came first.
func TestSessionWhoseSnapshotWriteFailsLaterReadsAsLoadedAfterItsFileChanges(t *testing.T) {
	snapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change+" after the failure", func(t *testing.T) {
				live, _ := randomStoreSession(t, 3, 700)
				session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				failSnapshotWrites(t, session)
				appendLater(t, session)
				want := everyRead(session)
				changeSessionFile(t, session.Path(), change)
				sameEveryRead(t, everyRead(session), want)
			})
			t.Run(change+" before the failure", func(t *testing.T) {
				live, _ := randomStoreSession(t, 3, 700)
				session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				before := entryJSONs(t, session)
				changeSessionFile(t, session.Path(), change)
				failSnapshotWrites(t, session)
				ids := appendLater(t, session)
				after := entryJSONs(t, session)
				if len(after) != len(before)+len(ids) || !slices.Equal(after[:len(before)], before) {
					t.Fatal("the entries the session loaded changed with its file")
				}
				for i, id := range ids {
					got, ok := session.GetEntry(id)
					data, err := json.Marshal(got)
					if !ok || err != nil || string(data) != after[len(before)+i] {
						t.Fatalf("the entry %s read back as %s (%v), want %s", id, data, err, after[len(before)+i])
					}
				}
			})
		}
	})
}

// A session that can make no snapshot holds the bodies of its context tail decoded and every other message in a compressed block of the
// session, so the entries it holds are bounded as those of a session that has a snapshot are, and none is located in the file.
func TestSessionWithoutASnapshotKeepsOnlyItsContextTailDecoded(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		live, _ := randomStoreSession(t, 3, 700)
		session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		session.mu.RLock()
		defer session.mu.RUnlock()
		decoded, packed := 0, 0
		for i := range session.slots {
			slot := &session.slots[i]
			if slot.typ() != typeMessage {
				continue
			}
			cold := session.coldPart(slot)
			switch {
			case session.resident.get(i) != nil:
				decoded++
			case cold.blockOf() != 0:
				packed++
			default:
				t.Fatalf("message %s is neither decoded nor in a block: it is located in the file at %d", session.idOf(slot), cold.off)
			}
			if cold.blockOf() == 0 && cold.off >= 0 {
				t.Fatalf("message %s is located in the file at %d", session.idOf(slot), cold.off)
			}
		}
		if decoded > retainTailEntries || packed == 0 {
			t.Fatalf("%d messages decoded (at most %d), %d packed", decoded, retainTailEntries, packed)
		}
	})
}

// A fork after the file changed writes the branch the loaded session holds: the records of the branch file are those the unchanged file gives.
func TestForkAfterTheFileChangesWithoutASnapshotWritesTheLoadedBranch(t *testing.T) {
	noSnapshotModes(t, func(t *testing.T) {
		for _, change := range sessionFileChanges {
			t.Run(change, func(t *testing.T) {
				live, _ := randomStoreSession(t, 9, 700)
				dir := filepath.Dir(live.Path())
				reference, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				session, err := NewSessionManagerWithDir(t.TempDir(), dir).Open(live.Path())
				if err != nil {
					t.Fatal(err)
				}
				entries := session.GetEntries()
				target := entries[len(entries)/2].Base().ID
				forked := func(s *Session) []string {
					t.Helper()
					path, err := s.CreateBranchedSession(target)
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					// The first line is the new session's header; the label entries are new (fresh ids and times), the rest are the retained records.
					var records []string
					for _, line := range strings.Split(string(data), "\n")[1:] {
						if !strings.Contains(line, `"type":"label"`) {
							records = append(records, line)
						}
					}
					return records
				}
				want := forked(reference)
				changeSessionFile(t, session.Path(), change)
				if got := forked(session); !slices.Equal(got, want) {
					t.Fatalf("the fork wrote %d records, want the %d of the unchanged file's fork", len(got), len(want))
				}
			})
		}
	})
}
