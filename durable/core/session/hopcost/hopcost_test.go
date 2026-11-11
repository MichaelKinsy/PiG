// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import "testing"

// Pi's measured Storage read durations over the gate (dcore-trace-hops.md): direct-open value and the proxied value.
func TestReadMatchesPi(t *testing.T) {
	cases := []struct {
		name         string
		r            Read
		direct, lazy int
	}{
		{"conversation, task, findDocument, submission, scanTasks", ReadRow, 5, 7},
		{"document", ReadDocument, 8, 10},
		{"entry(id)", ReadEntry, 6, 8},
		{"entry(conversation, id), scanEntries, findLatestHeadMarker", ReadEntryIn, 11, 13},
		{"scanEntries", ReadScanEntries, 11, 13},
		{"findLatestHeadMarker", ReadHeadMarker, 11, 13},
		{"mintId", MintID, 1, 4},
	}
	for _, c := range cases {
		if got := readBackend(GateCapsql, c.r); got != c.direct {
			t.Errorf("%s: direct = %d, want %d", c.name, got, c.direct)
		}
		if got := readBackend(Gate, c.r); got != c.lazy {
			t.Errorf("%s: gate = %d, want %d", c.name, got, c.lazy)
		}
	}
}

// guarded is the Session guard's ticks around a backend call that takes d: the async arrow adopts the backend's promise (never
// before tick 2), `await call` adds one and the async #call's return one more (session.ts #call). It is the viaProxy arithmetic,
// and the client-ticks rows (F-ticks-node/-gate: every client call spends 2 more per Storage call than before 42a3497d) hold it
// end to end.
func TestTheSessionGuardWrapsEveryStorageCall(t *testing.T) {
	for d, want := range map[int]int{1: 4, 2: 4, 3: 5, 5: 7, 11: 13} {
		if got := guarded(d); got != want {
			t.Errorf("guarded(%d) = %d, want %d", d, got, want)
		}
	}
	for _, p := range []Profile{GateCapsql, Gate, Memory, Node} {
		for r := ReadRow; r <= MintID; r++ {
			if got, want := ReadHops(p, r), guarded(readBackend(p, r)); got != want {
				t.Errorf("%s read %d = %d, want %d", p, r, got, want)
			}
		}
		w := []Writes{{Entries: 2}, {Tasks: 1}}
		if got, want := Commit(p, w), guarded(commitBackend(p, w)); got != want {
			t.Errorf("%s commit = %d, want %d", p, got, want)
		}
	}
}

// The statements of a call land CommitTail ticks before the writer resumes from Storage.commit: the COMMIT is one of the call's last ticks.
func TestCommitTailIsWithinTheCommit(t *testing.T) {
	for _, p := range []Profile{GateCapsql, Node, Gate} {
		w := []Writes{{Entries: 1}}
		if tail := CommitTail(p); tail >= Commit(p, w) {
			t.Errorf("%s: the COMMIT is %d ticks before the resumption, a commit takes only %d", p, tail, Commit(p, w))
		}
	}
	if got := Commit(Memory, []Writes{{Entries: 3}}); got != MemoryCommitResume {
		t.Errorf("a MemoryStorage commit takes %d ticks, want %d", got, MemoryCommitResume)
	}
	if HitTicks != guarded(1) {
		t.Errorf("HitTicks = %d, want %d", HitTicks, guarded(1))
	}
}

// A hit answers in HitTicks over every profile; a miss of a conversation or task goes through the cache's async `#keep`, one tick more than
// the Storage read, and a submission is passed through. The numbers are measured (session_ticks.json: tx.conversation, tx.task, tx.submission).
func TestRecordReads(t *testing.T) {
	for _, p := range []Profile{GateCapsql, Node, Gate, Memory} {
		for _, r := range []Record{RecordConversation, RecordTask, RecordSubmission} {
			if RecordRead(p, r, CacheHit) != HitTicks {
				t.Errorf("%s %d: a hit takes %d, want %d", p, r, RecordRead(p, r, CacheHit), HitTicks)
			}
			want := ReadHops(p, ReadRow)
			if r != RecordSubmission {
				want = guarded(readBackend(p, ReadRow) + 1)
			}
			if got := RecordRead(p, r, CacheMiss); got != want {
				t.Errorf("%s %d: a miss takes %d, want %d", p, r, got, want)
			}
			if got, want := RecordRead(p, r, CacheOff), ReadHops(p, ReadRow); got != want {
				t.Errorf("%s %d: a read without a cache takes %d, want Storage's %d", p, r, got, want)
			}
		}
	}
}

// The host's callback reaches the core two ticks after the grant (the facade's own pass counter reads 2 as the first operation is sent:
// host/cf/test/host-callback-lag.test.ts), and Pi's callback starts with the job, or after the decision region for a task's runtime.commit.
func TestHostCallbackStart(t *testing.T) {
	if HostFirstOp != 2 {
		t.Errorf("HostFirstOp = %d, want 2", HostFirstOp)
	}
	if got := HostCallbackStart(false); got != 0 {
		t.Errorf("a Session commit's callback starts at tick %d, want 0", got)
	}
	if got := HostCallbackStart(true); got != GatedRegion {
		t.Errorf("a task commit's callback starts at tick %d, want %d", got, GatedRegion)
	}
}
