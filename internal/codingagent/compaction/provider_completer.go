package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ProviderCompleter collects one summarization response from the caller's stream override or the model's provider. It preserves provider errors, usage and invalid-summary diagnostics.
type ProviderCompleter struct {
	// StreamFn is the caller's stream override; nil streams with the model's provider.
	StreamFn agent.StreamFn
}

func (c ProviderCompleter) CompleteSimple(
	ctx context.Context,
	model *ai.Model,
	systemPrompt string,
	messages []agent.AgentMessage,
	options ai.StreamOptions,
) (string, *ai.Usage, error) {
	llmMessages := make([]ai.Message, 0, len(messages))
	for _, message := range messages {
		switch {
		case message.User != nil:
			llmMessages = append(llmMessages, message.User.LLMMessage())
		case message.Assistant != nil:
			usage := ai.Usage{}
			if message.Assistant.Usage != nil {
				usage = *message.Assistant.Usage
			}
			llmMessages = append(llmMessages, ai.AssistantMessage{
				Content: message.Assistant.Content, API: message.Assistant.API,
				Provider: message.Assistant.Provider, Model: message.Assistant.ModelID,
				ResponseModel: message.Assistant.ResponseModel, ResponseID: message.Assistant.ResponseID,
				Diagnostics: message.Assistant.Diagnostics, Usage: usage,
				StopReason: message.Assistant.StopReason, Deferred: message.Assistant.Deferred,
				ErrorMessage: message.Assistant.ErrorMessage, RawStopReason: message.Assistant.RawStopReason,
				Timestamp: message.Assistant.Timestamp,
			})
		}
	}

	transcript := ai.NormalizeContext(ai.Context{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
	})
	var stream *ai.AssistantMessageEventStream
	var err error
	if c.StreamFn != nil {
		stream, err = c.StreamFn(ctx, model, transcript, options)
	} else {
		stream, err = model.Provider.Stream(ctx, transcript, options)
	}
	if err != nil {
		return "", nil, fmt.Errorf("compaction completer: stream: %w", err)
	}
	message := stream.Result()
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if message.StopReason == ai.StopReasonError {
		detail := message.ErrorMessage
		if detail == "" {
			detail = "Unknown error"
		}
		return "", nil, errors.New(detail)
	}
	if message.StopReason == ai.StopReasonLength {
		return "", nil, errors.New("generation hit the token cap and the summary is incomplete")
	}
	for _, block := range message.Content {
		if _, ok := block.(ai.ToolCall); ok {
			return "", nil, ErrSummarizationToolCall
		}
	}
	var text strings.Builder
	for _, block := range message.Content {
		if block, ok := block.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	usage := message.Usage
	return text.String(), &usage, nil
}
