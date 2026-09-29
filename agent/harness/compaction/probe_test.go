// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package compaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func probeLine(t *testing.T, kind string, value any) {
	t.Helper()
	fmt.Print(kind + " ")
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
}
func probeTexts(messages []agent.AgentMessage) []string {
	result := []string{}
	for _, message := range messages {
		if message.User != nil {
			for _, block := range message.ContentBlocks() {
				if block, ok := block.(ai.TextContent); ok {
					result = append(result, block.Text)
				}
			}
		} else if message.Assistant != nil {
			for _, block := range message.Assistant.Content {
				if block, ok := block.(ai.TextContent); ok {
					result = append(result, block.Text)
				}
			}
		}
	}
	return result
}

func TestHarnessCompactionProbe(t *testing.T) {
	var f entryFactory
	c := f.compact("previous summary", nil, user("retained user"), assistant("retained assistant"))
	u := f.message(user("new user"), c.ID)
	a := f.message(assistant("new assistant"), u.ID)
	p := preparation(t, []session.Entry{c, u, a}, CompactionSettings{Enabled: true, ReserveTokens: 100, KeepRecentTokens: 1})
	probeLine(t, "HARNESS_PREP", struct {
		Previous string   `json:"previous"`
		Split    bool     `json:"split"`
		Tokens   int      `json:"tokens"`
		History  []string `json:"history"`
		Prefix   []string `json:"prefix"`
		Tail     []string `json:"tail"`
	}{p.PreviousSummary, p.IsSplitTurn, p.TokensBefore, probeTexts(p.MessagesToSummarize), probeTexts(p.TurnPrefixMessages), probeTexts(p.RetainedTail)})
	messages := []agent.AgentMessage{user("Summarize this.")}
	prep := generationPreparation(messages, true)
	prep.MessagesToSummarize = messages
	prep.Settings.ReserveTokens = 500000
	prep.FileOps.Read["\ue000"] = struct{}{}
	prep.FileOps.Read["\U00010000"] = struct{}{}
	prep.FileOps.Written["write.ts"] = struct{}{}
	models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("history summary", ai.StopReasonStop, ""), summaryResponse("turn prefix summary", ai.StopReasonStop, "")}}
	models.responses[0].Usage = *mockUsage(1, 2, 3, 4)
	models.responses[1].Usage = *mockUsage(5, 6, 7, 8)
	result, err := Compact(t.Context(), prep, models, summaryModel(true, 128000), "", ai.ThinkingHigh, nil, ai.RetryCallbacks{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage == nil || len(models.options) != 2 {
		t.Fatal("missing usage or requests")
	}
	usage := result.Usage
	probeLine(t, "HARNESS_RESULT", struct {
		Summary  string `json:"summary"`
		Tokens   int    `json:"tokensBefore"`
		Retained int    `json:"retained"`
		Usage    []int  `json:"usage"`
		Details  any    `json:"details"`
	}{result.Summary, result.TokensBefore, len(result.RetainedTail), []int{usage.Input, usage.Output, usage.CacheRead, usage.CacheWrite, usage.TotalTokens}, result.Details})
	type requestRecord struct {
		System    string            `json:"system"`
		Text      string            `json:"text"`
		MaxTokens int               `json:"maxTokens"`
		Thinking  ai.ThinkingLevel  `json:"thinking"`
		Cache     ai.CacheRetention `json:"cacheRetention"`
	}
	var requests []requestRecord
	for i, request := range models.requests {
		one := &summaryModels{requests: []ai.Context{request}}
		options := models.options[i]
		requests = append(requests, requestRecord{request.SystemPrompt, requestUserText(t, one), options.MaxTokens, options.Thinking, options.CacheRetention})
	}
	probeLine(t, "HARNESS_REQUESTS", requests)
	probeLine(t, "HARNESS_ROUTING", models.options[0].SessionID != models.options[1].SessionID)
	failed := assistant("failed attempt")
	failed.Assistant.StopReason = ai.StopReasonError
	call := assistant("")
	call.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "orphan", Name: "read", Arguments: ai.JsonObject{"path": "file.ts"}}}
	historyModels := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("summary", ai.StopReasonStop, "")}}
	if _, err := GenerateSummary(t.Context(), []agent.AgentMessage{user("task"), failed, call}, historyModels, summaryModel(false, 8192), 2000, "", "", "", nil, ai.RetryCallbacks{}); err != nil {
		t.Fatal(err)
	}
	history, _, _ := strings.Cut(strings.TrimPrefix(requestUserText(t, historyModels), "<conversation>\n"), "\n</conversation>")
	probeLine(t, "HARNESS_HISTORY", history)
	for _, test := range []struct {
		reason  ai.StopReason
		message string
	}{{ai.StopReasonError, "boom"}, {ai.StopReasonAborted, "stopped"}} {
		models := &summaryModels{responses: []ai.AssistantMessage{summaryResponse("", test.reason, test.message)}}
		_, err := GenerateSummary(t.Context(), messages, models, summaryModel(false, 8192), 2000, "", "", "", nil, ai.RetryCallbacks{})
		var failure *harness.CompactionError
		if !errors.As(err, &failure) {
			t.Fatalf("error=%v", err)
		}
		probeLine(t, "HARNESS_ERROR", struct {
			Code    harness.CompactionErrorCode `json:"code"`
			Message string                      `json:"message"`
		}{failure.Code, failure.Message})
	}
}
