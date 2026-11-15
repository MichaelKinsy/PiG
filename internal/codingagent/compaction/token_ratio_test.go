package compaction

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
)

func TestSummaryPlanTokenRatio(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 1_000_000, MaxOutputTokens: 128_000}}
	for _, test := range []struct{ measured, want float64 }{
		{1.0, 1.0},
		{1.6, 1.6},
		{0.4, 1.0}, // more than four characters per token is not trusted
		{9, 2.0},   // a corrupt usage is bounded
	} {
		plan, ok := newSummaryPlan(model, 16384, "", test.measured)
		if !ok || plan.ratio != test.want*tokenRatioMargin {
			t.Fatalf("measured %v: ratio %v (ok %v), want %v with the margin", test.measured, plan.ratio, ok, test.want)
		}
	}
	if _, ok := newSummaryPlan(model, 16384, "", 0); ok {
		t.Fatal("a session without a measured ratio has a plan: it is split by the first form")
	}
	if plan, _ := newSummaryPlan(model, 16384, "", 1); plan.fixed < 128_000 || plan.fixed > 128_000+2000 {
		t.Fatalf("fixed tokens %d, want the 128000 output cap and the instructions only", plan.fixed)
	}
}

// A session whose latest assistant usage says what the provider counts sizes the request by it: at four characters per token (ratio 1) the
// history the owner's sessions have, 750k tokens with a 128k output cap in a 1M window, is one request; at two characters per token (ratio 2)
// 400k tokens are.
func TestCompactionOfACalibratedSessionSizesRequestsByItsRatio(t *testing.T) {
	for _, test := range []struct {
		thousandTokens, charsPerToken, wantRequests int
		ratio                                       float64
	}{
		{750, 4, 1, 1.0},
		{800, 4, 1, 1.0},
		{900, 4, 2, 1.0},
		{380, 2, 1, 2.0},
		{480, 2, 2, 2.0},
	} {
		t.Run(fmt.Sprintf("history %dk at %d characters per token", test.thousandTokens, test.charsPerToken), func(t *testing.T) {
			completer := &windowCompleter{window: 1_000_000, outputCap: 128_000, charsPerToken: test.charsPerToken, summaryChars: 4000}
			model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 1_000_000, MaxOutputTokens: 128_000}}
			prep := CompactionPreparation{FirstKeptEntryID: "kept", MessagesToSummarize: overWindowMessages(test.thousandTokens, 4000-15), Settings: CompactionSettings{ReserveTokens: 16384}}
			compactiontypes.SetTokenRatio(&prep, test.ratio)
			result, err := Compact(t.Context(), prep, model, "key", nil, "", "", CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
			if err != nil {
				t.Fatalf("compaction failed: %v", err)
			}
			if len(completer.requests) != test.wantRequests || completer.refused != 0 {
				t.Fatalf("%d summary requests, %d refused, want %d and none", len(completer.requests), completer.refused, test.wantRequests)
			}
			if result.FirstKeptEntryID != "kept" {
				t.Fatalf("first kept entry %q", result.FirstKeptEntryID)
			}
		})
	}
}

// A session without usage is split as the first form of D167 split it: when the estimate is low (a tokenizer of two characters per token) the one
// request is refused, the history is halved and summarized in two requests, the second updating the first's summary: the compaction succeeds, and
// no more requests are refused than that one.
func TestCompactionOfAHistoryTheEstimateUndercountsHalvesAfterTheRefusal(t *testing.T) {
	completer := &windowCompleter{window: 1_000_000, outputCap: 128_000, charsPerToken: 2, summaryChars: 4000}
	result, err := compactHistory(t, completer, overWindowMessages(500, 4000-15), 1_000_000, 128_000, 0)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	if completer.refused != 1 || len(completer.requests) != 3 {
		t.Fatalf("%d requests, %d refused, want 3 and 1", len(completer.requests), completer.refused)
	}
	if result.Summary == "" || result.FirstKeptEntryID != "kept" {
		t.Fatalf("result %+v", result)
	}
}

// When the usage that calibrated the plan understates what the provider counts (it says four characters per token, the provider counts three) and
// the planned request is refused, the rest of the history is planned again by the first form of D167: the compaction succeeds, exactly one request
// is refused, and the requests the provider accepts are those the first form sent for the same history.
func TestCompactionAfterARefusalOfACalibratedRequestContinuesByTheFirstForm(t *testing.T) {
	messages := overWindowMessages(750, 4000-15)
	completer := &windowCompleter{window: 1_000_000, outputCap: 128_000, charsPerToken: 3, summaryChars: 4000}
	result, err := compactHistory(t, completer, messages, 1_000_000, 128_000, 1.0)
	if err != nil {
		t.Fatalf("compaction failed: %v", err)
	}
	// the requests of the first form for the same history, as a session without usage sends them (the carried summaries differ only by number)
	reference := &windowCompleter{window: 1_000_000, outputCap: 128_000, charsPerToken: 3, summaryChars: 4000}
	if _, err := compactHistory(t, reference, messages, 1_000_000, 128_000, 0); err != nil {
		t.Fatalf("first form failed: %v", err)
	}
	if len(reference.requests) < 2 || completer.refused != 1 || len(completer.requests) != len(reference.requests)+1 {
		t.Fatalf("%d requests, %d refused, want the first form's %d and one refused", len(completer.requests), completer.refused, len(reference.requests))
	}
	for index, request := range reference.requests {
		if got := completer.requests[index+1]; len(got) != len(request) {
			t.Fatalf("request %d is %d characters, the first form's is %d", index+1, len(got), len(request))
		}
	}
	if result.Summary == "" || result.FirstKeptEntryID != "kept" {
		t.Fatalf("result %+v", result)
	}
}

// PrepareCompaction measures the ratio on the latest assistant usage: the usage's tokens over the estimate of the messages up to that assistant
// message; a session without usage has none.
func TestPrepareCompactionMeasuresTheProviderTokenRatio(t *testing.T) {
	entries := func(totalTokens int) []codingagent.SessionEntry {
		assistant := assistantEntry("a1", fmt.Sprintf("%040000d", 1))
		if totalTokens > 0 {
			assistant["message"].(map[string]any)["usage"] = map[string]any{"input": totalTokens - 10, "output": 10, "cacheRead": 0, "cacheWrite": 0, "totalTokens": totalTokens}
		}
		return []codingagent.SessionEntry{
			mustSessionEntry(t, userEntry("u1", fmt.Sprintf("%040000d", 2))),
			mustSessionEntry(t, assistant),
			mustSessionEntry(t, userEntry("u2", "next")),
			mustSessionEntry(t, assistantEntry("a2", "done")),
		}
	}
	settings := DefaultCompactionSettings
	settings.KeepRecentTokens = 1
	// two messages of 40,000 characters: 20,000 tokens by the estimate; the provider counted 30,000
	prep := PrepareCompaction(entries(30_000), settings)
	if prep == nil || compactiontypes.TokenRatioOf(*prep) < 1.49 || compactiontypes.TokenRatioOf(*prep) > 1.51 {
		t.Fatalf("prep %+v, want a ratio of 1.5", prep)
	}
	if prep := PrepareCompaction(entries(0), settings); prep == nil || compactiontypes.TokenRatioOf(*prep) != 0 {
		t.Fatalf("prep %+v, want no ratio without usage", prep)
	}
}
