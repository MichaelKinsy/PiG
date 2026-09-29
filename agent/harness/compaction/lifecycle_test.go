// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package compaction

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

type requestTelemetry struct{ harness.TelemetryContext }

// A rejected first summary prevents the prefix request; the owned cancellation and telemetry context reach the request unchanged.
func TestHarnessSummaryRequestOwnership(t *testing.T) {
	telemetry := &requestTelemetry{TelemetryContext: harness.NoopTelemetryContext}
	ctx, cancel := context.WithCancel(harness.WithTelemetryContext(t.Context(), telemetry))
	defer cancel()
	messages := []agent.AgentMessage{user("Summarize this.")}
	prep := generationPreparation(messages, true)
	prep.MessagesToSummarize = messages
	entered := make(chan struct{})
	calls := 0
	request := func(received context.Context, _ ai.Context, _ ai.StreamOptions) (*ai.AssistantMessage, error) {
		calls++
		if received != ctx || harness.GetTelemetryContext(received) != telemetry {
			t.Error("request lost its owner context")
		}
		close(entered)
		<-received.Done()
		return nil, received.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := CompactWithRequest(ctx, prep, CompactGenerationOptions{Model: summaryModel(false, 8192)}, request)
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("requests=%d", calls)
	}
}

func BenchmarkPrepareHarnessCompaction(b *testing.B) {
	var f entryFactory
	entries := make([]session.Entry, 0, 2000)
	parent := ""
	for range 1000 {
		u := f.message(user("task to summarize"))
		if parent != "" {
			u.ParentID = new(parent)
		}
		a := f.message(assistant("completed task"), u.ID)
		entries = append(entries, u, a)
		parent = a.ID
	}
	b.ReportAllocs()
	for b.Loop() {
		if result, err := PrepareCompaction(entries, DefaultCompactionSettings); err != nil || result == nil {
			b.Fatalf("result=%+v error=%v", result, err)
		}
	}
}
