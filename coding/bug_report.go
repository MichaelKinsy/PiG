package coding

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// SummarizeForBugReport asks the session model for a bug report summary of the
// current conversation. /bug calls it only after the user chose a summary
// instead of attaching the transcript. Mirrors upstream
// AgentSession.summarizeForBugReport.
//
// The request takes the summarization auth and routing of compaction: a virtual model is routed first and its thinking level applies
// (agent-session.ts:4307-4321). Its retries emit no summarization retry events, because Pi passes no retry callbacks.
func (s *Session) SummarizeForBugReport(ctx context.Context, hint string) (string, error) {
	model := s.Model()
	if model == nil {
		return "", errors.New("No model selected")
	}
	request, err := s.prepareSummarizationRequest(ctx, model)
	if err != nil {
		return "", err
	}
	retry, _ := s.summarizationRetryOptions("bug-report", "manual")
	return compaction.GenerateBugReportSummary(ctx, compaction.GenerateBugReportSummaryOptions{
		Messages:      s.Messages(),
		Hint:          hint,
		Model:         request.model,
		APIKey:        request.apiKey,
		Headers:       request.headers,
		Env:           request.env,
		Completer:     request.completer,
		StreamFn:      request.streamFn,
		Retry:         retry,
		ThinkingLevel: request.thinkingLevel,
		SessionID:     s.ID(),
	})
}
