package codingagent

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Port of agent-session-stats.test.ts "includes cache-warming usage exactly
// once without adding messages".
func TestCacheWarmingUsageCountsOnceWithoutAddingMessages(t *testing.T) {
	session := NewSession("usage", t.TempDir())
	entry, err := session.AppendUsage("cache_warm", "anthropic", "claude-opus-4-6", ai.Usage{
		Input: 2, Output: 1, CacheRead: 97, TotalTokens: 100,
		Cost: ai.UsageCost{Input: 0.001, Output: 0.002, CacheRead: 0.007, Total: 0.01},
	}, "extension override")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != "usage" || entry.Kind != "cache_warm" || entry.Note != "extension override" {
		t.Fatalf("entry = %+v", entry)
	}
	entries := session.Entries()
	if len(entries) != 1 || entries[0].Base.Type != "usage" {
		t.Fatalf("entries = %+v", entries)
	}
	stats := session.Accounting()
	want := SessionTokenStats{Input: 2, Output: 1, CacheRead: 97, Total: 100, Cost: 0.01}
	if stats.Tokens != want {
		t.Fatalf("tokens = %+v, want %+v", stats.Tokens, want)
	}
	if stats.TotalMessages != 0 {
		t.Fatalf("total messages = %d", stats.TotalMessages)
	}
	if messages := session.BuildContext(nil); len(messages) != 0 {
		t.Fatalf("usage entered context: %+v", messages)
	}
	wantBreakdown := []SessionUsageBreakdown{{Key: "anthropic/claude-opus-4-6", Cost: 0.01, Tokens: 100}}
	if !reflect.DeepEqual(stats.UsageBreakdown, wantBreakdown) {
		t.Fatalf("breakdown = %+v, want %+v", stats.UsageBreakdown, wantBreakdown)
	}
}

// The usage entry keeps upstream's wire shape: base fields, then kind,
// provider, model, usage, and a note only when one is given.
func TestUsageEntryWireShapeAndReload(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	session := NewSession("usage-wire", t.TempDir())
	session.SetPath(path)
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "p", ModelID: "m", Usage: &ai.Usage{Input: 1}}}); err != nil {
		t.Fatal(err)
	}
	entry, err := session.AppendUsage("cache_warm", "p", "m", ai.Usage{CacheRead: 5, Output: 1, TotalTokens: 6}, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		key, _ := decoder.Token()
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"type", "id", "parentId", "timestamp", "kind", "provider", "model", "usage"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	if leaf := session.LeafID(); leaf == nil || *leaf != entry.ID {
		t.Fatalf("usage entry did not become the leaf")
	}
	loaded, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Accounting(), session.Accounting()) {
		t.Fatalf("reloaded accounting = %+v, want %+v", loaded.Accounting(), session.Accounting())
	}
}

// Port of cache-stats.test.ts "uses only cache-warm usage entries as cache
// refreshes".
func TestOnlyCacheWarmUsageRefreshesCacheComparison(t *testing.T) {
	missMessage := &agent.AssistantMessage{
		Role: "assistant", Provider: "test", ModelID: "test-model", Timestamp: 600_000,
		Usage: &ai.Usage{Output: 10, CacheWrite: 110_000, Cost: ai.UsageCost{CacheWrite: 0.4125}},
	}
	idleAfter := func(kind string) int64 {
		t.Helper()
		session := NewSession("cache-refresh", "/tmp")
		turn1 := MessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", ID: "x", Timestamp: "1970-01-01T00:00:00Z"},
			Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{
				Role: "assistant", Provider: "test", ModelID: "test-model", Timestamp: 0,
				Usage: &ai.Usage{Output: 10, CacheWrite: 100_000, Cost: ai.UsageCost{CacheWrite: 0.375}},
			}},
		}
		usage := UsageEntry{
			SessionEntryBase: SessionEntryBase{Type: "usage", ID: "usage-" + kind, Timestamp: time.UnixMilli(500_000).UTC().Format(time.RFC3339Nano)},
			Kind:             kind, Provider: "test", Model: "test-model",
			Usage: ai.Usage{CacheRead: 100_000, TotalTokens: 100_000},
		}
		for _, entry := range []any{turn1, usage} {
			if err := session.AppendEntry(entry); err != nil {
				t.Fatal(err)
			}
		}
		miss := session.detectCacheMiss(missMessage)
		if miss == nil {
			t.Fatalf("%s: no cache miss detected", kind)
		}
		return miss.idleMillis
	}
	if idle := idleAfter("cache_warm"); idle != 100_000 {
		t.Fatalf("idle after cache warm = %d, want 100000", idle)
	}
	if idle := idleAfter("custom_operation"); idle != 600_000 {
		t.Fatalf("idle after other usage = %d, want 600000", idle)
	}
}

// Port of cache-stats.ts's cache_warm scan branch: a positive refresh becomes
// the previous cached prompt for cumulative Session accounting, while an empty
// refresh leaves the prior prompt unchanged.
func TestCacheWarmUsageUpdatesAccountingPreviousPrompt(t *testing.T) {
	t.Run("positive refresh establishes previous prompt", func(t *testing.T) {
		session := NewSession("cache-warm-previous", t.TempDir())
		if _, err := session.AppendUsage("cache_warm", "custom", "model", ai.Usage{CacheRead: 5_000}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: "assistant", Provider: "custom", ModelID: "model", Usage: &ai.Usage{Input: 5_000},
		}}); err != nil {
			t.Fatal(err)
		}
		waste := session.Accounting().CacheWaste
		if waste.MissedTokens != 5_000 || waste.MissCount != 1 {
			t.Fatalf("cache waste = %+v, want 5000 missed tokens in one miss", waste)
		}
	})

	t.Run("empty refresh preserves previous prompt", func(t *testing.T) {
		session := NewSession("empty-cache-warm", t.TempDir())
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: "assistant", Provider: "custom", ModelID: "model", Usage: &ai.Usage{CacheRead: 5_000},
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendUsage("cache_warm", "other", "empty", ai.Usage{}, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: "assistant", Provider: "custom", ModelID: "model", Usage: &ai.Usage{Input: 5_000},
		}}); err != nil {
			t.Fatal(err)
		}
		waste := session.Accounting().CacheWaste
		if waste.MissedTokens != 5_000 || waste.MissCount != 1 {
			t.Fatalf("cache waste = %+v, want prior 5000-token prompt preserved", waste)
		}
	})
}

// Usage entries are hidden from /tree in every filter mode.
func TestTreeTagsUsageEntries(t *testing.T) {
	session := NewSession("usage-tree", t.TempDir())
	entry, err := session.AppendUsage("cache_warm", "p", "m", ai.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &treeNodeAdapter{n: &SessionTreeNode{Entry: mustEntryByID(t, session, entry.ID)}, f: newTreeRowFormatter(session)}
	if tags := adapter.NodeFilterTags(); !reflect.DeepEqual(tags, []string{"usage"}) {
		t.Fatalf("tags = %v, want [usage]", tags)
	}
}

func mustEntryByID(t *testing.T, session *Session, id string) SessionEntry {
	t.Helper()
	entry, ok := session.EntryByID(id)
	if !ok {
		t.Fatalf("entry %s missing", id)
	}
	return entry
}

// A background usage append (cache warming) racing the agent's message
// appends must extend the one active chain, never fork it.
func TestConcurrentUsageAppendKeepsOneChain(t *testing.T) {
	session := NewSession("usage-race", t.TempDir())
	const each = 50
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range each {
			if _, err := session.AppendUsage("cache_warm", "p", "m", ai.Usage{}, ""); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range each {
		if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user"}}); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if branch := session.GetBranch(); len(branch) != 2*each {
		t.Fatalf("active branch has %d entries, want %d: an append forked the chain", len(branch), 2*each)
	}
}
