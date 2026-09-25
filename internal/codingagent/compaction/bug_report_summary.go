package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// bugSummarySystemPrompt and bugSummaryInstructions mirror upstream
// core/bug-report.ts with PiG's product name (D2).
const bugSummarySystemPrompt = `You are helping a user file a bug report about pig, the coding agent they are talking to. You will be shown the conversation transcript. Write a report for the pig developers describing what the user was doing and what went wrong.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the report.`

const bugSummaryInstructions = `Write the bug report in Markdown with these sections:

## What the user was doing
One short paragraph.

## What went wrong
Concrete description of the failure: wrong output, errors, hangs, tool failures, unexpected behavior. Quote error messages and tool output verbatim where they exist.

## Steps to reproduce
Numbered list, as specific as the transcript allows.

## Relevant details
Tool calls involved, files touched, model behavior, anything else that helps a developer reproduce or locate the problem.

Do not include file contents, secrets, or credentials from the transcript; refer to files by path only. Keep the report factual and concise.`

// upstream: coding-agent/src/core/bug-report.ts:generateBugReportSummary
const (
	bugSummaryDefaultContextWindow = 128_000
	bugSummaryMaxTokens            = 4096
)

// GenerateBugReportSummaryOptions configures GenerateBugReportSummary.
type GenerateBugReportSummaryOptions struct {
	Messages  []agent.AgentMessage
	Hint      string
	Model     *ai.Model
	Completer SimpleCompleter
	StreamFn  StreamFn
	Retry     *RetryOptions
	// ThinkingLevel applies to reasoning-capable models.
	ThinkingLevel ai.ThinkingLevel
	// SessionID is the routing session ID forwarded without prompt caching.
	SessionID string
}

// selectBugReportMessages keeps the newest messages that fit tokenBudget,
// always keeping at least the last one, in original order.
func selectBugReportMessages(messages []agent.AgentMessage, tokenBudget int) []agent.AgentMessage {
	start := len(messages)
	tokens := 0
	for start > 0 {
		next := EstimateTokens(messages[start-1])
		if start < len(messages) && tokens+next > tokenBudget {
			break
		}
		tokens += next
		start--
	}
	return messages[start:]
}

// GenerateBugReportSummary asks the session model for a report when the user
// does not share the transcript. Mirrors upstream generateBugReportSummary.
func GenerateBugReportSummary(ctx context.Context, opts GenerateBugReportSummaryOptions) (string, error) {
	model := opts.Model
	if model == nil {
		return "", errors.New("No model selected")
	}
	contextWindow := model.Capabilities.ContextWindow
	if contextWindow <= 0 {
		contextWindow = bugSummaryDefaultContextWindow
	}
	selected := selectBugReportMessages(opts.Messages, contextWindow*6/10)
	var parts []string
	if len(selected) < len(opts.Messages) {
		parts = append(parts, fmt.Sprintf("Note: only the last %d of %d messages are shown.", len(selected), len(opts.Messages)))
	}
	parts = append(parts, "<conversation>\n"+SerializeConversation(convertToLlm(selected))+"\n</conversation>")
	if hint := strings.TrimSpace(opts.Hint); hint != "" {
		parts = append(parts, "<user-report>\n"+hint+"\n</user-report>")
	}
	parts = append(parts, bugSummaryInstructions)
	request := []agent.AgentMessage{{
		User: &agent.UserMessage{
			Role:      "user",
			Content:   []ai.UserContentBlock{ai.TextContent{Text: strings.Join(parts, "\n\n")}},
			Timestamp: time.Now().UnixMilli(),
		},
	}}
	maxTokens := bugSummaryMaxTokens
	if model.Capabilities.MaxOutputTokens > 0 {
		maxTokens = min(maxTokens, model.Capabilities.MaxOutputTokens)
	}
	options := createSummarizationOptions(model, maxTokens, opts.ThinkingLevel, opts.SessionID)
	text, _, err := completeSummarization(ctx, model, opts.Completer, opts.StreamFn, opts.Retry, bugSummarySystemPrompt, request, options)
	if ctx.Err() != nil {
		return "", errors.New("Bug report summary was cancelled")
	}
	if err != nil {
		return "", summarizationFailure("Bug report summary", err)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("Bug report summary was empty")
	}
	return text, nil
}
