package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// agent-session.ts getSessionStats, usage-totals.ts getUsageCostBreakdown, cache-stats.ts computeCacheWaste and footer.ts formatTokens against
// pinned Pi, over generated session entries (messages, tool results, usage entries, compactions and branch summaries with and without usage), and
// Number.toLocaleString against formatNumber, which /session prints.
type accountingSession struct {
	Entries []map[string]any   `json:"entries"`
	Prices  map[string]float64 `json:"prices"`
}

// generateAccountingSessions returns count generated sessions (messages, tool results, usage entries, compactions and branch summaries with and
// without usage), with the cache-read prices ($/million tokens) a model price lookup answers.
func generateAccountingSessions(r *rand.Rand, count int) []accountingSession {
	models := []struct{ provider, model string }{{"anthropic", "claude-a"}, {"anthropic", "claude-b"}, {"openai", "gpt-x"}, {"local", "m"}}
	prices := map[string]float64{"anthropic/claude-a": 0.3, "anthropic/claude-b": 1.5, "openai/gpt-x": 0}
	tokenChoices := []int{0, 0, 1, 7, 500, 1024, 1025, 2048, 5000, 20000, 100000, 120000}
	pick := func(values []int) int { return values[r.IntN(len(values))] }
	usageOf := func() map[string]any {
		input, output, read, write := pick(tokenChoices), pick(tokenChoices), pick(tokenChoices), pick(tokenChoices)
		if r.IntN(3) == 0 {
			read = 0
		}
		if r.IntN(3) == 0 {
			write = 0
		}
		cost := map[string]any{"input": float64(input) * 3e-6, "output": float64(output) * 15e-6, "cacheRead": float64(read) * 0.3e-6, "cacheWrite": float64(write) * 3.75e-6}
		total := cost["input"].(float64) + cost["output"].(float64) + cost["cacheRead"].(float64) + cost["cacheWrite"].(float64)
		if r.IntN(8) == 0 {
			total = 0
		}
		cost["total"] = total
		return map[string]any{"input": input, "output": output, "cacheRead": read, "cacheWrite": write, "totalTokens": input + output + read + write, "cost": cost}
	}
	var sessions []accountingSession
	for range count {
		entries := []map[string]any{}
		timestamp := int64(1_700_000_000_000)
		parent := any(nil)
		for i := range r.IntN(14) {
			id := fmt.Sprintf("e%d", i)
			base := map[string]any{"id": id, "parentId": parent, "timestamp": "2026-01-01T00:00:00.000Z"}
			parent = id
			timestamp += int64(pick([]int{0, 1000, 60_000, 400_000}))
			var entry map[string]any
			switch r.IntN(11) {
			case 0, 1:
				entry = map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": "hi", "timestamp": timestamp}}
			case 2, 3, 4, 5:
				m := models[r.IntN(len(models))]
				content := []any{map[string]any{"type": "text", "text": "ok"}}
				for range r.IntN(3) {
					content = append(content, map[string]any{"type": "toolCall", "id": "c", "name": "read", "arguments": map[string]any{}})
				}
				message := map[string]any{"role": "assistant", "content": content, "api": "anthropic-messages", "provider": m.provider, "model": m.model, "usage": usageOf(), "stopReason": "stop", "timestamp": timestamp}
				switch r.IntN(4) {
				case 0:
					message["responseModel"] = "resolved-" + m.model
				case 1:
					message["responseModel"] = ""
				}
				entry = map[string]any{"type": "message", "message": message}
			case 6:
				message := map[string]any{"role": "toolResult", "toolCallId": "c", "toolName": "read", "content": []any{}, "isError": false, "timestamp": timestamp}
				if r.IntN(2) == 0 {
					message["usage"] = usageOf()
				}
				entry = map[string]any{"type": "message", "message": message}
			case 7:
				m := models[r.IntN(len(models))]
				entry = map[string]any{"type": "usage", "kind": []string{"cache_warm", "other"}[r.IntN(2)], "provider": m.provider, "model": m.model, "usage": usageOf()}
			case 8:
				entry = map[string]any{"type": "compaction", "summary": "s", "firstKeptEntryId": "e0", "tokensBefore": 100}
				if r.IntN(2) == 0 {
					entry["usage"] = usageOf()
				}
			case 9:
				entry = map[string]any{"type": "branch_summary", "fromId": "e0", "summary": "s"}
				if r.IntN(2) == 0 {
					entry["usage"] = usageOf()
				}
			default:
				entry = map[string]any{"type": "model_change", "provider": "anthropic", "modelId": "claude-a"}
			}
			maps.Copy(entry, base)
			if entry["type"] == "usage" && entry["kind"] == "cache_warm" {
				// cache-stats.ts reads Date.parse(entry.timestamp) of a cache_warm entry.
				entry["timestamp"] = "2026-01-01T00:00:00.000Z"
			}
			entries = append(entries, entry)
		}
		sessions = append(sessions, accountingSession{entries, prices})
	}
	return sessions
}

func TestSessionAccountingMatchesPi(t *testing.T) {
	sessions := generateAccountingSessions(rand.New(rand.NewPCG(5, 8)), 600)
	numbers := []int{0, 1, 999, 1000, 1001, 9999, 10000, 12345, 99999, 100000, 999499, 999500, 999999, 1000000, 1049999, 1050000, 9949999, 9950000, 9999999, 10000000, 12345678, 123456789, 1234567890}
	input, err := json.Marshal(map[string]any{"sessions": sessions, "numbers": numbers})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_accounting.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		Sessions []struct {
			Stats struct {
				UserMessages, AssistantMessages, ToolCalls, ToolResults, TotalMessages int
				Tokens                                                                 struct{ Input, Output, CacheRead, CacheWrite, Total int }
				Cost                                                                   float64
			}
			Breakdown []struct {
				Key    string
				Cost   float64
				Tokens int
			}
			Waste struct {
				MissedTokens int
				MissedCost   float64
				MissCount    int
			}
		}
		Numbers []struct{ Locale, Tokens string }
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }
	failures := 0
	report := func(i int, what string, got, pi any) {
		if failures++; failures <= 8 {
			raw, _ := json.Marshal(sessions[i].Entries)
			t.Errorf("session %d %s:\n  Pig %+v\n  Pi  %+v\n  entries %s", i, what, got, pi, raw)
		}
	}
	for i, s := range sessions {
		session := NewSession("accounting", t.TempDir())
		session.SetCacheReadPriceSource(func(provider, model string) float64 { return s.Prices[provider+"/"+model] })
		for _, entry := range s.Entries {
			if err := session.AppendEntry(entry); err != nil {
				t.Fatalf("append %v: %v", entry, err)
			}
		}
		got, pi := session.Accounting(), want.Sessions[i]
		if got.UserMessages != pi.Stats.UserMessages || got.AssistantMessages != pi.Stats.AssistantMessages || got.ToolCalls != pi.Stats.ToolCalls || got.ToolResults != pi.Stats.ToolResults || got.TotalMessages != pi.Stats.TotalMessages {
			report(i, "message counts", []int{got.UserMessages, got.AssistantMessages, got.ToolCalls, got.ToolResults, got.TotalMessages}, []int{pi.Stats.UserMessages, pi.Stats.AssistantMessages, pi.Stats.ToolCalls, pi.Stats.ToolResults, pi.Stats.TotalMessages})
		}
		tk := got.Tokens
		if tk.Input != pi.Stats.Tokens.Input || tk.Output != pi.Stats.Tokens.Output || tk.CacheRead != pi.Stats.Tokens.CacheRead || tk.CacheWrite != pi.Stats.Tokens.CacheWrite || tk.Total != pi.Stats.Tokens.Total || !near(tk.Cost, pi.Stats.Cost) {
			report(i, "tokens", tk, pi.Stats)
		}
		if len(got.UsageBreakdown) != len(pi.Breakdown) {
			report(i, "breakdown length", got.UsageBreakdown, pi.Breakdown)
		} else {
			for j, entry := range got.UsageBreakdown {
				if want := pi.Breakdown[j]; entry.Key != want.Key || entry.Tokens != want.Tokens || !near(entry.Cost, want.Cost) {
					report(i, fmt.Sprintf("breakdown[%d]", j), entry, want)
					break
				}
			}
		}
		if got.CacheWaste.MissedTokens != pi.Waste.MissedTokens || got.CacheWaste.MissCount != pi.Waste.MissCount || !near(got.CacheWaste.MissedCost, pi.Waste.MissedCost) {
			report(i, "cache waste", got.CacheWaste, pi.Waste)
		}
	}
	for i, n := range numbers {
		if got := formatNumber(n); got != want.Numbers[i].Locale {
			t.Errorf("formatNumber(%d) = %q, Pi toLocaleString %q", n, got, want.Numbers[i].Locale)
		}
		if got := formatTokens(n); got != want.Numbers[i].Tokens {
			t.Errorf("formatTokens(%d) = %q, Pi %q", n, got, want.Numbers[i].Tokens)
		}
	}
	if failures > 8 {
		t.Errorf("%d session probes differ", failures)
	}
}

// interactive-mode.ts handleSessionCommand (/session) and cache-warmer.ts formatCacheWarmingStatus against pinned Pi, over generated sessions,
// names, cache-warming modes and statuses. Statuses that depend on the clock use a fixed `now`, so /session itself only gets clock-independent ones.
func TestSessionInfoCommandMatchesPi(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	sessions := generateAccountingSessions(r, 400)
	// /session shows a re-billed cost only from $0.0001 up: a miss of exactly that cost and one of the next float below it.
	boundary := func(tokens int) accountingSession {
		usage := func(input, write int, cost map[string]any) map[string]any {
			return map[string]any{"input": input, "output": 0, "cacheRead": 0, "cacheWrite": write, "totalTokens": input + write, "cost": cost}
		}
		message := func(id string, parent any, timestamp int, usage map[string]any) map[string]any {
			return map[string]any{"type": "message", "id": id, "parentId": parent, "timestamp": "2026-01-01T00:00:00.000Z", "message": map[string]any{"role": "assistant", "content": []any{}, "api": "anthropic-messages", "provider": "local", "model": "m", "usage": usage, "stopReason": "stop", "timestamp": timestamp}}
		}
		first := usage(0, tokens, map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 1, "total": 1})
		second := usage(tokens, 0, map[string]any{"input": 0.0001, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0.0001})
		return accountingSession{Entries: []map[string]any{message("e0", nil, 0, first), message("e1", "e0", 1000, second)}, Prices: map[string]float64{}}
	}
	sessions = append(sessions, boundary(1025), boundary(1083))
	decision := func(economics bool) map[string]any {
		return map[string]any{
			"phase":                   []string{"streaming", "idle"}[r.IntN(2)],
			"warmCost":                []float64{0.0004, 0.01234, 0.5}[r.IntN(3)],
			"missCost":                []float64{0.2, 1.23456, 0.0005}[r.IntN(3)],
			"continuationProbability": []float64{0, 0.005, 0.425, 0.995, 1, 0.1234}[r.IntN(6)],
			"expectedSavings":         []float64{0.05, 0.0499, -0.2, 12.3456, 0.0004}[r.IntN(5)],
			"economicsAvailable":      economics,
			"action":                  []string{"warm", "stop"}[r.IntN(2)],
		}
	}
	randomStatus := func(clockFree bool) map[string]any {
		status := map[string]any{"state": []string{"inactive", "scheduled", "refreshing"}[r.IntN(3)]}
		if r.IntN(2) == 0 {
			// Pi reports an undefined reason as "unknown reason" and keeps an empty one; Pi never writes an empty reason, and Go's string field cannot tell the two apart.
			status["reason"] = []string{"no cache", "provider unsupported"}[r.IntN(2)]
		}
		if r.IntN(3) != 0 {
			status["decision"] = decision(r.IntN(4) != 0)
		}
		if r.IntN(5) == 0 {
			status["extensionOverride"] = true
		}
		if !clockFree || r.IntN(2) == 0 {
			if clockFree {
				status["nextWarmAt"] = 1 // in the past
			} else {
				status["nextWarmAt"] = []int64{0, 1, 5_000, 61_000, 3_600_000, 3_661_001, 90_000_000}[r.IntN(7)]
			}
		}
		return status
	}
	names := []string{"", "", "Fix login", "  padded  ", "a\x1b[31mred", "日本語", "x"}
	models := []map[string]any{nil, {"provider": "anthropic", "id": "claude-a"}, {"provider": "openai", "id": "gpt-x"}}
	type probe struct {
		ID      string             `json:"id"`
		Name    string             `json:"name"`
		Mode    string             `json:"mode"`
		Status  map[string]any     `json:"status"`
		Model   map[string]any     `json:"model"`
		Entries []map[string]any   `json:"entries"`
		Prices  map[string]float64 `json:"prices"`
	}
	var probes []probe
	var goSessions []*Session
	for i, s := range sessions {
		session := NewSession("info", t.TempDir())
		session.SetCacheReadPriceSource(func(provider, model string) float64 { return s.Prices[provider+"/"+model] })
		for _, entry := range s.Entries {
			if err := session.AppendEntry(entry); err != nil {
				t.Fatal(err)
			}
		}
		goSessions = append(goSessions, session)
		p := probe{ID: session.ID(), Name: names[r.IntN(len(names))], Mode: string(CacheWarmingModes[r.IntN(len(CacheWarmingModes))]), Model: models[r.IntN(len(models))], Entries: s.Entries, Prices: s.Prices}
		if i%2 == 0 {
			p.Status = randomStatus(true)
		}
		probes = append(probes, p)
	}
	type statusProbe struct {
		Status map[string]any `json:"status"`
		Now    int64          `json:"now"`
	}
	var statusProbes []statusProbe
	for range 400 {
		statusProbes = append(statusProbes, statusProbe{randomStatus(false), []int64{0, 2_000, 60_000, 3_600_000}[r.IntN(4)]})
	}
	input, err := json.Marshal(map[string]any{"sessions": probes, "statuses": statusProbes})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_info.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct {
		Infos     []string
		Formatted []string
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	// The probes must reach the optional sections, or agreement would prove nothing.
	for _, section := range []string{"Cache Re-billed:", "Cache miss penalty:", "Cached:", "written to cache", "Name:", "Tools/summaries:", " misses)", "1 miss)"} {
		found := false
		for _, info := range want.Infos {
			found = found || strings.Contains(info, section)
		}
		if !found {
			t.Errorf("no probe reached %q", section)
		}
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	failures := 0
	for i, p := range probes {
		var status *CacheWarmingStatus
		if p.Status != nil {
			status = decodeStatus(t, p.Status)
		}
		settings := NewSettingsManager(t.TempDir(), t.TempDir())
		if err := settings.SetCacheWarmingMode(CacheWarmingMode(p.Mode)); err != nil {
			t.Fatal(err)
		}
		var got string
		sc := &SlashContext{
			AppendText:         func(text string) { got = text },
			GetSessionName:     func() string { return p.Name },
			CurrentSession:     func() *Session { return goSessions[i] },
			SettingsManager:    settings,
			CacheWarmingStatus: func() *CacheWarmingStatus { return status },
		}
		if p.Model != nil {
			sc.SelectedModelKey = func() string { return fmt.Sprintf("%v/%v", p.Model["provider"], p.Model["id"]) }
		}
		if err := sessionHandler(sc); err != nil {
			t.Fatal(err)
		}
		if got != want.Infos[i] {
			if failures++; failures <= 4 {
				raw, _ := json.Marshal(p)
				t.Errorf("session %d:\n  Pig %q\n  Pi  %q\n  probe %s", i, got, want.Infos[i], raw)
			}
		}
	}
	for i, p := range statusProbes {
		status := decodeStatus(t, p.Status)
		if got := FormatCacheWarmingStatus(*status, p.Now); got != want.Formatted[i] {
			if failures++; failures <= 8 {
				t.Errorf("status %v at %d:\n  Pig %q\n  Pi  %q", p.Status, p.Now, got, want.Formatted[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d probes differ", failures)
	}
}

func decodeStatus(t *testing.T, raw map[string]any) *CacheWarmingStatus {
	t.Helper()
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var status CacheWarmingStatus
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	return &status
}
