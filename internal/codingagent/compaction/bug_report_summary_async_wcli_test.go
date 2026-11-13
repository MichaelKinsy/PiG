package compaction

// pi: packages/coding-agent/src/core/bug-report.ts

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type bugSummaryCtxKey struct{}

// Pi bug-report.ts:347,356 passes options.signal to completeSummarization, and an aborted signal wins over whatever error the completer reported: the call is
// awaited, the caller context reaches the completer, and a completer that fails with its own error after the abort still yields the cancellation message.
func TestGenerateBugReportSummaryForwardsCallerContextAndAbortWinsOverError(t *testing.T) {
	model := &ai.Model{ID: "m"}
	ctx := context.WithValue(context.Background(), bugSummaryCtxKey{}, "caller")
	var seen any
	completer := simpleCompleterFunc(func(ctx context.Context, _ *ai.Model, _ string, _ []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
		seen = ctx.Value(bugSummaryCtxKey{})
		return "a summary", nil, nil
	})
	if got, err := GenerateBugReportSummary(ctx, GenerateBugReportSummaryOptions{Model: model, Completer: completer}); err != nil || got != "a summary" {
		t.Fatalf("summary = %q, %v", got, err)
	}
	if seen != "caller" {
		t.Fatalf("the completer ran under %v, want the caller context", seen)
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	failing := simpleCompleterFunc(func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
		cancel()
		return "", nil, errors.New("upstream 500")
	})
	if _, err := GenerateBugReportSummary(cancelCtx, GenerateBugReportSummaryOptions{Model: model, Completer: failing}); err == nil || err.Error() != "Bug report summary was cancelled" {
		t.Fatalf("err = %v, want the cancellation message although the completer failed differently", err)
	}
	cancelled := simpleCompleterFunc(func(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
		return "text after abort", nil, nil
	})
	aborted, abort := context.WithCancel(ctx)
	abort()
	if got, err := GenerateBugReportSummary(aborted, GenerateBugReportSummaryOptions{Model: model, Completer: cancelled}); err == nil || got != "" {
		t.Fatalf("summary = %q, %v; an aborted signal must not return text", got, err)
	}
}
