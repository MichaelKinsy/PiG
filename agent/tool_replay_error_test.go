package agent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi's _restoreToolsFromTranscript reads the toolsAdded and toolsRemoved of every system message of the branch and throws a TypeError for a member it cannot read, and a content it cannot read never fails it. Each case is a branch's system messages; the installed Pi replays the same messages, and ToolReplayError must report the message Pi throws, or nil where Pi replays without one.
func TestToolReplayErrorMatchesPi(t *testing.T) {
	cases := [][]string{
		{`{"role":"system","content":5,"timestamp":1}`},
		{`{"role":"system","content":[null],"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":5,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":1.5e0,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":false,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":{"a":1},"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":"ab","timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[null],"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[5,"x"],"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":null,"toolsRemoved":null,"timestamp":1}`},
		{`{"role":"system","content":"","toolsRemoved":true,"timestamp":1}`},
		{`{"role":"system","content":"","toolsRemoved":[null],"toolsAdded":5,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[null],"toolsRemoved":5,"timestamp":1}`},
		{`{"role":"system","content":"","toolsAdded":[{"name":"a"}],"toolsRemoved":[null],"timestamp":1}`, `{"role":"system","content":"","toolsAdded":5,"timestamp":2}`},
		{`{"role":"system","content":"","toolsAdded":{"b":2},"timestamp":1}`, `{"role":"system","content":"","toolsRemoved":4,"timestamp":3}`},
		{`{"role":"system","content":"base","toolsAdded":[{"name":"read","description":"d","parameters":{}}],"timestamp":1}`, `{"role":"system","content":"","toolsRemoved":[{"name":"read"}],"timestamp":2}`},
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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tool-replay-oracle.mjs", pigversion.UpstreamPackageVersion)
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
		if err := ToolReplayError(decoded); err != nil {
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
