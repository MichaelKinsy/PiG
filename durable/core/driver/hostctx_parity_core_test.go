// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// The context a model request is prepared and sent from, through the core and its Go host (sqlhost answers the context reads
// and expands the bounds the request names), against the reference harness over memory storage running the same
// conversation. Each provider request is recorded as the role and text of its messages, followed by the kinds of the tasks the
// conversation ran, in id order.

type contextScript func(t *testing.T, root harness.Conversation, turn func(text string))

func runContextScript(t *testing.T, core bool, compaction *harness.CompactionPolicyPatch, script contextScript) [][]string {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 1000, MaxTokens: 900}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	var mu sync.Mutex
	var requests [][]string
	steps := make([]ai.FauxResponseStep, 32)
	for i := range steps {
		steps[i] = ai.FauxFactoryStep(func(c ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			var texts []string
			for _, m := range c.Messages() {
				b, err := json.Marshal(m)
				if err != nil {
					return ai.AssistantMessage{}, err
				}
				texts = append(texts, messageText(t, b))
			}
			mu.Lock()
			requests = append(requests, texts)
			mu.Unlock()
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ok")}, StopReason: "stop"}.AssistantMessage(), nil
		})
	}
	faux.SetResponses(steps)
	settings := &harness.HarnessSettings{Stream: &durable.ConversationStreamOptions{CacheRetention: ai.CacheRetention("none")}, Compaction: compaction}
	options := harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry(), Settings: func() *harness.HarnessSettings { return settings }}
	var h harness.Harness
	var err error
	if core {
		h, err = driver.OpenHarness(t.Context(), filepath.Join(t.TempDir(), "store.db"), options)
	} else {
		h, err = harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), options)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	h.Resume()
	root, err := h.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	turn := func(text string) {
		t.Helper()
		sub, err := root.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
		if err != nil {
			t.Fatal(err)
		}
		if settled, err := sub.Wait(t.Context()); err != nil || settled.Status != "done" {
			t.Fatalf("turn %.20q: %+v %v", text, settled, err)
		}
	}
	script(t, root, turn)
	if err := h.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	var tasks []string
	for id := range durable.TaskId(256) {
		task, err := h.GetTask(t.Context(), id+1)
		if err != nil {
			t.Fatal(err)
		}
		if task != nil {
			tasks = append(tasks, "task "+task.Kind)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return append(slices.Clone(requests), tasks)
}

// sameRequests runs a script on the reference harness and on the core and requires the provider to see the same requests.
func sameRequests(t *testing.T, compaction *harness.CompactionPolicyPatch, script contextScript) [][]string {
	t.Helper()
	want := runContextScript(t, false, compaction, script)
	got := runContextScript(t, true, compaction, script)
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("the core sent\n%q\nthe reference harness sent\n%q", got, want)
	}
	return want
}

// A context behind a head marker (generation.ts prepare and request over runtime.context): after a reset the core names the
// newest head marker and its head, the host derives the messages from the marker on (context.ts selectActive), and the provider
// receives what the reference sends. A core that names no marker, or another marker head, fails the request.
func TestCoreARequestAfterAResetSendsTheContextBehindTheHeadMarker(t *testing.T) {
	requests := sameRequests(t, nil, func(t *testing.T, root harness.Conversation, turn func(string)) {
		for _, text := range []string{"one", "two", "three"} {
			turn(text)
		}
		handoff := "carry on"
		if err := root.Reset(t.Context(), &handoff); err != nil {
			t.Fatal(err)
		}
		turn("four")
	})
	last := strings.Join(requests[len(requests)-2], "\n")
	if len(requests) != 5 || !strings.Contains(last, "carry on") || strings.Contains(last, "user one") {
		t.Fatalf("the request after the reset sent\n%s", last)
	}
}

// thresholdPolicy blocks at 800 of the faux model's 1000-token window and keeps 100 recent tokens.
func thresholdPolicy() *harness.CompactionPolicyPatch {
	return &harness.CompactionPolicyPatch{Enabled: new(true), ReserveTokens: new(200), KeepRecentTokens: new(100), BackgroundTokens: new(0)}
}

// generation.ts thresholdCompaction over the host's estimate: a context over the blocking threshold compacts first only when
// range selection finds a cut (compaction.ts selectCut). One long first message (about 1143 tokens against a blocking
// threshold of 800) has nothing before it to summarize, so the request goes out as it is. The reference shows the policy
// reaches the threshold: a second turn with recent entries to keep compacts first, and the summary request is one more
// provider request. That half runs on the reference only, because the Go driver does not serve the core's `complete` hook
// effect that summarization sends (driver/hook.go).
func TestCoreThresholdCompactionNeedsACutLikeTheReference(t *testing.T) {
	long := strings.Repeat("x", 4000)
	alone := sameRequests(t, thresholdPolicy(), func(_ *testing.T, _ harness.Conversation, turn func(string)) { turn(long) })
	if len(alone) != 2 || slices.Contains(alone[1], "task pi.compaction") {
		t.Fatalf("one turn over the threshold with no cut made %d requests", len(alone))
	}
	cut := runContextScript(t, false, thresholdPolicy(), func(_ *testing.T, _ harness.Conversation, turn func(string)) {
		turn(long)
		turn(strings.Repeat("y", 500))
	})
	if len(cut) != 4 || !slices.Contains(cut[3], "task pi.compaction") {
		t.Fatalf("two turns over the threshold with a cut made %d requests, want the second compacted first", len(cut))
	}
}

// messageText is a message's role and the text of its content: what two encodings of one message agree on.
func messageText(t *testing.T, raw []byte) string {
	t.Helper()
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(m.Content, &text); err != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Content, &blocks)
		for _, b := range blocks {
			text += b.Type + ":" + b.Text + ";"
		}
	}
	return m.Role + " " + text
}
