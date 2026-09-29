// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package compaction

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/utils"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/compaction/compaction.ts (summary request boundaries).
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

// SummaryRequest is a caller-owned, awaited provider-request boundary. The context carries cancellation and harness telemetry.
type SummaryRequest func(context.Context, ai.Context, ai.StreamOptions) (*ai.AssistantMessage, error)
type SummaryGenerationOptions struct {
	Model                               *ai.Model
	ReserveTokens                       int
	CustomInstructions, PreviousSummary string
	ThinkingLevel                       ai.ThinkingLevel
}
type CompactGenerationOptions struct {
	Model              *ai.Model
	CustomInstructions string
	ThinkingLevel      ai.ThinkingLevel
}

// CreateSummaryRequestOptions isolates routing and disables cache writes for a standalone summary.
func CreateSummaryRequestOptions(options ai.StreamOptions) ai.StreamOptions {
	options.CacheRetention = ai.CacheRetentionNone
	if options.SessionID == "" {
		options.SessionID = uuid.Must(uuid.NewV7()).String()
	}
	return options
}
func CompleteSimpleWithRetries(ctx context.Context, models Models, model *ai.Model, request ai.Context, options ai.StreamOptions, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) (*ai.AssistantMessage, error) {
	options = CreateSummaryRequestOptions(options)
	response, err := ai.RetryAssistantCall(ctx, func() (ai.AssistantMessage, error) {
		response := models.CompleteSimple(ctx, model, request, options)
		if response == nil {
			return ai.AssistantMessage{}, fmt.Errorf("summary request returned no assistant message")
		}
		return *response, nil
	}, retry, callbacks)
	if err != nil {
		return nil, err
	}
	return &response, nil
}
func GenerateSummary(ctx context.Context, messages []agent.AgentMessage, models Models, model *ai.Model, reserve int, custom, previous string, thinking ai.ThinkingLevel, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) (string, error) {
	result, err := GenerateSummaryWithUsage(ctx, messages, models, model, reserve, custom, previous, thinking, retry, callbacks)
	return result.Text, err
}
func GenerateSummaryWithUsage(ctx context.Context, messages []agent.AgentMessage, models Models, model *ai.Model, reserve int, custom, previous string, thinking ai.ThinkingLevel, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) (SummaryResult, error) {
	return GenerateSummaryWithRequest(ctx, messages, SummaryGenerationOptions{Model: model, ReserveTokens: reserve, CustomInstructions: custom, PreviousSummary: previous, ThinkingLevel: thinking}, func(ctx context.Context, request ai.Context, options ai.StreamOptions) (*ai.AssistantMessage, error) {
		return CompleteSimpleWithRetries(ctx, models, model, request, options, retry, callbacks)
	})
}
func summaryRequestOptions(model *ai.Model, maxTokens int, thinking ai.ThinkingLevel) ai.StreamOptions {
	if model.Capabilities.MaxOutputTokens > 0 {
		maxTokens = min(maxTokens, model.Capabilities.MaxOutputTokens)
	}
	options := ai.StreamOptions{MaxTokens: maxTokens}
	if (model.Capabilities.MaxThinking != "" || model.ProviderMeta.Reasoning) && thinking != "" && thinking != ai.ThinkingOff {
		options.Thinking = thinking
		options.IsReasoning = true
	}
	return CreateSummaryRequestOptions(options)
}
func summaryResponseResult(response *ai.AssistantMessage, operation string) (SummaryResult, error) {
	if response == nil {
		return SummaryResult{}, fmt.Errorf("summary request returned no assistant message")
	}
	if response.StopReason == ai.StopReasonAborted {
		message := response.ErrorMessage
		if message == "" {
			message = operation + " aborted"
		}
		return SummaryResult{}, &harness.CompactionError{Code: harness.CompactionErrorAborted, Message: message}
	}
	if response.StopReason == ai.StopReasonError {
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return SummaryResult{}, &harness.CompactionError{Code: harness.CompactionErrorSummarizationFailed, Message: operation + " failed: " + message}
	}
	var text []string
	for _, block := range response.Content {
		if block, ok := block.(ai.TextContent); ok {
			text = append(text, block.Text)
		}
	}
	return SummaryResult{Text: strings.Join(text, "\n"), Usage: response.Usage}, nil
}
func summaryContext(prompt string) ai.Context {
	return ai.Context{SystemPrompt: SummarizationSystemPrompt, Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: prompt}}, Timestamp: time.Now().UnixMilli()}}}
}

// GenerateSummaryWithRequest preserves coded failed/aborted responses and propagates request errors without converting them into response failures.
func GenerateSummaryWithRequest(ctx context.Context, messages []agent.AgentMessage, options SummaryGenerationOptions, request SummaryRequest) (SummaryResult, error) {
	base := summarizationPrompt
	if options.PreviousSummary != "" {
		base = updateSummarizationPrompt
	}
	if options.CustomInstructions != "" {
		base += "\n\nAdditional focus: " + options.CustomInstructions
	}
	prompt := "<conversation>\n" + SerializeConversation(convertToLlm(messages)) + "\n</conversation>\n\n"
	if options.PreviousSummary != "" {
		prompt += "<previous-summary>\n" + options.PreviousSummary + "\n</previous-summary>\n\n"
	}
	prompt += base
	response, err := request(ctx, summaryContext(prompt), summaryRequestOptions(options.Model, int(math.Floor(.8*float64(options.ReserveTokens))), options.ThinkingLevel))
	if err != nil {
		return SummaryResult{}, err
	}
	return summaryResponseResult(response, "Summarization")
}
func generateTurnPrefixSummary(ctx context.Context, messages []agent.AgentMessage, model *ai.Model, reserve int, thinking ai.ThinkingLevel, request SummaryRequest) (SummaryResult, error) {
	prompt := "<conversation>\n" + SerializeConversation(convertToLlm(messages)) + "\n</conversation>\n\n" + turnPrefixSummarizationPrompt
	response, err := request(ctx, summaryContext(prompt), summaryRequestOptions(model, int(math.Floor(.5*float64(reserve))), thinking))
	if err != nil {
		return SummaryResult{}, err
	}
	return summaryResponseResult(response, "Turn prefix summarization")
}
func Compact(ctx context.Context, preparation CompactionPreparation, models Models, model *ai.Model, custom string, thinking ai.ThinkingLevel, retry *ai.RetryPolicy, callbacks ai.RetryCallbacks) (CompactResult, error) {
	return CompactWithRequest(ctx, preparation, CompactGenerationOptions{Model: model, CustomInstructions: custom, ThinkingLevel: thinking}, func(ctx context.Context, request ai.Context, options ai.StreamOptions) (*ai.AssistantMessage, error) {
		return CompleteSimpleWithRetries(ctx, models, model, request, options, retry, callbacks)
	})
}

// CompactWithRequest generates history and optional turn-prefix summaries in order, then retains the prepared tail verbatim.
func CompactWithRequest(ctx context.Context, p CompactionPreparation, options CompactGenerationOptions, request SummaryRequest) (CompactResult, error) {
	historyOptions := SummaryGenerationOptions{Model: options.Model, ReserveTokens: p.Settings.ReserveTokens, CustomInstructions: options.CustomInstructions, PreviousSummary: p.PreviousSummary, ThinkingLevel: options.ThinkingLevel}
	var summary string
	var usage ai.Usage
	if p.IsSplitTurn && len(p.TurnPrefixMessages) > 0 {
		history := "No prior history."
		var historyUsage *ai.Usage
		if len(p.MessagesToSummarize) > 0 {
			result, err := GenerateSummaryWithRequest(ctx, p.MessagesToSummarize, historyOptions, request)
			if err != nil {
				return CompactResult{}, err
			}
			history = result.Text
			historyUsage = &result.Usage
		}
		prefix, err := generateTurnPrefixSummary(ctx, p.TurnPrefixMessages, options.Model, p.Settings.ReserveTokens, options.ThinkingLevel, request)
		if err != nil {
			return CompactResult{}, err
		}
		summary = history + "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefix.Text
		usage = prefix.Usage
		if historyUsage != nil {
			usage = utils.AddUsage(*historyUsage, prefix.Usage)
		}
	} else {
		result, err := GenerateSummaryWithRequest(ctx, p.MessagesToSummarize, historyOptions, request)
		if err != nil {
			return CompactResult{}, err
		}
		summary = result.Text
		usage = result.Usage
	}
	read, modified := ComputeFileLists(p.FileOps)
	summary += FormatFileOperations(read, modified)
	return CompactResult{Summary: summary, TokensBefore: p.TokensBefore, Usage: &usage, RetainedTail: p.RetainedTail, Details: CompactionDetails{ReadFiles: read, ModifiedFiles: modified}}, nil
}
