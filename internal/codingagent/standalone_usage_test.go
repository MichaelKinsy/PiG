package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStandaloneUsageAccounting(t *testing.T) {
	s := NewSession("usage", t.TempDir())
	s.SetPath(filepath.Join(t.TempDir(), "session.jsonl"))
	raw := json.RawMessage(`{"type":"usage","id":"usage1","parentId":null,"timestamp":"2026-09-23T00:00:00Z","kind":"cache_warm","note":"refresh","provider":"anthropic","model":"model","usage":{"input":100,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":100,"cost":{"input":0.0003,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.0003}}}`)
	if err := s.AppendEntry(NewSessionEntry(raw, SessionEntryBase{Type: "usage", ID: "usage1", Timestamp: "2026-09-23T00:00:00Z"})); err != nil {
		t.Fatal(err)
	}
	initial := s.FooterUsageTotals()
	if initial.input != 100 || initial.cost != 0.0003 {
		t.Fatalf("live usage omitted: %+v", initial)
	}
	header := `{"type":"session","version":3,"id":"usage","timestamp":"2026-09-23T00:00:00Z","cwd":"/tmp"}`
	if err := os.WriteFile(s.Path(), []byte(header+"\n"+string(raw)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSessionFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	stats := loaded.Accounting()
	if len(stats.UsageBreakdown) != 1 || stats.UsageBreakdown[0].Key != "anthropic/model" {
		t.Fatalf("breakdown: %+v", stats)
	}
	got := loaded.FooterUsageTotals()
	if got.input != 100 || got.cost != 0.0003 {
		t.Fatalf("standalone usage omitted: %+v", got)
	}
}
