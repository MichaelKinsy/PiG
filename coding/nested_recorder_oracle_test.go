package coding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/coding-agent/src/core/nested-tool-calls.ts

// NestedCallRecorder (nested-tool-calls.ts:25-80): the argument size limits (8 KiB per call, 32 KiB in total, counted as UTF-8 bytes of
// JSON.stringify), the 256-call cap, the incomplete marker, the 500-unit error slice and the summed usage, over scripted call sequences with
// arguments of every size class and JSON shape (U+2028, lone surrogates, large numbers, nesting). The pinned Pi and PiG give the same snapshot.
func TestNestedCallRecorderMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	chunk := []string{"a", "é", "😀", "\u2028", "\u2029", "<&>", "\\", "\"", "\n", "\u00a0", "\ufeff"}
	str := func(n int) string {
		var b strings.Builder
		for b.Len() < n {
			b.WriteString(chunk[rng.Intn(len(chunk))])
		}
		return b.String()
	}
	sizes := []int{0, 3, 20, 200, 2000, 4000, 8000, 8180, 8190, 8191, 8192, 9000}
	value := func() string {
		switch rng.Intn(9) {
		case 0:
			return quote(str(sizes[rng.Intn(len(sizes))]))
		case 1:
			return `"\ud83d"`
		case 2:
			return []string{"0", "-0", "1.5", "1e21", "1e-7", "123456789012345678901234567890", "true", "null"}[rng.Intn(8)]
		case 3:
			return fmt.Sprintf(`{"a":[1,2,{"b":%s}],"c":%s}`, quote(str(rng.Intn(100))), quote(str(rng.Intn(3000))))
		case 4:
			return `[` + strings.Repeat(`"xxxxxxxxxx",`, rng.Intn(300)) + `1]`
		default:
			return quote(str(rng.Intn(30)))
		}
	}
	var scripts [][]map[string]any
	for range 400 {
		var ops []map[string]any
		starts := 0
		calls := []int{1, 3, 6, 12, 40, 300}[rng.Intn(6)]
		for range calls {
			args := "{}"
			switch rng.Intn(6) {
			case 0:
				args = "null"
			case 1, 2:
				args = fmt.Sprintf(`{"k":%s}`, value())
			case 3:
				args = fmt.Sprintf(`{"x":%s,"y":%s,"z":%s}`, value(), value(), value())
			}
			ops = append(ops, map[string]any{"op": "start", "call": json.RawMessage(fmt.Sprintf(`{"type":"toolCall","id":"c%d","name":"tool%d","arguments":%s}`, starts, rng.Intn(3), args))})
			starts++
			if rng.Intn(3) != 0 {
				text := ""
				if rng.Intn(2) == 0 {
					text = str([]int{0, 5, 499, 500, 501, 600, 1500}[rng.Intn(7)])
				}
				ops = append(ops, map[string]any{"op": "finish", "index": rng.Intn(starts), "isError": rng.Intn(2) == 0, "text": text})
			}
			if rng.Intn(4) == 0 {
				ops = append(ops, map[string]any{"op": "usage", "usage": map[string]any{"input": rng.Intn(100), "output": rng.Intn(100), "cacheRead": rng.Intn(10), "cacheWrite": rng.Intn(10), "totalTokens": rng.Intn(300),
					"cost": map[string]any{"input": 0.001 * float64(rng.Intn(100)), "output": 0.5, "cacheRead": 0, "cacheWrite": 0, "total": 0.25}}})
			}
		}
		scripts = append(scripts, ops)
	}
	// Exact limit boundaries with ASCII text: an argument object of 8191, 8192 and 8193 bytes, then four of 8192 (32768 in total) followed by 2 more bytes.
	exact := func(size int) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"toolCall","id":"b","name":"t","arguments":{"k":%q}}`, strings.Repeat("a", size-8)))
	}
	for _, size := range []int{8191, 8192, 8193} {
		scripts = append(scripts, []map[string]any{{"op": "start", "call": exact(size)}})
	}
	boundary := []map[string]any{}
	for range 4 {
		boundary = append(boundary, map[string]any{"op": "start", "call": exact(8192)})
	}
	scripts = append(scripts, boundary, append(append([]map[string]any{}, boundary...), map[string]any{"op": "start", "call": json.RawMessage(`{"type":"toolCall","id":"e","name":"t","arguments":{}}`)}))
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/nested_recorder.mjs", pigversion.UpstreamVersion)
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
	for i, ops := range scripts {
		recorder := NewNestedCallRecorder()
		var records []*ai.NestedToolCallRecord
		started := []any{}
		for _, op := range ops {
			switch op["op"] {
			case "start":
				var call ai.ToolCall
				if err := json.Unmarshal(op["call"].(json.RawMessage), &call); err != nil {
					t.Fatalf("%s: %v", op["call"], err)
				}
				record := recorder.Start(call)
				records = append(records, record)
				started = append(started, record != nil)
			case "finish":
				recorder.Finish(records[op["index"].(int)], op["isError"].(bool), op["text"].(string))
			case "usage":
				var usage ai.Usage
				encoded, _ := json.Marshal(op["usage"])
				if err := json.Unmarshal(encoded, &usage); err != nil {
					t.Fatal(err)
				}
				recorder.AddUsage(usage)
			}
		}
		roundTrip := func(value any) any {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var out any
			_ = json.Unmarshal(encoded, &out)
			return collapseReplacement(out)
		}
		got := map[string]any{"started": started, "snapshot": nil, "usage": nil, "errorUnits": []any{}}
		if snapshot := recorder.Snapshot(); snapshot != nil {
			units := make([]any, len(snapshot.Calls))
			for j, call := range snapshot.Calls {
				if call.Error != "" {
					encoded := jsstring.ToUTF16(call.Error)
					values := make([]any, len(encoded))
					for k, unit := range encoded {
						values[k] = float64(unit)
					}
					units[j] = values
				}
			}
			got["errorUnits"] = units
			value := roundTrip(snapshot).(map[string]any)
			for _, call := range value["calls"].([]any) {
				delete(call.(map[string]any), "durationMs")
			}
			got["snapshot"] = value
		}
		if usage := recorder.TotalUsage(); usage != nil {
			got["usage"] = roundTrip(usage)
		}
		for key := range got {
			expected := collapseReplacement(want[i][key])
			if !reflect.DeepEqual(got[key], expected) {
				if failures++; failures <= 5 {
					t.Errorf("script %d %s: %s", i, key, firstDiff(key, got[key], expected))
				}
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d differences from Pi", failures)
	}
}

// collapseReplacement folds each run of U+FFFD in every string into one: Node writes a lone surrogate as a single U+FFFD, PiG's WTF-8 unit is three
// invalid bytes that the JSON round trip replaces one by one.
func collapseReplacement(value any) any {
	switch v := value.(type) {
	case string:
		var out strings.Builder
		previous := false
		for _, r := range v {
			if r == '\ufffd' && previous {
				continue
			}
			previous = r == '\ufffd'
			out.WriteRune(r)
		}
		return out.String()
	case []any:
		for i := range v {
			v[i] = collapseReplacement(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = collapseReplacement(v[k])
		}
	}
	return value
}

func firstDiff(path string, got, want any) string {
	switch g := got.(type) {
	case map[string]any:
		if w, ok := want.(map[string]any); ok {
			for key, value := range g {
				if other, present := w[key]; !present {
					return fmt.Sprintf("%s.%s: PiG has it", path, key)
				} else if !reflect.DeepEqual(value, other) {
					return firstDiff(path+"."+key, value, other)
				}
			}
			for key := range w {
				if _, present := g[key]; !present {
					return fmt.Sprintf("%s.%s: Pi has it", path, key)
				}
			}
		}
	case []any:
		if w, ok := want.([]any); ok {
			for i := range min(len(g), len(w)) {
				if !reflect.DeepEqual(g[i], w[i]) {
					return firstDiff(fmt.Sprintf("%s[%d]", path, i), g[i], w[i])
				}
			}
			return fmt.Sprintf("%s: PiG %d items, Pi %d", path, len(g), len(w))
		}
	}
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if len(a) > 160 {
		a = append(a[:160:160], "..."...)
	}
	if len(b) > 160 {
		b = append(b[:160:160], "..."...)
	}
	return fmt.Sprintf("%s: PiG %s, Pi %s", path, a, b)
}
