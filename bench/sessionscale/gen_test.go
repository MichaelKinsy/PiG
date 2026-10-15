// SPDX-License-Identifier: MIT

package sessionscale

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// The generator writes the owner's shape scaled to the size: the compaction and leaf counts, a tree whose every parent precedes its child, an
// active context of at least the requested tokens, and the same bytes for the same seed.
func TestGenerateWritesTheScaledOwnerShape(t *testing.T) {
	shape := Shape{Bytes: 12 << 20, Compactions: 5, Leaves: 4, ContextTokens: 30_000, Seed: 3}
	var out bytes.Buffer
	meta, err := Generate(&out, shape)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Compactions != 5 || meta.Leaves != 4 {
		t.Fatalf("compactions %d leaves %d, want 5 and 4", meta.Compactions, meta.Leaves)
	}
	if meta.ContextTokens < 30_000 || meta.Bytes < 12<<20 {
		t.Fatalf("context %d tokens, %d bytes", meta.ContextTokens, meta.Bytes)
	}
	seen := map[string]bool{}
	children := map[string]int{}
	var leaf string
	sc := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	sc.Buffer(nil, 4<<20)
	for n := 0; sc.Scan(); n++ {
		var e struct {
			Type     string  `json:"type"`
			ID       string  `json:"id"`
			ParentID *string `json:"parentId"`
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d: %v", n+1, err)
		}
		if n == 0 {
			if e.Type != "session" {
				t.Fatalf("line 1 is %q, want the session header", e.Type)
			}
			continue
		}
		if e.ParentID != nil && !seen[*e.ParentID] {
			t.Fatalf("line %d: parent %s does not precede it", n+1, *e.ParentID)
		}
		if seen[e.ID] {
			t.Fatalf("line %d: duplicate id %s", n+1, e.ID)
		}
		seen[e.ID] = true
		if e.ParentID != nil {
			children[*e.ParentID]++
		}
		leaf = e.ID
	}
	leaves := 0
	for id := range seen {
		if children[id] == 0 {
			leaves++
		}
	}
	// Labels and session_info entries are appended at the leaf and become it, as Pi's appendEntry does, so each abandoned branch end and the final leaf are the only childless entries.
	if leaves != meta.Leaves || leaf != meta.LeafID {
		t.Fatalf("%d childless entries, want %d; last id %s, leaf %s", leaves, meta.Leaves, leaf, meta.LeafID)
	}
	var again bytes.Buffer
	if _, err := Generate(&again, shape); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), again.Bytes()) {
		t.Fatal("the same shape and seed wrote different bytes")
	}
}

// PiG's session loader, an oracle independent of the generator's bookkeeping, resumes the file to the leaf, entry count and active context the generator reports: the context opens with
// the last compaction, holds as many entries as Pi's buildContextEntries returns, and Pi's estimateTokens over its messages totals the reported tokens.
func TestGeneratedSessionResumesToTheReportedContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := Generate(f, Shape{Bytes: 8 << 20, Compactions: 4, Leaves: 3, ContextTokens: 40_000, Seed: 7, Cwd: t.TempDir()})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	session, err := codingagent.NewSessionManagerWithDir(t.TempDir(), t.TempDir()).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := session.GetEntryCount(); got != meta.Entries {
		t.Fatalf("loaded %d entries, generator reports %d", got, meta.Entries)
	}
	if leaf := session.GetLeafID(); leaf == nil || *leaf != meta.LeafID {
		t.Fatalf("leaf %v, generator reports %s", leaf, meta.LeafID)
	}
	entries := session.BuildContextEntries()
	if len(entries) != meta.ContextEntries || len(entries) == 0 || entries[0].Base().ID != meta.LastCompactionID {
		t.Fatalf("context has %d entries, generator reports %d opening with compaction %s", len(entries), meta.ContextEntries, meta.LastCompactionID)
	}
	tokens := 0
	for _, message := range session.BuildSessionContext().Messages {
		tokens += compaction.EstimateTokens(message)
	}
	if tokens != meta.ContextTokens || tokens < 40_000 {
		t.Fatalf("Pi's estimate of the resumed context is %d tokens, generator reports %d (want at least 40000)", tokens, meta.ContextTokens)
	}
}

// The default 299 MB shape reproduces the owner's counts and per-type bytes within 5%: the entry mix, the user, assistant and tool-result counts and bytes, the compactions and leaves, and the longest line.
func TestDefaultShapeMatchesTheOwnerStatistics(t *testing.T) {
	meta, err := Generate(io.Discard, Shape{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 0.05*want {
			t.Errorf("%s: %.0f, owner %.0f (more than 5%% apart)", name, got, want)
		}
	}
	near("bytes", float64(meta.Bytes), OwnerBytes)
	near("entries", float64(meta.Entries), OwnerEntries)
	near("tool results", float64(meta.ByType["toolResult"]), OwnerToolResults)
	near("tool-result bytes", float64(meta.BytesByType["toolResult"]), OwnerToolResultBytes)
	near("assistant messages", float64(meta.ByType["assistant"]), OwnerAssistants)
	near("assistant bytes", float64(meta.BytesByType["assistant"]), OwnerAssistantBytes)
	near("user messages", float64(meta.ByType["user"]), OwnerUsers)
	near("longest line", float64(meta.MaxLine), OwnerMaxLine)
	if meta.Compactions != OwnerCompactions || meta.Leaves != OwnerLeaves {
		t.Errorf("compactions %d leaves %d, owner %d and %d", meta.Compactions, meta.Leaves, OwnerCompactions, OwnerLeaves)
	}
}

// The real-session profile writes the profiled entry mix and prompt caching with misses on two models whose
// cache-read prices models.json declares, so the open path that reprices cache misses runs on it.
func TestGenerateWritesTheRealSessionProfile(t *testing.T) {
	dir := t.TempDir()
	meta, err := Write(dir, Shape{Bytes: 24 << 20, Profile: ProfileP1, Seed: 1}, "")
	if err != nil {
		t.Fatal(err)
	}
	for kind, want := range map[string]int{"system": P1Systems, "model_change": P1ModelChanges, "custom_message": P1CustomMessages, "thinking_level_change": P1ThinkingLevels, "compaction": meta.Shape.Compactions} {
		if got := meta.ByType[kind]; got != want {
			t.Errorf("%s entries %d, want %d", kind, got, want)
		}
	}
	ratio := func(a, b int) float64 { return float64(a) / float64(b) }
	for name, c := range map[string][2]float64{
		"assistants per user":        {ratio(meta.ByType["assistant"], meta.ByType["user"]), ratio(P1Assistants, P1Users)},
		"tool results per assistant": {ratio(meta.ByType["toolResult"], meta.ByType["assistant"]), ratio(P1ToolResults, P1Assistants)},
	} {
		if math.Abs(c[0]/c[1]-1) > 0.15 {
			t.Errorf("%s %.3f, profile %.3f", name, c[0], c[1])
		}
	}
	session, err := codingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Open(meta.Session)
	if err != nil {
		t.Fatal(err)
	}
	waste := session.Accounting().CacheWaste
	if waste.MissCount == 0 || waste.MissedCost <= 0 {
		t.Fatalf("cache waste %+v, want priced misses", waste)
	}
	models, err := os.ReadFile(filepath.Join(dir, "agent", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(models, []byte(`"`+ModelB+`"`)) || !bytes.Contains(models, []byte(`"cacheRead": 0.5`)) {
		t.Fatalf("models.json does not price %s:\n%s", ModelB, models)
	}
}
