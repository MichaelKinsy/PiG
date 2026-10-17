package compaction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's estimateTokens (core/compaction/compaction.ts) runs against the vendored Pi module for the same messages.
func TestEstimateTokensMatchesPiOracle(t *testing.T) {
	assistant := func(blocks ...ai.AssistantContentBlock) agent.AgentMessage {
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Content: blocks}}
	}
	cases := map[string]agent.AgentMessage{
		"assistant negative zero":   assistant(oracleCall(t, "f", `{"a":-0}`)),
		"assistant numbers":         assistant(oracleCall(t, "f", `{"a":1e21,"b":0.1,"c":1.5e-7,"d":12345678901234567890,"e":100}`)),
		"assistant html and sep":    assistant(oracleCall(t, "f", "{\"s\":\"<&>\\u2028\\u2029\"}")),
		"assistant astral":          assistant(ai.TextContent{Text: strings.Repeat("😀", 7)}, ai.ThinkingContent{Thinking: "é小"}),
		"assistant empty":           assistant(),
		"assistant integer keys":    assistant(oracleCall(t, "f", `{"b":1,"2":2,"1":3}`)),
		"assistant nested objects":  assistant(oracleCall(t, "g", `{"o":{"z":[1,{"y":null}],"a":"x"},"n":null,"t":true}`)),
		"assistant unpaired escape": assistant(oracleCall(t, "g", `{"s":"\ud83d"}`)),
	}
	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var want int
			pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/compaction/compaction.js");
emit(mod.estimateTokens(input));`, json.RawMessage(raw), &want)
			if got := EstimateTokens(message); got != want {
				t.Errorf("EstimateTokens = %d, Pi %d (message %s)", got, want, raw)
			}
		})
	}
}
