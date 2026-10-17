package codingagent

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forEachRawLocked is the oracle of the cache-miss repricing tests: it calls fn with the record and type of every entry in append order, reading the lines of entries that are not resident
// without decoding them. The caller holds s.mu.
func (s *Session) forEachRawLocked(fn func(raw json.RawMessage, entryType string)) {
	records := recordSource{s: s}
	defer records.close()
	reader := recordReader{blocks: s.blockList}
	for i := range s.slots {
		slot := &s.slots[i]
		if slot.entry != nil {
			fn(slot.entry.Raw(), slot.typ())
			continue
		}
		reader.file = records.get()
		if record, ok := reader.record(s.coldPart(slot)); ok {
			fn(record, slot.typ())
		}
	}
}

// accountEveryRecordAgain is how SetCacheReadPriceSource repriced cache misses before it kept them: account every record again with prices.
func accountEveryRecordAgain(s *Session, prices ModelPriceSource) sessionAccountingAccumulator {
	s.mu.Lock()
	defer s.mu.Unlock()
	rebuilt := sessionAccountingAccumulator{prices: prices}
	s.forEachRawLocked(func(raw json.RawMessage, entryType string) { rebuilt.add(raw, entryType) })
	return rebuilt
}

// cacheMissSessionLines writes a session of assistant responses that read from cache, miss it, or write it, on a built-in priced model and
// two unpriced ones, with compactions and cache-warming usage entries between them.
func cacheMissSessionLines(rng *rand.Rand, n int) []string {
	lines := []string{`{"type":"session","version":3,"id":"reprice","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`}
	models := [][2]string{{"anthropic", "claude-sonnet-4-5"}, {"custom", "model-a"}, {"custom", "model-b"}}
	parent := "null"
	prompt := 2_000
	for i := range n {
		id := fmt.Sprintf("e%05d", i)
		stamp := fmt.Sprintf("2026-01-01T00:%02d:%02d.000Z", i/60%60, i%60)
		var line string
		switch rng.IntN(40) {
		case 0:
			line = fmt.Sprintf(`{"type":"compaction","id":%q,"parentId":%s,"timestamp":%q,"summary":"s","firstKeptEntryId":%q,"tokensBefore":1}`, id, parent, stamp, id)
		case 1:
			m := models[rng.IntN(len(models))]
			line = fmt.Sprintf(`{"type":"usage","id":%q,"parentId":%s,"timestamp":%q,"kind":"cache_warm","provider":%q,"model":%q,"usage":{"input":3,"output":0,"cacheRead":%d,"cacheWrite":0,"totalTokens":%d,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`, id, parent, stamp, m[0], m[1], prompt, prompt+3)
		default:
			m := models[rng.IntN(len(models))]
			prompt += rng.IntN(3_000)
			input, read, write := 1+rng.IntN(50), 0, 0
			switch rng.IntN(4) {
			case 0: // a miss: nothing read
				input = prompt
			case 1: // a partial read
				read = rng.IntN(prompt)
				write = prompt - read - input
			default:
				read = prompt - input
			}
			write = max(write, 0)
			costIn, costRead, costWrite := float64(input)*rng.Float64()*1e-5, float64(read)*rng.Float64()*1e-6, float64(write)*rng.Float64()*1e-5
			line = fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":%q,"message":{"role":"assistant","content":[],"api":"anthropic-messages","provider":%q,"model":%q,"usage":{"input":%d,"output":7,"cacheRead":%d,"cacheWrite":%d,"totalTokens":%d,"cost":{"input":%v,"output":0,"cacheRead":%v,"cacheWrite":%v,"total":0}},"stopReason":"stop","timestamp":%d}}`,
				id, parent, stamp, m[0], m[1], input, read, write, input+read+write+7, costIn, costRead, costWrite, 1767225600000+int64(i)*1000)
		}
		lines = append(lines, line)
		parent = `"` + id + `"`
	}
	return lines
}

func requireSameCacheWaste(t *testing.T, label string, s *Session, want sessionAccountingAccumulator) {
	t.Helper()
	got := s.Accounting().CacheWaste
	w := want.stats.CacheWaste
	if got.MissCount != w.MissCount || got.MissedTokens != w.MissedTokens || math.Float64bits(got.MissedCost) != math.Float64bits(w.MissedCost) {
		t.Fatalf("%s: cache waste %+v (cost bits %x), accounting every record again gives %+v (cost bits %x)", label, got, math.Float64bits(got.MissedCost), w, math.Float64bits(w.MissedCost))
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if (s.stats.cachePrev == nil) != (want.cachePrev == nil) || (s.stats.cachePrev != nil && *s.stats.cachePrev != *want.cachePrev) {
		t.Fatalf("%s: previous cache request %+v, want %+v", label, s.stats.cachePrev, want.cachePrev)
	}
}

// A price source reprices the detected cache misses (cache-stats.ts prices a miss with getModel's cacheRead cost) to the bit-equal value
// accounting every record again gives, for loaded and appended sessions, with and without misses, and for a nil source.
func TestRepricingCacheMissesMatchesAccountingEveryRecordAgain(t *testing.T) {
	sources := map[string]ModelPriceSource{
		"nil":  nil,
		"zero": func(string, string) float64 { return 0 },
		"registry": func(provider, model string) float64 {
			return map[string]float64{"anthropic/claude-sonnet-4-5": 0.31, "custom/model-a": 0.3, "custom/model-b": 0.5}[provider+"/"+model]
		},
	}
	for seed := range uint64(24) {
		rng := rand.New(rand.NewPCG(seed, 11))
		lines := cacheMissSessionLines(rng, 20+rng.IntN(400))
		path := filepath.Join(t.TempDir(), "session.jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s, _, err := loadPersistedSession(path)
		if err != nil {
			t.Fatal(err)
		}
		requireSameCacheWaste(t, fmt.Sprintf("seed %d loaded", seed), s, accountEveryRecordAgain(s, nil))
		for _, name := range []string{"registry", "zero", "nil", "registry"} {
			s.SetCacheReadPriceSource(sources[name])
			requireSameCacheWaste(t, fmt.Sprintf("seed %d %s", seed, name), s, accountEveryRecordAgain(s, sources[name]))
		}
		// Responses appended after the source is bound are priced with it, and a later source reprices them too.
		for _, line := range cacheMissSessionLines(rng, 30)[1:] {
			line = strings.Replace(line, `"id":"e`, `"id":"x`, 1)
			line = strings.Replace(line, `"parentId":"e`, `"parentId":"x`, 1)
			if err := s.appendEntryRecord([]byte(line), false); err != nil {
				t.Fatal(err)
			}
		}
		requireSameCacheWaste(t, fmt.Sprintf("seed %d appended", seed), s, accountEveryRecordAgain(s, sources["registry"]))
		s.SetCacheReadPriceSource(sources["nil"])
		requireSameCacheWaste(t, fmt.Sprintf("seed %d appended repriced", seed), s, accountEveryRecordAgain(s, sources["nil"]))
	}
}

// Repricing reads no record: it works after the session file is gone, when the records of evicted entries cannot be read again.
func TestRepricingCacheMissesReadsNoRecord(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	lines := cacheMissSessionLines(rng, 3_000)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _, err := loadPersistedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	prices := func(provider, model string) float64 { return float64(len(provider)+len(model)) / 10 }
	want := accountEveryRecordAgain(s, prices)
	if want.stats.CacheWaste.MissCount == 0 {
		t.Fatal("the fixture has no cache miss")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.SetCacheReadPriceSource(prices)
	requireSameCacheWaste(t, "without the file", s, want)
}
