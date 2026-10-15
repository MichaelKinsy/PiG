package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/agent/src/agent.ts Agent

// The Agent class against pinned Pi over random operation sequences: prompt and continue from every transcript state (empty, after a user message,
// after an assistant message, after an error or an abort), steer and follow-up queues in "all" and "one-at-a-time" mode, clears and reset. A stream function that throws is not
// compared: PiG reports its error as a stream function that failed during setup does in Pi (an error message in the loop's agent_end), while a throw
// reaches Pi's Agent.handleRunFailure, whose agent_end carries only the failure message. After each operation the events, the error, the transcript and whether messages are queued must be equal.
func TestAgentOperationsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(197))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	type op struct {
		Op   string `json:"op"`
		Text string `json:"text,omitempty"`
		Mode string `json:"mode,omitempty"`
	}
	type turn struct {
		Text string `json:"text"`
		Stop string `json:"stop"`
	}
	type probe struct {
		Turns []turn `json:"turns"`
		Ops   []op   `json:"ops"`
	}
	var probes []probe
	for range 500 {
		var p probe
		for range rng.Intn(6) {
			p.Turns = append(p.Turns, turn{Text: pick("a", "b", ""), Stop: pick("stop", "stop", "stop", "error", "aborted", "length")})
		}
		for range 2 + rng.Intn(7) {
			o := op{Op: pick("prompt", "prompt", "continue", "continue", "steer", "steer", "followUp", "followUp", "steerMode", "followMode", "clearSteer", "clearFollow", "clearAll", "reset")}
			switch o.Op {
			case "prompt", "steer", "followUp":
				o.Text = pick("one", "two", "three")
			case "steerMode", "followMode":
				o.Mode = pick("all", "one-at-a-time")
			}
			p.Ops = append(p.Ops, o)
		}
		probes = append(probes, p)
	}
	input, _ := json.Marshal(probes)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/agent_ops.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want [][]struct {
		Error    *string `json:"error"`
		Events   []any   `json:"events"`
		Messages []any   `json:"messages"`
		Queued   bool    `json:"queued"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, p := range probes {
		turnIndex := 0
		streamFn := func(_ context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			script := turn{Text: "done", Stop: "stop"}
			if turnIndex < len(p.Turns) {
				script = p.Turns[turnIndex]
			}
			turnIndex++
			if script.Stop == "throw" {
				return nil, errStreamFailed
			}
			var content []ai.AssistantContentBlock
			if script.Text != "" {
				content = append(content, ai.TextContent{Text: script.Text})
			}
			message := &ai.AssistantMessage{Content: content, API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReason(script.Stop)}
			stream := ai.NewAssistantMessageEventStream()
			_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReasonPending}})
			if script.Stop == "error" || script.Stop == "aborted" {
				message.ErrorMessage = script.Stop
				_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReason(script.Stop), Error: message})
			} else {
				_ = stream.Push(ai.DoneEvent{Reason: ai.StopReason(script.Stop), Message: message})
			}
			return stream, nil
		}
		a, err := agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "mock", DisplayName: "mock", Capabilities: ai.ModelCapabilities{ContextWindow: 8192}}, StreamFn: streamFn})
		if err != nil {
			t.Fatal(err)
		}
		var events []any
		a.Subscribe(func(_ context.Context, event agent.AgentEvent) error {
			frames, err := rpcAgentEvent(event)
			if err != nil {
				return err
			}
			for _, frame := range frames {
				raw, _ := json.Marshal(frame)
				var decoded any
				_ = json.Unmarshal(raw, &decoded)
				events = append(events, decoded)
			}
			return nil
		})
		for step, o := range p.Ops {
			events = nil
			var opErr error
			switch o.Op {
			case "prompt":
				opErr = a.Prompt(context.Background(), o.Text)
			case "continue":
				opErr = a.Continue(context.Background())
			case "steer":
				a.Steer(loopUserMessage(o.Text))
			case "followUp":
				a.FollowUp(loopUserMessage(o.Text))
			case "steerMode":
				a.SetSteeringMode(agent.QueueMode(o.Mode))
			case "followMode":
				a.SetFollowUpMode(agent.QueueMode(o.Mode))
			case "clearSteer":
				a.ClearSteeringQueue()
			case "clearFollow":
				a.ClearFollowUpQueue()
			case "clearAll":
				a.ClearAllQueues()
			case "reset":
				_ = a.Reset()
			}
			a.WaitForIdle()
			var messages []any
			for _, m := range a.MessagesSnapshot() {
				wire, err := rpcAgentMessage(m)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(wire)
				var decoded any
				_ = json.Unmarshal(raw, &decoded)
				messages = append(messages, decoded)
			}
			gotError := ""
			if opErr != nil {
				gotError = opErr.Error()
			}
			expected := want[i][step]
			wantError := ""
			if expected.Error != nil {
				wantError = *expected.Error
			}
			got, _ := json.Marshal([]any{gotError, loopStrip(events), loopStrip(messages), a.HasQueuedMessages()})
			exp, _ := json.Marshal([]any{wantError, loopStrip(expected.Events), loopStrip(expected.Messages), expected.Queued})
			if !bytes.Equal(got, exp) {
				if failures++; failures <= 4 {
					first := 0
					for first < len(got) && first < len(exp) && got[first] == exp[first] {
						first++
					}
					from := max(first-150, 0)
					t.Errorf("probe %d step %d (%v):\n turns %v ops %v\n PiG …%s\n Pi  …%s", i, step, o, p.Turns, p.Ops[:step+1], got[from:min(first+300, len(got))], exp[from:min(first+300, len(exp))])
				}
				break
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

var errStreamFailed = streamFailed{}

type streamFailed struct{}

func (streamFailed) Error() string { return "stream failed" }
