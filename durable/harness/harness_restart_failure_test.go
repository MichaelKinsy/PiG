// Ports the part of packages/durable/test/harness-restart-failure.test.ts (Pi b3a29f11) that stays with the reference Harness:
// what a settings source resolves on the normal path, which is Pi's 49 a round. Over the core the count is D206's: it is asserted by
// TestCoreTheRestartLimitOnTheNormalPath in durable/core/driver (harness_restart_failure_core_test.go), which also runs every other
// case over both engines.

package harness

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// countingSettings is a settings source that counts its resolutions.
type countingSettings struct{ reads int }

func (settings *countingSettings) Settings() durable.Settings {
	settings.reads++
	return durable.Settings{RestartLimit: 5}
}

func TestTheRestartLimitOnTheNormalPath(t *testing.T) {
	// #11139: only a task that was cut off reads the limit, so the normal path resolves the settings no more often than
	// before the limit applied to every kind (harness-restart-failure.test.ts counts the resolutions of a whole round).
	t.Run("resolves the settings only for a task that was cut off", func(t *testing.T) {
		settings := &countingSettings{}
		if CutOffTooOften(0, settings) {
			t.Fatal("a task that was never cut off is cut off too often")
		}
		if settings.reads != 0 {
			t.Fatalf("settings resolved %d times for a task that was never cut off, want none", settings.reads)
		}
		if CutOffTooOften(4, settings) || settings.reads != 1 {
			t.Fatalf("4 restarts under a limit of 5: cut off too often, or %d reads, want 1", settings.reads)
		}
		if !CutOffTooOften(5, settings) {
			t.Fatal("5 restarts under a limit of 5 are not cut off too often")
		}
	})
	// harness-restart-failure.test.ts "resolves the settings no more often than before": a round of one generation with one tool call
	// resolves the host settings 49 times in Pi (#11327: 49): 6 agent resolutions, 9 reads of the phases (prepare, request, tool round,
	// progress, the boundaries and the submission) and 34 retention checks, two for each of the 17 idle settlements of a round. Pi counts
	// the reads of `extensions`, which only a full resolution makes; here a resolution is a call of the settings source that is not one of
	// the two single-field reads (the document retention at each commit, the persistence window of a deferred commit). The scheduler's
	// idle settlements follow its goroutines, so their count varies by a few between rounds; the others are exact.
	t.Run("resolves the settings as often as Pi does for a round", func(t *testing.T) {
		setup := chatSetup(t)
		others, retention := 0, 0
		counting := func() *HarnessSettings {
			pcs := make([]uintptr, 12)
			frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
			isRetention := false
			for {
				frame, more := frames.Next()
				if strings.HasSuffix(frame.Function, ").documentRetentionMs") || strings.HasSuffix(frame.Function, ").persistWithinMs") {
					return &HarnessSettings{}
				}
				isRetention = isRetention || strings.HasSuffix(frame.Function, ").contextRetention")
				if !more {
					break
				}
			}
			if isRetention {
				retention++
			} else {
				others++
			}
			return &HarnessSettings{}
		}
		addTool(t, setup.Registry, DefineTool(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "t", Description: "t", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
			Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}}, nil
			},
		}))
		const rounds = 5
		var responses []ai.FauxResponseStep
		for round := 0; round <= rounds; round++ {
			responses = append(responses,
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("t", map[string]any{}, &ai.FauxToolCallOptions{ID: "c"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}))
		}
		setup.Faux.SetResponses(responses)
		harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{Models: setup.Models, Registry: setup.Registry, Settings: counting, OnReport: setup.Reports.add})
		if err != nil {
			t.Fatal(err)
		}
		root, err := harness.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
		if err != nil {
			t.Fatal(err)
		}
		submit := func(text string) {
			submission, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := submission.Wait(testContext); err != nil {
				t.Fatal(err)
			}
		}
		submit("warm up")
		others, retention = 0, 0
		for round := 0; round < rounds; round++ {
			submit("round")
		}
		waitForIdle(t, root)
		if others != 15*rounds {
			t.Fatalf("a round resolved the settings %v times outside the idle checks, want Pi's 15 (6 agents, 9 phases)", float64(others)/rounds)
		}
		// Pi's 34 per round: a settlement that finds nothing to settle is not counted by the scheduler's goroutines.
		if perRound := float64(retention) / rounds; perRound < 30 || perRound > 36 {
			t.Fatalf("a round made %v retention checks, want about Pi's 34", perRound)
		}
		mustClose(t, harness)
	})
}
