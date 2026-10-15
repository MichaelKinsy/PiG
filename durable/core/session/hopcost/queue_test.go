// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package hopcost

import "testing"

// The expectations are the ticks pi-durable's capsql facade measured on the reference (a logging copy of capsql.mjs `serial`,
// F-ticks-runtime-drift-gate: `DBG Q enq|start|done`, ticks counted from the submit's return).

// A statement on an idle queue is enqueued at t, starts at t+1 and resolves at t+4 (trace: enq 109, start 110, done 113).
func TestQueueIdleStatement(t *testing.T) {
	var q Queue
	if got := q.Statement(109); got != 113 {
		t.Fatalf("idle statement enqueued at 109 resolves at %d, want 113", got)
	}
	// The next one enqueued a tick after it resolved finds the queue idle again (trace: done 113, enq 114, start 115, done 118).
	if got := q.Statement(114); got != 118 {
		t.Fatalf("statement enqueued after the queue went idle resolves at %d, want 118", got)
	}
}

// A statement behind a statement starts one tick after the predecessor resolved, wherever it was enqueued: the scan's first
// statement (enq 181, done 185) holds the status read's statement (enq 182, start 186, done 189), which holds the scan's second
// (enq 186, start 190, done 193).
func TestQueueStatementBehindStatement(t *testing.T) {
	var q Queue
	if got := q.Statement(181); got != 185 {
		t.Fatalf("scan statement 1 resolves at %d, want 185", got)
	}
	if got := q.Statement(182); got != 189 {
		t.Fatalf("status statement behind it resolves at %d, want 189", got)
	}
	if got := q.Statement(186); got != 193 {
		t.Fatalf("scan statement 2 behind the status statement resolves at %d, want 193", got)
	}
}

// A transaction holds the queue for its whole callback (capsql.mjs :155-159): a statement enqueued meanwhile starts one tick
// after the transaction resolved, and the transaction waits for statements enqueued before it.
func TestQueueTransactionOccupiesTheQueue(t *testing.T) {
	var q Queue
	if got := q.Occupy(10, 20); got != 31 {
		t.Fatalf("transaction of 20 ticks enqueued at 10 resolves at %d, want 31", got)
	}
	if got := q.Statement(15); got != 35 {
		t.Fatalf("statement enqueued during the transaction resolves at %d, want 35", got)
	}
	var r Queue
	r.Statement(10)
	if got := r.Occupy(11, 20); got != 35 {
		t.Fatalf("transaction behind a statement resolves at %d, want 35", got)
	}
}

// The stages of every read sum to its ReadHops on an idle queue, for both gate profiles, and a profile without a queue has none.
func TestReadStepsSumToReadHops(t *testing.T) {
	reads := []Read{ReadRow, ReadDocument, ReadEntry, ReadEntryIn, ReadScanEntries, ReadHeadMarker}
	for _, p := range []Profile{GateCapsql, Gate} {
		for _, r := range reads {
			steps, ok := AppendSteps(nil, p, r)
			if !ok {
				t.Fatalf("%s read %d has no stages", p, r)
			}
			if got, want := Sum(steps), ReadHops(p, r); got != want {
				t.Errorf("%s read %d: stages sum to %d, ReadHops is %d", p, r, got, want)
			}
			var q Queue
			now := 0
			for _, s := range steps {
				if s.Statement {
					now = q.Statement(now)
				} else {
					now += s.Ticks
				}
			}
			if want := ReadHops(p, r); now != want {
				t.Errorf("%s read %d: walking the stages on an idle queue ends at %d, want %d", p, r, now, want)
			}
		}
	}
	for _, p := range []Profile{Memory, Node} {
		if _, ok := AppendSteps(nil, p, ReadRow); ok {
			t.Errorf("%s has a statement queue", p)
		}
	}
}

// A read job behind a scan: the status read (one statement, direct) waits for the scan's resolving statement. Walking the stages
// of the job and of the scan on one queue reproduces the reference (status job start 182, run done 192; scan statement 1
// enq 181).
func TestQueueScanBehindRead(t *testing.T) {
	var q Queue
	scan, _ := AppendSteps(nil, GateCapsql, ReadScanEntries)
	status, _ := AppendSteps(nil, GateCapsql, ReadRow)
	status = Trim(status)
	status = AppendDelay(status, 3)
	scanNow, statusNow := 181, 182
	// The scan's first statement is enqueued first (tick 181), the status statement next (182), the scan's second after it.
	scanNow = q.Statement(scanNow)
	statusNow = q.Statement(statusNow) + 3
	if statusNow != 192 {
		t.Fatalf("status job ends at %d behind the scan, want 192", statusNow)
	}
	scanNow = q.Statement(scanNow + 1)
	if scanNow != 193 {
		t.Fatalf("scan statement 2 ends at %d behind the status read, want 193", scanNow)
	}
	// The backend's 11 and 7 ticks, and the Session's Storage guard's 2 after each.
	if Sum(scan) != 13 || Sum(status) != 9 {
		t.Fatalf("idle stages: scan %d status %d, want 13 and 9", Sum(scan), Sum(status))
	}
}

// ReadOnLine: E23 measures 7 for readOnLine(() => storage.conversation(id)) (a direct 5-tick read) and 25 for readContextFrom's
// bounds (22); an async arrow that awaits nothing is still one tick of its own (scheduler.ts :308 waitForTask's read job is
// 4 ticks on Pi).
func TestReadOnLineCountsAJobThatAwaitsNothing(t *testing.T) {
	for _, c := range []struct {
		body   int
		direct bool
		want   int
	}{{5, true, 7}, {22, false, 25}, {0, false, 4}, {0, true, 3}} {
		if got := ReadOnLine(c.body, c.direct); got != c.want {
			t.Errorf("ReadOnLine(%d, %v) = %d, want %d", c.body, c.direct, got, c.want)
		}
	}
}
