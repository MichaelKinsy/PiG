package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// piAiCanonicalJSON writes a JSON value so that two spellings of the same value are the same text: numbers as float64, and the keys of an object sorted unless ordered is
// true, in which case they keep the order JSON.stringify writes them in (array-index keys ascending, then insertion order).
func piAiCanonicalJSON(t *testing.T, raw []byte, ordered bool) string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out strings.Builder
	var write func()
	write = func() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("%v in %.200s", err, raw)
		}
		switch token := token.(type) {
		case json.Delim:
			if token == '[' {
				out.WriteByte('[')
				for i := 0; decoder.More(); i++ {
					if i > 0 {
						out.WriteByte(',')
					}
					write()
				}
				_, _ = decoder.Token()
				out.WriteByte(']')
				return
			}
			type member struct{ key, value string }
			var members []member
			for decoder.More() {
				keyToken, _ := decoder.Token()
				var value strings.Builder
				saved := out
				out = value
				write()
				value, out = out, saved
				members = append(members, member{keyToken.(string), value.String()})
			}
			_, _ = decoder.Token()
			if ordered {
				var indexes, others []member
				for _, m := range members {
					if n, err := strconv.ParseUint(m.key, 10, 32); err == nil && strconv.FormatUint(n, 10) == m.key && n != math.MaxUint32 {
						indexes = append(indexes, m)
					} else {
						others = append(others, m)
					}
				}
				sort.SliceStable(indexes, func(i, j int) bool {
					a, _ := strconv.ParseUint(indexes[i].key, 10, 32)
					b, _ := strconv.ParseUint(indexes[j].key, 10, 32)
					return a < b
				})
				members = slices.Concat(indexes, others)
			} else {
				slices.SortFunc(members, func(a, b member) int { return strings.Compare(a.key, b.key) })
			}
			out.WriteByte('{')
			for i, m := range members {
				if i > 0 {
					out.WriteByte(',')
				}
				fmt.Fprintf(&out, "%q:%s", m.key, m.value)
			}
			out.WriteByte('}')
		case json.Number:
			number, _ := token.Float64()
			out.WriteString(strconv.FormatFloat(number, 'g', -1, 64))
		default:
			encoded, _ := json.Marshal(token)
			out.Write(encoded)
		}
	}
	write()
	return out.String()
}

// orderedPiAiHelpers are the helpers whose answer is a message, a tool or a schema that an extension reads in key order (a system message's sections, a schema's keys).
var orderedPiAiHelpers = map[string]bool{
	"StringEnum": true, "getCurrentSystemMessage": true, "getInitialSystemMessage": true, "getCurrentTools": true, "getDeclaredTools": true, "normalizeContext": true,
	"collapseSystemMessages": true, "resolveTranscript": true, "withoutInitialSystemMessage": true, "createInitialSystemMessage": true,
}

// Pi's pure pi-ai helpers (clampThinkingLevel, calculateCost, contentText, normalizeContext, isRetryableAssistantError, isContextOverflow, StringEnum, envApiKeyAuth and
// their siblings in packages/ai/src) give every SDK, and the Node runtime, the answer Pi's own code gave at the pin for the same call. The expected values are
// test/parity/sdk-vectors/pi-ai/*.json and pi-coding-agent/*.json (automation/gen/generate-sdk-vectors.mjs runs Pi's own code to write them); a helper an SDK never ported, or ported as a constant, fails a call whose answer
// is neither.
func TestConformance_PiAiHelpersAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	golden := piVectorsFile(t)
	content, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Cases []struct {
			Fn     string          `json:"fn"`
			Result json.RawMessage `json:"result"`
		} `json:"cases"`
		Timing []json.RawMessage `json:"timing"`
		Auth   struct {
			Cases []struct {
				Result json.RawMessage `json:"result"`
				Seen   []string        `json:"seen"`
			} `json:"cases"`
			Login json.RawMessage `json:"login"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatal(err)
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			before := len(*h.notify)
			if !h.runner.ExecuteCommand(context.Background(), "pi_ai_helpers_probe", golden) {
				t.Fatal("fixture has no pi_ai_helpers_probe command")
			}
			var reported string
			pollUntilConformance(t, "pi_ai_helpers_probe: no report", func() bool {
				for _, n := range (*h.notify)[before:] {
					if rest, ok := strings.CutPrefix(n, "pi_ai_helpers:"); ok {
						reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
						return true
					}
				}
				return false
			})
			var got struct {
				Cases  [][]json.RawMessage `json:"cases"`
				Timing []json.RawMessage   `json:"timing"`
				Auth   []struct {
					Result json.RawMessage `json:"result"`
					Seen   []string        `json:"seen"`
				} `json:"auth"`
				Login json.RawMessage `json:"login"`
			}
			if err := json.Unmarshal([]byte(reported), &got); err != nil {
				t.Fatalf("report is not JSON: %v", err)
			}
			if len(got.Cases) != len(want.Cases) {
				t.Fatalf("%d answers for %d calls", len(got.Cases), len(want.Cases))
			}
			failures := 0
			for i, c := range want.Cases {
				switch {
				case len(c.Result) == 0 && len(got.Cases[i]) == 0:
				case len(c.Result) == 0 || len(got.Cases[i]) == 0:
					failures++
					t.Errorf("call %d (%s): answered %s, want %s", i, c.Fn, got.Cases[i], c.Result)
				default:
					if g, w := piAiCanonicalJSON(t, got.Cases[i][0], orderedPiAiHelpers[c.Fn]), piAiCanonicalJSON(t, c.Result, orderedPiAiHelpers[c.Fn]); g != w {
						failures++
						t.Errorf("call %d (%s): answered %.300s, want %.300s", i, c.Fn, g, w)
					}
				}
				if failures >= 5 {
					t.Fatal("too many differences")
				}
			}
			// createAssistantMessageEventStream times its final message (durationMs) as Pi's does.
			if len(got.Timing) != len(want.Timing) || len(want.Timing) == 0 {
				t.Fatalf("%d timing answers for %d scenarios", len(got.Timing), len(want.Timing))
			}
			for i, c := range want.Timing {
				type timed struct {
					Present    bool     `json:"present"`
					Value      *float64 `json:"value"`
					LaterTimed bool     `json:"laterTimed"`
				}
				var g, w timed
				_ = json.Unmarshal(got.Timing[i], &g)
				_ = json.Unmarshal(c, &w)
				if !reflect.DeepEqual(g, w) {
					t.Errorf("timing scenario %d (%s): %s, want %s", i, c, got.Timing[i], c)
				}
			}
			if len(got.Auth) != len(want.Auth.Cases) {
				t.Fatalf("%d auth answers for %d calls", len(got.Auth), len(want.Auth.Cases))
			}
			for i, c := range want.Auth.Cases {
				wantResult, gotResult := "null", "null"
				if len(c.Result) > 0 {
					wantResult = piAiCanonicalJSON(t, c.Result, false)
				}
				if len(got.Auth[i].Result) > 0 {
					gotResult = piAiCanonicalJSON(t, got.Auth[i].Result, false)
				}
				// A stored credential's empty env is written as {} by Pi and left out by the SDKs that have no way to tell an empty env from none.
				if g, w := strings.Replace(gotResult, `"env":{},`, "", 1), strings.Replace(wantResult, `"env":{},`, "", 1); g != w || !slices.Equal(got.Auth[i].Seen, c.Seen) {
					t.Errorf("envApiKeyAuth case %d: answered %s asking %v, want %s asking %v", i, g, got.Auth[i].Seen, w, c.Seen)
				}
			}
			if g, w := piAiCanonicalJSON(t, got.Login, false), piAiCanonicalJSON(t, want.Auth.Login, false); g != w {
				t.Errorf("envApiKeyAuth login: %s, want %s", g, w)
			}
		})
	}
}
