package sdk

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
)

// realisticLog builds entries shaped like the session file that motivated this
// work: ~14k entries averaging ~5 KB, dominated by assistant text.
func realisticLog(n int) []json.RawMessage {
	body := make([]byte, 4800)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	out := make([]json.RawMessage, 0, n)
	for i := range n {
		e, _ := json.Marshal(map[string]any{
			"id": fmt.Sprintf("e%d", i), "parentId": fmt.Sprintf("e%d", i-1),
			"type": "message",
			"message": map[string]any{
				"role":     "assistant",
				"provider": "anthropic",
				"model":    "claude",
				"content":  []map[string]any{{"type": "text", "text": string(body)}},
			},
		})
		out = append(out, e)
	}
	return out
}

func heapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func TestMirrorResidentCost(t *testing.T) {
	const n = 14000
	log := realisticLog(n)
	wire := 0
	for _, e := range log {
		wire += len(e)
	}

	// Production feeds the mirror from a wire frame, so the entry bytes are
	// allocated by the decode inside the measured region. Seeding from a slice
	// built beforehand measures only the slice headers and reports a mirror
	// that costs nothing, which the observed resident sizes disprove.
	frame, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	base := heapAlloc()
	runtime.KeepAlive(frame)

	var decoded []json.RawMessage
	if err := json.Unmarshal(frame, &decoded); err != nil {
		t.Fatal(err)
	}
	m := &sessionMirror{}
	m.subscribed.Store(true)
	m.seed(decoded, n, "e13999")
	decoded = nil

	held := heapAlloc() - base
	runtime.KeepAlive(frame)
	runtime.KeepAlive(m)

	if len(m.getEntries()) != n {
		t.Fatalf("mirror holds %d entries", len(m.getEntries()))
	}
	t.Logf("wire bytes   : %6.1f MB", float64(wire)/(1<<20))
	t.Logf("mirror heap  : %6.1f MB", float64(held)/(1<<20))
	t.Logf("overhead     : %6.2fx", float64(held)/float64(wire))
}

// GetBranch decodes once per mirror revision and returns a shallow slice copy
// on subsequent calls. This benchmark measures the steady-state cached path.
func BenchmarkBranchDecode(b *testing.B) {
	const n = 14000
	log := realisticLog(n)
	m := &sessionMirror{}
	m.subscribed.Store(true)
	m.seed(log, n, fmt.Sprintf("e%d", n-1))
	if len(m.getBranchEntries()) == 0 {
		b.Fatal("empty branch")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if len(m.getBranchEntries()) == 0 {
			b.Fatal("empty branch")
		}
	}
}

func recordedSessionLog(b *testing.B) ([]json.RawMessage, string) {
	b.Helper()
	path := os.Getenv("PIG_BENCH_SESSION_FILE")
	if path == "" {
		b.Skip("set PIG_BENCH_SESSION_FILE to a private JSONL session copy")
	}
	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var entries []json.RawMessage
	leafID := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 128*1024*1024)
	for scanner.Scan() {
		raw := bytes.Clone(scanner.Bytes())
		var identity struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			b.Fatalf("decode session line %d: %v", len(entries)+1, err)
		}
		if identity.Type == "session" {
			continue
		}
		entries = append(entries, raw)
		if identity.ID != "" {
			leafID = identity.ID
		}
	}
	if err := scanner.Err(); err != nil {
		b.Fatal(err)
	}
	return entries, leafID
}

func BenchmarkRecordedSessionBranchCached(b *testing.B) {
	entries, leafID := recordedSessionLog(b)
	m := &sessionMirror{}
	m.subscribed.Store(true)
	m.seed(entries, len(entries), leafID)
	branch := m.getBranchEntries()
	if len(branch) == 0 {
		b.Fatal("empty branch")
	}
	b.ReportMetric(float64(len(entries)), "entries")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if len(m.getBranchEntries()) != len(branch) {
			b.Fatal("branch length changed")
		}
	}
}
