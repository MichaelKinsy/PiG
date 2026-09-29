package sdk

import (
	"encoding/json"
	"testing"
)

// Pi extensions/types.ts:294-300: null is unknown, not a known zero.
func TestContextUsageFallbacksPreserveNullAndZero(t *testing.T) {
	for _, tc := range []struct {
		wire    string
		tokens  int
		percent float64
	}{
		{`{"tokens":null,"contextWindow":128000,"percent":null}`, -1, -1},
		{`{"tokens":0,"contextWindow":128000,"percent":0}`, 0, 0},
		{`{"tokens":15,"contextWindow":128000,"percent":0.01171875}`, 15, 0.01171875},
	} {
		var usage ContextUsage
		if err := json.Unmarshal([]byte(tc.wire), &usage); err != nil {
			t.Fatal(err)
		}
		if usage.TokensOr(-1) != tc.tokens || usage.PercentOr(-1) != tc.percent {
			t.Fatalf("fallbacks = %d, %v", usage.TokensOr(-1), usage.PercentOr(-1))
		}
		encoded, err := json.Marshal(usage)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != tc.wire {
			t.Fatalf("round trip = %s, want %s", encoded, tc.wire)
		}
	}
}

// Pi sendCustomMessage distinguishes omitted triggerTurn from explicit false.
func TestBoolOptionsPreservePresence(t *testing.T) {
	for _, custom := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			options SendMessageOptions
			want    string
		}{
			{"default", SendMessageOptions{}, `{}`},
			{"true", SendMessageOptions{TriggerTurn: Bool(true)}, `{"triggerTurn":true}`},
			{"false", SendMessageOptions{TriggerTurn: Bool(false)}, `{"triggerTurn":false}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ext := New("surface")
				ext.Command("send", "", func(ctx Context, _ string) error {
					if custom {
						return ctx.SendCustomMessage(CustomMessage{CustomType: "note", Content: "hello"}, tc.options)
					}
					return ctx.SendMessage("note", "hello", false, tc.options)
				})
				host, _, done := surfaceHost(t, ext, nil)
				calls, response := runSurfaceCommand(t, host, "send", func(*callMsg) *callResultMsg { return &callResultMsg{} })
				if response.Error != nil || len(calls) != 1 {
					t.Fatalf("calls=%v response=%v", calls, response)
				}
				args := decodeArgs(t, calls[0].Args)
				encoded, err := json.Marshal(args["options"])
				if err != nil {
					t.Fatal(err)
				}
				if string(encoded) != tc.want {
					t.Fatalf("options = %s, want %s", encoded, tc.want)
				}
				surfaceShutdown(t, host, done)
			})
		}
	}
}
