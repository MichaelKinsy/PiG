package codingagent

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.87.1 session-manager.ts:1633-1738 rebuilds labels from the resolved
// whole-session map, preserving Map insertion order and the winning timestamp.
func TestCloneReconstructsLabelsAndRetainedChain(t *testing.T) {
	s := NewSession("source", t.TempDir())
	appendRow := func(row string) {
		t.Helper()
		if err := s.AppendEntry(json.RawMessage(row)); err != nil {
			t.Fatal(err)
		}
	}
	appendRow(`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"hello","timestamp":1}}`)
	appendRow(`{"type":"label","id":"00000002","parentId":"00000001","timestamp":"2026-01-01T00:00:00.002Z","targetId":"00000001","label":"obsolete"}`)
	appendRow(`{"type":"model_change","id":"00000003","parentId":"00000002","timestamp":"2026-01-01T00:00:00.003Z","provider":"anthropic","modelId":"claude-test","extra":null}`)
	appendRow(`{"type":"label","id":"00000004","parentId":"00000003","timestamp":"2026-01-01T00:00:00.004Z","targetId":"00000001"}`)
	appendRow(`{"type":"message","id":"00000005","parentId":"00000004","timestamp":"2026-01-01T00:00:00.005Z","message":{"role":"user","content":"followup","timestamp":2}}`)
	// These label records are off the selected branch but affect its resolved labels.
	appendRow(`{"type":"label","id":"00000006","parentId":"00000005","timestamp":"2026-01-01T00:00:00.006Z","targetId":"00000005","label":"last"}`)
	appendRow(`{"type":"label","id":"00000007","parentId":"00000006","timestamp":"2026-01-01T00:00:00.007Z","targetId":"00000003","label":"model"}`)
	appendRow(`{"type":"label","id":"00000008","parentId":"00000007","timestamp":"2026-01-01T00:00:00.008Z","targetId":"00000005","label":"updated"}`)
	before, _ := json.Marshal(s.GetEntries())
	clone, err := NewSessionManagerWithDir(s.CWD(), t.TempDir()).Clone(s, "00000005")
	if err != nil {
		t.Fatal(err)
	}
	rows := clone.GetEntries()
	if len(rows) != 5 {
		t.Fatalf("clone contains %d entries, want 3 retained entries + 2 resolved labels: %s", len(rows), mustJSON(t, rows))
	}
	want := []string{
		`{"type":"message","id":"00000001","parentId":null,"timestamp":"2026-01-01T00:00:00.001Z","message":{"role":"user","content":"hello","timestamp":1}}`,
		`{"type":"model_change","id":"00000003","parentId":"00000001","timestamp":"2026-01-01T00:00:00.003Z","provider":"anthropic","modelId":"claude-test","extra":null}`,
		`{"type":"message","id":"00000005","parentId":"00000003","timestamp":"2026-01-01T00:00:00.005Z","message":{"role":"user","content":"followup","timestamp":2}}`,
	}
	for i, raw := range want {
		assertSessionJSON(t, rows[i].Raw(), []byte(raw))
	}
	for i, label := range []struct{ target, value, timestamp string }{
		{"00000005", "updated", "2026-01-01T00:00:00.008Z"}, {"00000003", "model", "2026-01-01T00:00:00.007Z"},
	} {
		var got LabelEntry
		if err := json.Unmarshal(rows[i+3].Raw(), &got); err != nil {
			t.Fatal(err)
		}
		if got.TargetID != label.target || got.Label == nil || *got.Label != label.value || got.Timestamp != label.timestamp {
			t.Fatalf("label: %+v", got)
		}
		if got.ParentID == nil || *got.ParentID != rows[i+2].Base().ID {
			t.Fatalf("orphan label: %+v", got)
		}
	}
	after, _ := json.Marshal(s.GetEntries())
	if string(before) != string(after) {
		t.Fatal("clone mutated source")
	}
}

// Ports packages/coding-agent/test/suite/regressions/8989-fork-compaction-label-boundary.test.ts.
func TestClonePreservesCompactionContextAtRemovedLabel(t *testing.T) {
	s := NewSession("source", t.TempDir())
	old, err := s.AppendMessage(mkUserMsg("old"))
	if err != nil {
		t.Fatal(err)
	}
	label := "checkpoint"
	if _, err := s.AppendLabelChange(old, &label); err != nil {
		t.Fatal(err)
	}
	boundary := *s.GetLeafID()
	kept, err := s.AppendMessage(mkUserMsg("kept"))
	if err != nil {
		t.Fatal(err)
	}
	compaction, err := s.AppendCompaction("summary", boundary, 100, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := s.AppendMessage(mkUserMsg("after"))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := NewSessionManagerWithDir(s.CWD(), t.TempDir()).Clone(s, leaf)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := clone.GetEntry(compaction)
	var c CompactionEntry
	if err := json.Unmarshal(entry.Raw(), &c); err != nil {
		t.Fatal(err)
	}
	if c.FirstKeptEntryID != kept {
		t.Fatalf("firstKeptEntryId=%q, want %q", c.FirstKeptEntryID, kept)
	}
	messages := clone.BuildContext(nil)
	if len(messages) != 3 {
		t.Fatalf("context lost kept messages: %s", mustJSON(t, messages))
	}
	if messages[0].Custom["role"] != agent.RoleCompactionSummary || messages[0].Custom["summary"] != "summary" || extractUserText(messages[1]) != "kept" || extractUserText(messages[2]) != "after" {
		t.Fatalf("wrong compaction context: %s", mustJSON(t, messages))
	}
}

// Pi generates eight lowercase hex characters and Date.toISOString() always
// includes exactly three fractional digits (session-manager.ts:277-284,1202-1452).
func TestGeneratedEntryWireIdentity(t *testing.T) {
	s := NewSession("source", t.TempDir())
	id, err := s.AppendMessage(mkUserMsg("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendModelChange("anthropic", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendThinkingLevelChange("off"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCustomMessageEntry("probe", "text", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendUsage("summary", "anthropic", "test", ai.Usage{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCompaction("summary", id, 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BranchWithSummary(&id, "branch", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendLabelChange(id, new("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendBashExecution(BashExecutionMessage{Command: "true", ExitCode: new(0), Timestamp: 1}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range s.GetEntries() {
		if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(e.Base().ID) {
			t.Errorf("%s id=%q", e.Base().Type, e.Base().ID)
		}
		if ids[e.Base().ID] {
			t.Errorf("duplicate id=%q", e.Base().ID)
		}
		ids[e.Base().ID] = true
		if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`).MatchString(e.Base().Timestamp) {
			t.Errorf("%s timestamp=%q", e.Base().Type, e.Base().Timestamp)
		}
	}
}

// BenchmarkSessionClone measures a file-backed clone with an assistant, 1000 user messages and interleaved labels. Each iteration removes only its own generated file.
func BenchmarkSessionClone(b *testing.B) {
	source := NewSession("source", b.TempDir())
	if _, err := source.AppendMessage(mkAssistantMsg("seed assistant")); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		id, err := source.AppendMessage(mkUserMsg("representative message"))
		if err != nil {
			b.Fatal(err)
		}
		if i%10 == 0 {
			if _, err := source.AppendLabelChange(id, new("checkpoint")); err != nil {
				b.Fatal(err)
			}
		}
	}
	manager := NewSessionManagerWithDir(source.CWD(), b.TempDir())
	leaf := *source.GetLeafID()
	b.ReportAllocs()
	for b.Loop() {
		clone, err := manager.Clone(source, leaf)
		if err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(clone.Path()); err != nil {
			b.Fatal(err)
		}
	}
}

func assertSessionJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("record=%s, want %s", got, want)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
