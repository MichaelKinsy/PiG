package agent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi's getCurrentSystemMessage throws a TypeError when a system message read from a session holds a content, toolsAdded or toolsRemoved member it cannot read, and AgentSession.prompt fails with it (agent-session.ts _preparePromptAndToolLoadout). Each case is a transcript; the installed Pi replays the same messages, and SystemReplayError must report the message Pi throws, or nil where Pi replays without one.
func TestCurrentSystemMessageErrorMatchesPi(t *testing.T) {
	cases := [][]string{
		{`{"role":"system","content":5,"timestamp":1}`},
		{`{"role":"system","content":true,"timestamp":1}`},
		{`{"role":"system","content":{"a":1},"timestamp":1}`},
		{`{"role":"system","content":[null],"timestamp":1}`},
		{`{"role":"system","content":[5,"x",{"type":"text","text":5}],"timestamp":1}`},
		{`{"role":"system","content":"x","sections":"ab","timestamp":"x"}`},
		{`{"role":"system","content":"","toolsAdded":5,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":1.5e0,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":1e21,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":-0.0,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":false,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":{"a":1},"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":"ab","timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[null],"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[5,"x"],"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":null,"toolsRemoved":null,"timestamp":1}`},
		{`{"role":"system","content":"","toolsRemoved":true,"timestamp":1}`},
		{`{"role":"system","content":"","toolsRemoved":[null],"toolsAdded":5,"timestamp":1}`},
		// The content of every system message is read before any tool list.
		{`{"role":"system","content":"","toolsAdded":5,"timestamp":1}`, `{"role":"user","content":"u","timestamp":2}`, `{"role":"system","content":7,"timestamp":3}`},
		{`{"role":"system","content":"","toolsAdded":{"b":2},"timestamp":1}`, `{"role":"system","content":"","toolsRemoved":4,"timestamp":3}`},
		{`{"role":"system","content":"base","toolsAdded":[{"name":"read","description":"d","parameters":{}}],"timestamp":1}`, `{"role":"user","content":"u","timestamp":2}`},
	}
	input, err := json.Marshal(func() [][]json.RawMessage {
		out := make([][]json.RawMessage, len(cases))
		for i, messages := range cases {
			for _, message := range messages {
				out[i] = append(out[i], json.RawMessage(message))
			}
		}
		return out
	}())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/system-replay-oracle.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.Bytes())
	}
	var want []string
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(cases) {
		t.Fatalf("Pi oracle report %s: %v", out, err)
	}
	throws := 0
	for i, messages := range cases {
		decoded := make([]AgentMessage, len(messages))
		for j, message := range messages {
			if err := decoded[j].UnmarshalJSON([]byte(message)); err != nil {
				t.Fatalf("%s: %v", message, err)
			}
		}
		got := "ok"
		if err := SystemReplayError(decoded); err != nil {
			got = err.Error()
		}
		if got != want[i] {
			t.Errorf("%s\n got %q\nwant %q (Pi)", messages, got, want[i])
		}
		if want[i] != "ok" {
			throws++
		}
	}
	if throws == 0 || throws == len(cases) {
		t.Fatalf("Pi throws for %d of %d cases; the table must hold both outcomes", throws, len(cases))
	}
}
