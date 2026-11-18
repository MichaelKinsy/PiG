package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// TestDiagnosticsAllowanceNarrow locks the oracle's D237 allowance: only the stream_ended_without_finish_reason entry, only on openai-completions.
func TestDiagnosticsAllowanceNarrow(t *testing.T) {
	build := func(types ...string) map[string]any {
		entries := []any{}
		for _, typ := range types {
			entries = append(entries, map[string]any{"type": typ})
		}
		return map[string]any{"role": "assistant", "diagnostics": entries}
	}
	encode := func(m map[string]any) string {
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	const d237 = "stream_ended_without_finish_reason"

	// (a) the D237 entry on openai-completions is removed, and the emptied key dropped.
	if got := encode(stripD237Diagnostics(ai.APIOpenAICompletions, build(d237))); got != `{"role":"assistant"}` {
		t.Errorf("(a) got %s", got)
	}
	// (b) the same type on another api is kept.
	if in := build(d237); encode(stripD237Diagnostics(ai.APIOpenAIResponses, in)) != encode(in) {
		t.Errorf("(b) removed on another api")
	}
	// (c) another type on openai-completions is kept; the D237 entry beside it goes.
	if got := encode(stripD237Diagnostics(ai.APIOpenAICompletions, build("other", d237))); got != `{"diagnostics":[{"type":"other"}],"role":"assistant"}` {
		t.Errorf("(c) got %s", got)
	}
	// (d) input is untouched (the Pi side is never passed, and a call does not modify its argument).
	in := build(d237)
	before := encode(in)
	stripD237Diagnostics(ai.APIOpenAICompletions, in)
	if encode(in) != before {
		t.Errorf("(d) input modified: %s", encode(in))
	}
}

// TestDiagnosticsAllowanceEndToEnd runs the comparison's message encoding (comparableMessageJSON, the step compareResponses applies to PiG's message before
// it compares with Pi's) on a (PiG, Pi) pair: only the D237 entry on openai-completions is forgiven.
func TestDiagnosticsAllowanceEndToEnd(t *testing.T) {
	const d237 = "stream_ended_without_finish_reason"
	message := func(types ...string) map[string]any {
		entries := []any{}
		for _, typ := range types {
			entries = append(entries, map[string]any{"type": typ})
		}
		m := map[string]any{"role": "assistant"}
		if len(entries) > 0 {
			m["diagnostics"] = entries
		}
		return m
	}
	// differs compares PiG's message with Pi's the way compareResponses does: the allowance on PiG's side only.
	differs := func(api ai.API, pig, pi map[string]any) (bool, bool) {
		got, used := comparableMessageJSON(api, pig)
		want, _ := json.Marshal(map[string]any{"message": pi})
		return string(got) != string(want), used
	}
	pi := message()
	t.Run("D237 only on completions equals", func(t *testing.T) {
		if d, used := differs(ai.APIOpenAICompletions, message(d237), pi); d || !used {
			t.Errorf("differs=%v used=%v, want equal with the allowance used", d, used)
		}
	})
	t.Run("D237 plus another type differs", func(t *testing.T) {
		if d, _ := differs(ai.APIOpenAICompletions, message(d237, "other"), pi); !d {
			t.Error("a message with another diagnostics type equals Pi's")
		}
	})
	t.Run("other type alone differs", func(t *testing.T) {
		if d, used := differs(ai.APIOpenAICompletions, message("other"), pi); !d || used {
			t.Errorf("differs=%v used=%v, want differs without the allowance", d, used)
		}
	})
	t.Run("D237 on another api differs", func(t *testing.T) {
		for _, api := range []ai.API{ai.APIAnthropicMessages, ai.APIOpenAIResponses} {
			if d, used := differs(api, message(d237), pi); !d || used {
				t.Errorf("%s: differs=%v used=%v, want differs without the allowance", api, d, used)
			}
		}
	})
}
