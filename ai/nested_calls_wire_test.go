package ai_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// pi: packages/ai/src/types.ts:658 gives a tool result message `nestedCalls`, the record of the calls its tool made through ctx.executeTool()
// (coding-agent agent-session.ts:1168-1173). It is session data: no converter of packages/ai/src/api reads the member, so the request body of no
// API kind holds the record's ids, names or error texts. PiG's bodies hold none either and are the bytes Pi posts.
func TestNestedCallsDoNotReachAProviderRequest(t *testing.T) {
	user := `{"role":"user","content":"go","timestamp":1}`
	var rows []toolSurrogateRow
	for _, shape := range toolSurrogateShapes() {
		row := shape
		row.Name = shape.Name + ": a tool result with nestedCalls"
		row.Context = json.RawMessage(`{"systemPrompt":"s","tools":[` + surrogateTool("N", "x") + `],"messages":[` + user +
			`,{"role":"assistant","content":[{"type":"toolCall","id":"toolu_1","name":"N[x]","arguments":{"K[x]":"E[x]"}}],"api":"` + shape.API + `","provider":"` + shape.Provider + `","model":"` + shape.ID + `",` +
			`"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":2},` +
			`{"role":"toolResult","toolCallId":"toolu_1","toolName":"N[x]","content":[{"type":"text","text":"RAN[ok]"}],"isError":false,"timestamp":3,` +
			`"nestedCalls":{"calls":[{"id":"toolu_1/1","name":"NN[inner]","status":"error","error":"NE[boom]"}],"complete":true}}]}`)
		rows = append(rows, row)
	}
	want := piPayloads(t, "testdata/tool_surrogate_wire_body.mjs", rows)
	for i, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			skipStrippedAPI(t, row.API)
			capture := &wireCapture{}
			toolSurrogateResult(t, row, capture)
			for _, side := range []struct{ name, body string }{{"Pi", want[i]}, {"PiG", string(capture.body)}} {
				if !strings.Contains(side.body, "RAN[ok]") {
					t.Fatalf("%s's request body does not hold the tool result: %s", side.name, side.body)
				}
				for _, text := range []string{"nestedCalls", "NE[boom]", "NN[inner]", "toolu_1/1"} {
					if strings.Contains(side.body, text) {
						t.Errorf("%s's request body holds %s of the nested call record: %s", side.name, text, side.body)
					}
				}
			}
			if difference := wireDifference(string(capture.body), want[i]); difference != "" {
				t.Error(difference)
			}
		})
	}
}
