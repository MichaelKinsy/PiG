package extensionconformance

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

// TestProviderStreamEventsShowThePartialAsPushed: a provider appends to its one partial message and pushes it, with nothing between the two that lets the SDK's forwarder run,
// 100 times and then once more after the last push. The host receives the i-th text_delta with i "x" in its partial, the start with none, and the done with all of them plus the
// tail: the message as it was at each push, which is Pi's wire form when real I/O separates the pushes (as it does between the network reads of Pi's providers) and stronger than
// Pi's after a synchronous burst (D613). An SDK that reads the author's message when its forwarder thread gets to the event, rather than when the author pushes it, shows later
// text and races the author's writes (go test -race reports it for the Go SDK).
func TestProviderStreamEventsShowThePartialAsPushed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, source string
		fused        bool
		packed       bool
	}{
		{"go-isolated", "testdata/provider-go", false, false}, {"go-packed", "testdata/provider-go", false, true},
		{"python-isolated", "testdata/provider-python", false, false}, {"python-packed", "testdata/provider-python", false, true},
		{"rust-isolated", "testdata/provider-rust", false, false}, {"rust-packed", "testdata/provider-rust", false, true},
		{"lisp-isolated", "testdata/provider-lisp", false, false}, {"lisp-packed", "testdata/provider-lisp", false, true},
		{"node-isolated", "testdata/provider-snapshot.mjs", false, false}, {"node-packed", "testdata/provider-snapshot.mjs", false, true},
		{"fused-go", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			host := subprocess.NewHost(t.TempDir())
			defer host.Shutdown("test done")
			host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
			if err := loadProviderFixture(t, host, tc.name, tc.source, tc.fused, tc.packed); err != nil {
				t.Fatal(err)
			}
			applyQueuedRegistrations(host)
			model := services.ModelRuntime().GetModel("snapshot-provider", "faux")
			if model == nil {
				t.Fatal("snapshot-provider/faux is not registered")
			}
			stream := services.ModelRuntime().Stream(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}}, ai.StreamOptions{APIKey: "extension-key"})
			var starts, dones []string
			var deltas []string
			for event := range stream.Events(t.Context()) {
				text, err := partialText(event)
				if err != nil {
					t.Fatal(err)
				}
				switch event.EventType() {
				case ai.EventStart:
					starts = append(starts, text)
				case ai.EventTextDelta:
					deltas = append(deltas, text)
				case ai.EventDone:
					dones = append(dones, text)
				}
			}
			wantStart, wantDeltas, wantDone := testfixture.ProviderSnapshotWant()
			if fmt.Sprint(starts) != fmt.Sprint([]string{wantStart}) {
				t.Errorf("start events show %q, want [%q]", starts, wantStart)
			}
			if len(deltas) != len(wantDeltas) {
				t.Fatalf("got %d text_delta events, want %d", len(deltas), len(wantDeltas))
			}
			wrong := 0
			for i, want := range wantDeltas {
				if deltas[i] != want {
					if wrong++; wrong <= 3 {
						t.Errorf("text_delta %d shows %d characters %q, want %d (the author had pushed that many)", i+1, len(deltas[i]), deltas[i], len(want))
					}
				}
			}
			if wrong > 3 {
				t.Errorf("%d of %d text_delta events show a later partial than the author pushed", wrong, len(wantDeltas))
			}
			if fmt.Sprint(dones) != fmt.Sprint([]string{wantDone}) {
				t.Errorf("done events show %q, want [%q]", dones, wantDone)
			}
		})
	}
}

// partialText is the text of the first content block of an event's partial message (the message of a done event), read from the event's JSON.
func partialText(event ai.AssistantMessageEvent) (string, error) {
	raw, err := json.Marshal(event)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Partial *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"partial"`
		Message *struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	switch {
	case decoded.Partial != nil && len(decoded.Partial.Content) > 0:
		return decoded.Partial.Content[0].Text, nil
	case decoded.Message != nil && len(decoded.Message.Content) > 0:
		return decoded.Message.Content[0].Text, nil
	}
	return "", nil
}
