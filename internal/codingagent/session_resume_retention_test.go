// SPDX-License-Identifier: MIT

//go:build !race

package codingagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/test/longsession"
)

// resumeHistoryTurns and resumeKeptTurns size the resumed fixtures: longsession turns of about 20 KB each (four entries: a prompt, a read
// call, a 16 KiB read result and an answer). The history a compaction left behind is about 44 MB, the turns it kept about 2.2 MB, the active
// context of a session that compacted.
const (
	resumeHistoryTurns = 2200
	resumeKeptTurns    = 110
)

// The bounds on what a resumed session retains, measured after the resume (the session opened and the context the agent starts from built)
// and after a short exchange, in the bytes the session and the context reach (reachableBytes), not in heap statistics:
//   - ceilingResumeContextSlope: per further byte of the context's records. The context's messages decoded are what Pi holds and what the agent
//     works from, about one byte per record byte; a second copy of the records beside them is not.
//   - ceilingResumeHistorySlope: per further byte of history before the compaction, which the session reads back from its file when a reader
//     asks for it. An index of the entries may stay, at most 100 B for one of these 5 KB entries.
//   - ceilingResumeContextFactor and ceilingResumeConstant: the whole, within twice the context's record bytes plus the session's fixed state.
//
// On integrate-042 1166362571 the slopes were 2.17 per context byte (each resident entry read back keeps its record, a slice of the joined read
// buffer, beside its decode) and 0.041 per history byte (about 205 B per entry of index: slots, the id map, cold parts, ids, parents, /tree facts).
// Counted from the object graph they are 1.11 and 0.0176 here after the resume, 1.14 and 0.0176 after an exchange, the same in every run.
//
// The heap is read only for the third bound, which has 3.5 MB of room over the 0.1 MB the heap statistics of a process move by. The slopes
// have 12 % (history) and 24 % (context) of room on the graph, which does not move, and 0.5 % on the heap, which does: for one tree the heap's
// history slope read 0.0171 to 0.0199 against the 0.0200 bar, 0.017 to 0.019 when the process had run nothing before (the first measurement
// keeps the one-time initialization of the packages the session uses, 58 KB, that the fixtures after it do not) and 0.0196 to 0.0199 when
// other tests had already run it.
const (
	ceilingResumeContextSlope  = 1.5
	ceilingResumeHistorySlope  = 0.02
	ceilingResumeContextFactor = 2.0
	ceilingResumeConstant      = 4 << 20
)

// resumeFixture writes a version 3 session file of historyTurns+keptTurns longsession turns and then a compaction whose kept range is the last
// keptTurns turns. It returns the file's path, the bytes of history before the kept range and the bytes of the active context's lines (the
// kept turns and the compaction).
func resumeFixture(t *testing.T, dir string, historyTurns, keptTurns int) (path string, historyBytes, contextBytes int64) {
	t.Helper()
	turns := historyTurns + keptTurns
	path = filepath.Join(dir, "session.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewWriterSize(file, 1<<16)
	lines := &resumeLineCounter{out: out, keepFrom: 1 + 4*historyTurns, limit: 1 + 4*turns}
	// The token target overshoots (a turn is about 4.6k tokens); the counter cuts the file at the turn boundary.
	if _, err := longsession.Generate(lines, longsession.Options{Tokens: turns * 8192, Window: 200000, Seed: 7, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if lines.lines != lines.limit {
		t.Fatalf("fixture has %d lines; want %d", lines.lines, lines.limit)
	}
	compaction, err := json.Marshal(struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		ParentID         string `json:"parentId"`
		Timestamp        string `json:"timestamp"`
		Summary          string `json:"summary"`
		FirstKeptEntryID string `json:"firstKeptEntryId"`
		TokensBefore     int    `json:"tokensBefore"`
		FromHook         bool   `json:"fromHook"`
	}{"compaction", "c0", fmt.Sprintf("b%d", turns-1), "2027-01-01T00:00:00.000Z", "The session read and explained the modules so far.",
		fmt.Sprintf("u%d", historyTurns), turns * 4600, false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(append(compaction, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := out.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path, lines.bytes - lines.keptBytes, lines.keptBytes + int64(len(compaction)+1)
}

// resumeLineCounter passes the first limit lines written to it to out and counts their bytes, and the bytes of the lines from keepFrom on.
type resumeLineCounter struct {
	out                    *bufio.Writer
	keepFrom, limit, lines int
	bytes, keptBytes       int64
}

func (c *resumeLineCounter) Write(p []byte) (int, error) {
	for start := 0; start < len(p); {
		end := start
		for end < len(p) && p[end] != '\n' {
			end++
		}
		if end < len(p) {
			end++
		}
		if c.lines < c.limit {
			if _, err := c.out.Write(p[start:end]); err != nil {
				return 0, err
			}
			c.bytes += int64(end - start)
			if c.lines >= c.keepFrom {
				c.keptBytes += int64(end - start)
			}
			if p[end-1] == '\n' {
				c.lines++
			}
		}
		start = end
	}
	return len(p), nil
}

// resumeHeapLive is the heap in use after two collections.
func resumeHeapLive() uint64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// resumeMeasure is the heap a resumed fixture retains.
type resumeMeasure struct {
	historyTurns, keptTurns    int
	historyBytes, contextBytes int64
	afterResume, afterExchange int64
	// reachResume and reachExchange are the bytes the session and its context reach at the same two points.
	reachResume, reachExchange int64
}

// measureResume writes a fixture, opens it as a resume does (SessionManager.Open, then the context the agent starts from), appends a short
// exchange and projects the context as a turn does. It returns the bytes the session and the context reach after the resume and after the
// exchange, and the heap retained at the same points, with the context's messages held as the agent holds them.
func measureResume(t *testing.T, historyTurns, keptTurns int) resumeMeasure {
	t.Helper()
	path, historyBytes, contextBytes := resumeFixture(t, t.TempDir(), historyTurns, keptTurns)
	m := resumeMeasure{historyTurns: historyTurns, keptTurns: keptTurns, historyBytes: historyBytes, contextBytes: contextBytes}
	before := resumeHeapLive()
	session, err := NewSessionManagerWithDir(filepath.Dir(path), filepath.Dir(path)).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	context := session.BuildSessionContext()
	if got, want := len(context.Messages), 1+4*keptTurns; got != want {
		t.Fatalf("resumed context holds %d messages; want %d (the compaction summary and the kept turns)", got, want)
	}
	m.afterResume = int64(resumeHeapLive()) - int64(before)
	graph := measureReachable(session, &context)
	m.reachResume = graph.Bytes()
	if len(graph.opaque) != 1 || graph.opaque["os.File"] != 1 {
		t.Fatalf("the walk from the resumed session left %v unfollowed; want only its snapshot file, so every other byte it holds is counted", graph.opaque)
	}
	for i := range 2 {
		if _, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("question %d after the resume", i))); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d after the resume", i))); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := len(session.BuildSessionProjectionView().Messages), 1+4*keptTurns+4; got != want {
		t.Fatalf("projection after the exchange holds %d messages; want %d", got, want)
	}
	m.afterExchange = int64(resumeHeapLive()) - int64(before)
	m.reachExchange = measureReachable(session, &context).Bytes()
	runtime.KeepAlive(context)
	runtime.KeepAlive(session)
	t.Logf("history %d turns (%d B), context %d turns (%d B): reaches %d B after the resume, %d B after an exchange; the heap retains %d B (%.2f x context), %d B (%.2f x context)",
		historyTurns, historyBytes, keptTurns, contextBytes, m.reachResume, m.reachExchange, m.afterResume, float64(m.afterResume)/float64(contextBytes),
		m.afterExchange, float64(m.afterExchange)/float64(contextBytes))
	return m
}

// A session resumed from a file whose history a compaction left behind retains what its active context needs, not that history: Pi's
// session-manager.ts holds every entry, and PiG reads entries outside the context back from its snapshot of the file, so what the session keeps
// is bounded by the context window, by the owner's bar. Each further byte of context retains its decode and no copy of its record, each further
// byte of history at most a compact index entry, and the whole stays within twice the context's records plus a constant.
func TestResumedSessionRetainsItsContextNotItsHistory(t *testing.T) {
	base := measureResume(t, resumeHistoryTurns, resumeKeptTurns)
	longHistory := measureResume(t, 2*resumeHistoryTurns, resumeKeptTurns)
	longContext := measureResume(t, resumeHistoryTurns, 2*resumeKeptTurns)
	type phase struct {
		name   string
		slopes bool
		at     func(resumeMeasure) int64
	}
	for _, p := range []phase{
		{"after the resume, reached", true, func(m resumeMeasure) int64 { return m.reachResume }},
		{"after an exchange, reached", true, func(m resumeMeasure) int64 { return m.reachExchange }},
		{"after the resume, on the heap", false, func(m resumeMeasure) int64 { return m.afterResume }},
		{"after an exchange, on the heap", false, func(m resumeMeasure) int64 { return m.afterExchange }},
	} {
		if p.slopes {
			assertResumeSlopes(t, p.name, p.at, base, longHistory, longContext)
		}
		for _, m := range []resumeMeasure{base, longHistory, longContext} {
			if bound := int64(ceilingResumeContextFactor*float64(m.contextBytes)) + ceilingResumeConstant; p.at(m) > bound {
				t.Errorf("%s, a session with %d B of history and a %d B context retains %d B; want at most %d B (%.0f x context + %d)",
					p.name, m.historyBytes, m.contextBytes, p.at(m), bound, ceilingResumeContextFactor, ceilingResumeConstant)
			}
		}
	}
}

// assertResumeSlopes checks the two slopes of the bytes at measures: what each further byte of context and of history retains.
func assertResumeSlopes(t *testing.T, name string, at func(resumeMeasure) int64, base, longHistory, longContext resumeMeasure) {
	t.Helper()
	moreContext, moreHistory := at(longContext)-at(base), at(longHistory)-at(base)
	contextSlope := float64(moreContext) / float64(longContext.contextBytes-base.contextBytes)
	historySlope := float64(moreHistory) / float64(longHistory.historyBytes-base.historyBytes)
	t.Logf("%s: %.3f B per context byte, %.4f B per history byte", name, contextSlope, historySlope)
	if contextSlope > ceilingResumeContextSlope {
		t.Errorf("%s, %d B more context retains %d B more (%.2f per context byte); want at most %.2f: a resident entry read back keeps its record beside its decode",
			name, longContext.contextBytes-base.contextBytes, moreContext, contextSlope, ceilingResumeContextSlope)
	}
	if historySlope > ceilingResumeHistorySlope {
		t.Errorf("%s, %d B more history before the compaction retains %d B more (%.4f per history byte, %.0f B per entry); want at most %.4f: the session keeps history its context does not use",
			name, longHistory.historyBytes-base.historyBytes, moreHistory, historySlope,
			float64(moreHistory)/float64(4*(longHistory.historyTurns-base.historyTurns)), ceilingResumeHistorySlope)
	}
}
