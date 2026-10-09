package compaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// generateSessionPath is one generated root-to-leaf session path: message, compaction, branch summary, custom message, model/thinking change, context edit and bookkeeping entries.
func generateSessionPath(rng *rand.Rand) []json.RawMessage {
	ts := `"timestamp":"2026-01-01T00:00:00.000Z"`
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	text := func() string {
		return strings.Repeat("word ", []int{0, 1, 3, 10, 40, 120, 300}[rng.Intn(7)]) + []string{"", "é", "😀", "\n", "\u2028", "\u2029", "<&>", "\\", "\"", "\u007f", "\u0001"}[rng.Intn(11)]
	}
	paths := []string{"a.go", "b/c.go", "d.ts", "e.md"}
	usage := func() string {
		if rng.Intn(3) == 0 {
			return `"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
		}
		in, out, cr, cw := rng.Intn(500), rng.Intn(300), rng.Intn(100), rng.Intn(50)
		total := in + out + cr + cw
		if rng.Intn(4) == 0 {
			total = 0
		}
		return fmt.Sprintf(`"usage":{"input":%d,"output":%d,"cacheRead":%d,"cacheWrite":%d,"totalTokens":%d,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`, in, out, cr, cw, total)
	}
	message := func() string {
		switch rng.Intn(8) {
		case 0, 1:
			if rng.Intn(4) == 0 {
				return fmt.Sprintf(`{"role":"user","content":[{"type":"text","text":%s},{"type":"image","data":"AAAA","mimeType":"image/png"}],"timestamp":1}`, quote(text()))
			}
			return fmt.Sprintf(`{"role":"user","content":%s,"timestamp":1}`, quote(text()))
		case 2, 3, 4:
			blocks := []string{fmt.Sprintf(`{"type":"text","text":%s}`, quote(text()))}
			if rng.Intn(3) == 0 {
				blocks = append(blocks, fmt.Sprintf(`{"type":"thinking","thinking":%s}`, quote(text())))
			}
			for range rng.Intn(3) {
				name := []string{"read", "write", "edit", "bash", "grep"}[rng.Intn(5)]
				blocks = append(blocks, fmt.Sprintf(`{"type":"toolCall","id":"c","name":"%s","arguments":{"path":%s,"x":%s}}`, name, quote(paths[rng.Intn(len(paths))]), quote(text())))
			}
			stop := []string{"stop", "stop", "stop", "toolUse", "error", "aborted", "length"}[rng.Intn(7)]
			return fmt.Sprintf(`{"role":"assistant","content":[%s],"api":"openai-completions","provider":"p","model":"m",%s,"stopReason":"%s","timestamp":1}`, strings.Join(blocks, ","), usage(), stop)
		case 5:
			return fmt.Sprintf(`{"role":"toolResult","toolCallId":"c","toolName":"read","content":[{"type":"text","text":%s}],"isError":%v,"timestamp":1}`, quote(text()), rng.Intn(5) == 0)
		case 6:
			extra := ""
			if rng.Intn(3) == 0 {
				extra = `,"excludeFromContext":true`
			}
			return fmt.Sprintf(`{"role":"bashExecution","command":%s,"output":%s,"exitCode":0,"cancelled":false,"truncated":false,"timestamp":1%s}`, quote(text()), quote(text()), extra)
		default:
			return fmt.Sprintf(`{"role":"custom","customType":"c","content":%s,"display":true,"timestamp":1}`, quote(text()))
		}
	}
	n := rng.Intn(14)
	entries := []json.RawMessage{}
	var ids []string
	for i := range n {
		id := fmt.Sprint(i + 1)
		parent := "null"
		if i > 0 {
			parent = quote(ids[i-1])
		}
		ids = append(ids, id)
		base := fmt.Sprintf(`"id":%q,"parentId":%s,%s`, id, parent, ts)
		var entry string
		switch k := rng.Intn(20); {
		case k < 11:
			entry = fmt.Sprintf(`{"type":"message",%s,"message":%s}`, base, message())
		case k < 13:
			first := ids[rng.Intn(len(ids))]
			if rng.Intn(6) == 0 {
				first = "zz"
			}
			entry = fmt.Sprintf(`{"type":"compaction",%s,"summary":%s,"firstKeptEntryId":%q,"tokensBefore":%d}`, base, quote(text()), first, rng.Intn(1000))
		case k == 13:
			details := ""
			switch rng.Intn(4) {
			case 0:
				details = fmt.Sprintf(`,"details":{"readFiles":[%s,%s],"modifiedFiles":[%s]}`, quote(paths[rng.Intn(len(paths))]), quote(paths[rng.Intn(len(paths))]), quote(paths[rng.Intn(len(paths))]))
			case 1:
				details = `,"details":{"readFiles":"x","modifiedFiles":null}`
			}
			if rng.Intn(3) == 0 {
				details += `,"fromHook":true`
			}
			entry = fmt.Sprintf(`{"type":"branch_summary",%s,"fromId":"1","summary":%s%s}`, base, quote(text()), details)
		case k == 14:
			entry = fmt.Sprintf(`{"type":"custom_message",%s,"customType":"c","content":%s,"display":true}`, base, quote(text()))
		case k == 15:
			entry = fmt.Sprintf(`{"type":"model_change",%s,"provider":"p","modelId":"m%d"}`, base, rng.Intn(3))
		case k == 16:
			entry = fmt.Sprintf(`{"type":"thinking_level_change",%s,"thinkingLevel":"high"}`, base)
		case k == 17:
			target := ids[rng.Intn(len(ids))]
			replacement := "null"
			if rng.Intn(2) == 0 {
				replacement = fmt.Sprintf(`{"content":%s}`, quote(text()))
			}
			entry = fmt.Sprintf(`{"type":"context_edit",%s,"targetId":%q,"replacement":%s}`, base, target, replacement)
		case k == 18:
			entry = fmt.Sprintf(`{"type":"label",%s,"targetId":"1","label":"l"}`, base)
		default:
			entry = fmt.Sprintf(`{"type":"session_info",%s,"name":"n"}`, base)
		}
		entries = append(entries, json.RawMessage(entry))
	}
	return entries
}

// pi: packages/coding-agent/src/core/compaction/compaction.ts

// prepareCompaction, findCutPoint, findTurnStartIndex, getLastAssistantUsage, estimateContextTokens, estimateTokens and shouldCompact
// (compaction.ts), with the session projection they read (session-manager.ts buildSessionContext), run in the pinned Pi and in PiG over
// generated session paths: user, assistant (usage, tool calls, errors), tool result, bash, custom, compaction, branch summary, custom
// message, model/thinking changes, context edits and bookkeeping entries, with token budgets around the cut points.
func TestCompactionMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var cases []map[string]any
	var raws [][]json.RawMessage
	for range 1200 {
		entries := generateSessionPath(rng)
		keep := []int{0, 1, 5, 20, 60, 200, 1000, 20000}[rng.Intn(8)]
		cases = append(cases, map[string]any{"entries": entries, "keep": keep, "reserve": 16384, "window": []int{0, 1, 50000, 83616, 83617, 100000, 200000}[rng.Intn(7)]})
		raws = append(raws, entries)
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/compaction_pi.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	roundTrip := func(value any) any {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for i, raw := range raws {
		entries := make([]codingagent.SessionEntry, len(raw))
		for j, r := range raw {
			entries[j] = sessionentry.DecodeSessionEntry(r)
		}
		settings := CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: cases[i]["keep"].(int)}
		context := codingagent.BuildSessionContext(entries, codingagent.LastLeaf(), nil)
		got := map[string]any{}
		got["context"] = map[string]any{"ok": roundTrip(context)}
		prep := PrepareCompaction(entries, settings)
		if prep == nil {
			got["prep"] = map[string]any{"ok": nil}
		} else {
			value := roundTrip(prep).(map[string]any)
			delete(value, "settings")
			got["prep"] = map[string]any{"ok": value}
		}
		cut := FindCutPoint(entries, 0, len(entries), settings.KeepRecentTokens)
		got["cut"] = map[string]any{"ok": map[string]any{"firstKeptEntryIndex": float64(cut.FirstKeptEntryIndex), "turnStartIndex": float64(cut.TurnStartIndex), "isSplitTurn": cut.IsSplitTurn}}
		turnStarts := make([]any, len(entries))
		for j := range entries {
			turnStarts[j] = map[string]any{"ok": float64(FindTurnStartIndex(entries, j, 0))}
		}
		got["turnStarts"] = turnStarts
		var usage any
		if u := GetLastAssistantUsage(entries); u != nil {
			usage = roundTrip(u)
		}
		got["lastUsage"] = map[string]any{"ok": usage}
		estimate := EstimateContextTokens(context.Messages)
		lastUsage := any(float64(estimate.LastUsageIndex))
		if estimate.LastUsageIndex < 0 {
			lastUsage = nil
		}
		got["estimate"] = map[string]any{"ok": map[string]any{"tokens": float64(estimate.Tokens), "usageTokens": float64(estimate.UsageTokens), "trailingTokens": float64(estimate.TrailingTokens), "lastUsageIndex": lastUsage}}
		tokens := make([]any, len(context.Messages))
		for j, message := range context.Messages {
			tokens[j] = map[string]any{"ok": float64(EstimateTokens(message))}
		}
		got["tokens"] = tokens
		got["should"] = map[string]any{"ok": ShouldCompact(cases[i]["window"].(int), 100000, settings)}
		for key, value := range got {
			if key == "prep" {
				// A compaction entry with an empty summary gives Pi previousSummary "", PiG's string field omits it (language: no undefined/"" distinction).
				if ok, _ := want[i][key].(map[string]any)["ok"].(map[string]any); ok != nil && ok["previousSummary"] == "" {
					delete(ok, "previousSummary")
				}
			}
			if !reflect.DeepEqual(value, want[i][key]) {
				if failures++; failures <= 6 {
					t.Errorf("case %d %s (keep %d, window %v): first difference at %s\n entries %.3000s", i, key, cases[i]["keep"], cases[i]["window"], firstDiff(key, value, want[i][key]), oracleJSON(raw))
				}
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d differences from Pi", failures)
	}
}

func oracleJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// firstDiff names the first path where two decoded JSON values differ, with both values.
func firstDiff(path string, got, want any) string {
	switch g := got.(type) {
	case map[string]any:
		w, ok := want.(map[string]any)
		if !ok {
			break
		}
		for _, key := range slices.Sorted(maps.Keys(g)) {
			if _, present := w[key]; !present {
				return fmt.Sprintf("%s.%s: PiG has it (%.200s), Pi does not", path, key, oracleJSON(g[key]))
			}
			if !reflect.DeepEqual(g[key], w[key]) {
				return firstDiff(path+"."+key, g[key], w[key])
			}
		}
		for key := range w {
			if _, present := g[key]; !present {
				return fmt.Sprintf("%s.%s: Pi has it (%.200s), PiG does not", path, key, oracleJSON(w[key]))
			}
		}
	case []any:
		w, ok := want.([]any)
		if !ok {
			break
		}
		for i := range min(len(g), len(w)) {
			if !reflect.DeepEqual(g[i], w[i]) {
				return firstDiff(fmt.Sprintf("%s[%d]", path, i), g[i], w[i])
			}
		}
		return fmt.Sprintf("%s: PiG has %d items, Pi %d", path, len(g), len(w))
	}
	return fmt.Sprintf("%s: PiG %.300s, Pi %.300s", path, oracleJSON(got), oracleJSON(want))
}
