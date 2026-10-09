// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type recordedCompactionEvent struct {
	kind, reason string
	willRetry    bool
}

func summaryOverrideHandlers(summary string, recorded *[]recordedCompactionEvent) map[string][]extension.HandlerFn {
	return map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeCompactEvent)
			prep := event.Preparation
			if recorded != nil {
				*recorded = append(*recorded, recordedCompactionEvent{event.Type, event.Reason, event.WillRetry})
			}
			return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: summary, FirstKeptEntryID: prep.FirstKeptEntryID, TokensBefore: prep.TokensBefore, Details: map[string]any{}}}, nil
		}},
		"session_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionCompactEvent)
			if recorded != nil {
				*recorded = append(*recorded, recordedCompactionEvent{event.Type, event.Reason, event.WillRetry})
			}
			return nil, nil
		}},
	}
}

func TestCompactionExtensionReasonsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		retry        bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5217-compaction-reason.test.ts:55
		{"reports manual reason for compact", "manual", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5217-compaction-reason.test.ts:68
		{"reports threshold reason for auto-compaction", "threshold", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5217-compaction-reason.test.ts:82
		{"reports overflow reason and willRetry for overflow recovery", "overflow", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := autoQueueSession(t, true)
			p := &scriptedProvider{responses: []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0)}}
			model := fakeModelWithProvider(p)
			model.ID = "faux-1"
			model.Capabilities.ContextWindow = 200000
			s.agent.SetModel(model)
			var recorded []recordedCompactionEvent
			withTreeHandlers(s, t, summaryOverrideHandlers("summary from extension", &recorded))
			for _, prompt := range []string{"first", "second"} {
				if _, err := s.Send(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
				drainEvents(t, s)
			}
			if tc.reason == "manual" {
				if _, err := s.Compact(t.Context(), ""); err != nil {
					t.Fatal(err)
				}
			} else {
				if compacted, err := s.runAutoCompaction(t.Context(), tc.reason, tc.retry); !compacted || err != nil {
					t.Fatal("auto-compaction failed")
				}
			}
			if want := []recordedCompactionEvent{{"session_before_compact", tc.reason, tc.retry}, {"session_compact", tc.reason, tc.retry}}; !reflect.DeepEqual(recorded, want) {
				t.Fatalf("events=%v, want %v", recorded, want)
			}
		})
	}
}
