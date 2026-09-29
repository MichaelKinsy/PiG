// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package compaction

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi completeSummarization propagates a rejected custom stream call without the stop-reason diagnostic prefix added to failed assistant responses.
func TestSummaryStreamExceptionsKeepOriginalError(t *testing.T) {
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "turn prefix"}[split], func(t *testing.T) {
			failure := errors.New("summary generator blew up")
			messages := []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "message to compact"}}}}}
			prep := CompactionPreparation{FirstKeptEntryID: "keep", Settings: CompactionSettings{ReserveTokens: 1000}, MessagesToSummarize: messages}
			if split {
				prep.IsSplitTurn = true
				prep.MessagesToSummarize = nil
				prep.TurnPrefixMessages = messages
			}
			stream := func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
				return "", nil, failure
			}
			_, err := Compact(t.Context(), prep, nil, nil, stream, "", "", nil, "")
			if !errors.Is(err, failure) || err.Error() != failure.Error() {
				t.Fatalf("error=%v; want original %v", err, failure)
			}
		})
	}
}
