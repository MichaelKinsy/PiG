package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's convertToLlm and bashExecutionToText (core/messages.ts) run against the vendored Pi module for the same custom-role messages.
func TestConvertToLLMCustomRolesMatchPiOracle(t *testing.T) {
	var corpus []map[string]any
	for _, command := range []string{"ls", "echo `x`", "", "echo 😀\ttab"} {
		for _, output := range []any{"", "out", "a\nb\n", "```fence```", nil} {
			for _, exit := range []any{nil, float64(0), float64(1), float64(-1), float64(130), float64(4294967295)} {
				for _, flags := range []struct{ cancelled, truncated bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
					for _, path := range []any{nil, "", "/tmp/full.log"} {
						message := map[string]any{"role": "bashExecution", "command": command, "exitCode": exit, "cancelled": flags.cancelled, "truncated": flags.truncated, "timestamp": float64(5)}
						if output != nil {
							message["output"] = output
						}
						if path != nil {
							message["fullOutputPath"] = path
						}
						corpus = append(corpus, message)
					}
				}
			}
		}
	}
	corpus = append(corpus,
		map[string]any{"role": "bashExecution", "command": "x", "output": "y", "exitCode": float64(0), "excludeFromContext": true, "timestamp": float64(1)},
		map[string]any{"role": "bashExecution", "command": "x", "output": "y", "exitCode": float64(2), "excludeFromContext": false, "timestamp": float64(1)},
		map[string]any{"role": "custom", "customType": "t", "content": "plain", "display": true, "timestamp": float64(9)},
		map[string]any{"role": "custom", "customType": "t", "content": "", "display": false, "timestamp": float64(9)},
		map[string]any{"role": "custom", "customType": "t", "content": []any{map[string]any{"type": "text", "text": "block"}, map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}}, "display": true, "timestamp": float64(9)},
		map[string]any{"role": "custom", "customType": "t", "content": []any{}, "display": true, "timestamp": float64(9)},
		map[string]any{"role": "branchSummary", "summary": "sum", "fromId": "a", "timestamp": float64(3)},
		map[string]any{"role": "branchSummary", "summary": "", "fromId": "a", "timestamp": float64(3)},
		map[string]any{"role": "compactionSummary", "summary": "sum\n</summary>", "tokensBefore": float64(10), "timestamp": float64(4)},
		map[string]any{"role": "compactionSummary", "summary": "😀", "tokensBefore": float64(0), "timestamp": float64(1700000000000)},
	)
	var want []json.RawMessage
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/messages.js");
emit(input.map((m) => mod.convertToLlm([m])));`, corpus, &want)
	mismatches := 0
	for i, message := range corpus {
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		var decoded AgentMessage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		converted := ConvertToLLM([]AgentMessage{decoded})
		gotJSON, err := json.Marshal(converted)
		if err != nil {
			t.Fatal(err)
		}
		var got, expected any
		if err := json.Unmarshal(gotJSON, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want[i], &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			if mismatches++; mismatches <= 8 {
				t.Errorf("%s:\n Go %s\n Pi %s", raw, gotJSON, want[i])
			}
		}
	}
	if mismatches > 8 {
		t.Errorf("%d mismatches in %d messages", mismatches, len(corpus))
	}
}
