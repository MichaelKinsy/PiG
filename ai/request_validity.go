package ai

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// checkRequestBody validates the JSON body a provider is about to send against the API's message contract. It does nothing outside a test binary or a requestvalidity-tagged build.
func checkRequestBody(ctx context.Context, api API, body []byte) {
	if !requestvalidity.Enabled() {
		return
	}
	requestvalidity.Enforce(ctx, string(api), requestvalidity.CheckBody(string(api), body))
}

// checkRequestSegments is checkRequestBody for a body held in segments; it joins them only when the check runs.
func checkRequestSegments(ctx context.Context, api API, body *requestBody) {
	if !requestvalidity.Enabled() {
		return
	}
	checkRequestBody(ctx, api, body.Bytes())
}

// checkTranscriptFlow validates the transcript a provider receives, after transformMessages, against Pi's tool-flow contract. Faux providers and pi-messages never serialize a provider body, so this is the check they get. Real providers get it too, before their converter runs.
func checkTranscriptFlow(ctx context.Context, transcript TranscriptContext) {
	if !requestvalidity.Enabled() || transcript.err != nil {
		return
	}
	model := &Model{Input: []string{"text", "image"}}
	messages := TransformMessages(slices.Clone(transcript.messages), model, nil)
	turns := make([]requestvalidity.Turn, 0, len(messages))
	for _, message := range messages {
		switch message := message.(type) {
		case SystemMessage:
			turns = append(turns, requestvalidity.Turn{Role: "system"})
		case UserMessage:
			turns = append(turns, requestvalidity.Turn{Role: "user"})
		case AssistantMessage:
			turn := requestvalidity.Turn{Role: "assistant"}
			for _, block := range message.Content {
				if call, ok := block.(ToolCall); ok {
					turn.Calls = append(turn.Calls, call.ID)
				}
			}
			turns = append(turns, turn)
		case ToolResultMessage:
			turns = append(turns, requestvalidity.Turn{Role: "tool", Results: []string{message.ToolCallID}})
		}
	}
	requestvalidity.Enforce(ctx, "transcript", requestvalidity.Transcript.Check(turns))
}
