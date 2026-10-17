package compaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/bench/sessionscale"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func lazyTestMessage(role, text string) agent.AgentMessage {
	if role == "user" {
		return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}}
	}
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop, Timestamp: 1}}
}

// Collecting the entries of an abandoned branch over a session's lazy reads (what NavigateTree does on every /tree navigation, as Pi's
// navigateTree does) gives the entries the decoded reads give, and decodes none of the abandoned messages that are not resident: their
// records are read only for a before_tree handler or a summary.
func TestCollectEntriesForBranchSummaryReadsNoEvictedBody(t *testing.T) {
	dir := t.TempDir()
	live, err := codingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := live.AppendMessage(lazyTestMessage("user", "first"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if _, err := live.AppendMessage(lazyTestMessage("assistant", fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
		if _, err := live.AppendMessage(lazyTestMessage("user", fmt.Sprintf("question %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	session, err := codingagent.NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.SetLeafID(&first); err != nil {
		t.Fatal(err)
	}
	oldLeaf := *live.GetLeafID()
	got := CollectEntriesForBranchSummary(codingagent.HandleReads{Session: session}, oldLeaf, first)
	want := CollectEntriesForBranchSummary(session, oldLeaf, first)
	if got.CommonAncestorID != want.CommonAncestorID || len(got.Entries) != len(want.Entries) || len(want.Entries) != 400 {
		t.Fatalf("collected %d entries from %q, want %d from %q", len(got.Entries), got.CommonAncestorID, len(want.Entries), want.CommonAncestorID)
	}
	lazy := 0
	for _, entry := range got.Entries {
		if sessionentry.IsLazy(entry) {
			lazy++
		}
	}
	if lazy < 390 {
		t.Fatalf("%d of %d collected entries are lazy; the collection decoded the abandoned branch", lazy, len(got.Entries))
	}
	materialized, err := sessionentry.MaterializeAll(got.Entries)
	if err != nil {
		t.Fatal(err)
	}
	for i := range materialized {
		if string(materialized[i].Raw()) != string(want.Entries[i].Raw()) {
			t.Fatalf("entry %d is %s, want %s", i, materialized[i].Raw(), want.Entries[i].Raw())
		}
	}
}

// Collecting the abandoned branch of a /tree navigation from the leaf of the 300 MB owner-shape session to its first user message takes no
// more than Pi's whole navigation on the real binary (0.20 s from Enter to the editor; integrate-042 0.21 s) (opt-in: SESSIONSCALE_GATE=1).
func TestCollectEntriesForBranchSummaryWithinPiTimeOnTheOwnerShape(t *testing.T) {
	if os.Getenv("SESSIONSCALE_GATE") != "1" {
		t.Skip("set SESSIONSCALE_GATE=1 to run the session-scale gates")
	}
	const piNavigation = 200 * time.Millisecond
	dir := t.TempDir()
	if _, err := sessionscale.Write(dir, sessionscale.Shape{Bytes: sessionscale.OwnerBytes, Seed: 1}, ""); err != nil {
		t.Fatal(err)
	}
	session, err := codingagent.NewSessionManagerWithDir(dir, dir).Open(filepath.Join(dir, "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	reads := codingagent.HandleReads{Session: session}
	leaf := *session.GetLeafID()
	branch := reads.GetBranch()
	target := ""
	for _, entry := range branch {
		if entry.Base().Type == "message" {
			target = entry.Base().ID
			break
		}
	}
	best := time.Duration(1<<63 - 1)
	var collected CollectEntriesResult
	for range 3 {
		start := time.Now()
		collected = CollectEntriesForBranchSummary(reads, leaf, target)
		best = min(best, time.Since(start))
	}
	t.Logf("collect %d entries of the abandoned branch: %v (Pi 1.1.0 navigation: %v)", len(collected.Entries), best, piNavigation)
	if len(collected.Entries) < len(branch)/2 || !slices.ContainsFunc(collected.Entries, sessionentry.IsLazy) {
		t.Fatalf("collected %d entries of a %d-entry branch", len(collected.Entries), len(branch))
	}
	testenv.TimingGateFatal(t, best > piNavigation, "collecting the abandoned branch took %v, more than Pi's whole navigation (%v)", best, piNavigation)
	// A summary prepares the newest entries within its budget (128k window less the 16,384 reserve): it reads only those.
	start := time.Now()
	prep, err := PrepareBranchEntries(collected.Entries, 128000-16384)
	if err != nil {
		t.Fatal(err)
	}
	prepared := time.Since(start)
	// A before_tree handler receives every collected entry decoded.
	start = time.Now()
	if _, err := sessionentry.MaterializeAll(collected.Entries); err != nil {
		t.Fatal(err)
	}
	t.Logf("summary preparation: %d messages, %d tokens in %v; before_tree decode of %d entries: %v", len(prep.Messages), prep.TotalTokens, prepared, len(collected.Entries), time.Since(start))
	testenv.TimingGateFatal(t, prepared > piNavigation, "preparing the summary took %v, more than Pi's whole navigation (%v)", prepared, piNavigation)
}

// memRecords serves records from memory and counts the ones read; fail makes every read an error.
type memRecords struct {
	records [][]byte
	read    int
	fail    error
}

func (m *memRecords) ReadRecords(ats []sessionentry.RecordAt, out []json.RawMessage) (int, error) {
	if m.fail != nil {
		return 0, m.fail
	}
	for k, at := range ats {
		out[k] = m.records[at.Off]
	}
	m.read += len(ats)
	return -1, nil
}

// PrepareBranchEntries over lazy entries prepares what it prepares over the decoded entries and reads only the records its newest-first walk
// reaches before the token budget stops it; a record that cannot be read back is its error.
func TestPrepareBranchEntriesReadsOnlyWithinTheBudget(t *testing.T) {
	dir := t.TempDir()
	live, err := codingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		if _, err := live.AppendMessage(lazyTestMessage("user", fmt.Sprintf("question %d %s", i, strings.Repeat("word ", 40)))); err != nil {
			t.Fatal(err)
		}
		if _, err := live.AppendMessage(lazyTestMessage("assistant", fmt.Sprintf("answer %d %s", i, strings.Repeat("word ", 40)))); err != nil {
			t.Fatal(err)
		}
	}
	decoded := live.GetEntries()
	source := &memRecords{}
	lazy := make([]sessionentry.LazyEntry, len(decoded))
	entries := make([]codingagent.SessionEntry, len(decoded))
	for i, entry := range decoded {
		source.records = append(source.records, entry.Raw())
		base := entry.Base()
		lazy[i] = sessionentry.NewLazyEntry(base.ID, base.ParentID, base.Timestamp, source, sessionentry.RecordAt{Off: int64(i)})
		entries[i] = &lazy[i]
	}
	const budget = 3000
	want, err := PrepareBranchEntries(decoded, budget)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PrepareBranchEntries(entries, budget)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lazy preparation %d messages, %d tokens; want %d, %d", len(got.Messages), got.TotalTokens, len(want.Messages), want.TotalTokens)
	}
	if limit := 2*len(want.Messages) + 64; source.read > limit {
		t.Fatalf("read %d records for %d prepared messages; want at most %d", source.read, len(want.Messages), limit)
	}
	source.fail = errors.New("gone")
	var changed *sessionentry.RecordChangedError
	if _, err := PrepareBranchEntries(entries, budget); !errors.As(err, &changed) {
		t.Fatalf("a record that cannot be read: %v, want a RecordChangedError", err)
	}
}
