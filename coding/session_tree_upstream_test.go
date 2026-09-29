// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

// Matches packages/coding-agent/test/utilities.ts:135 assistantMsg, including usage.
func appendUpstreamTreeAssistant(t *testing.T, sess *Session, text string) string {
	t.Helper()
	id, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "anthropic-messages", Provider: "anthropic", ModelID: "test", Usage: &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/9178-tree-during-compaction.test.ts:18
func TestNavigateTreeRejectsNavigationBeforeActiveLeafCanChange(t *testing.T) {
	sess := newTreeTestSessionWithSettings(t, `{"compaction":{"keepRecentTokens":1}}`)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	withTreeHandlers(sess, t, map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeCompactEvent)
			prep := event.Preparation.(*compaction.CompactionPreparation)
			close(started)
			<-release
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": "summary", "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "details": map[string]any{}}}, nil
		}},
	})
	appendTreeUser(t, sess, "first user")
	target := appendUpstreamTreeAssistant(t, sess, "first assistant")
	appendTreeUser(t, sess, "second user")
	original := appendUpstreamTreeAssistant(t, sess, "second assistant")
	sess.RefreshContext()
	compactDone := make(chan error, 1)
	go func() { compactDone <- sess.Compact(t.Context(), "") }()
	select {
	case <-started:
	case err := <-compactDone:
		t.Fatalf("compact ended before extension handler: %v", err)
	case <-t.Context().Done():
		t.Fatal("handler did not start")
	}
	if !sess.IsCompacting() {
		t.Fatal("session is not compacting")
	}
	_, err := sess.NavigateTree(t.Context(), target, NavigateTreeOptions{})
	if err == nil || err.Error() != "Wait for the current compaction or tree navigation to finish before navigating the session tree." {
		t.Fatalf("navigation error = %v", err)
	}
	if got := treeLeaf(sess); got != original {
		t.Fatalf("leaf = %s, want %s", got, original)
	}
	unblock()
	if err := <-compactDone; err != nil {
		t.Fatal(err)
	}
	entries := sess.inner.Entries()
	last := entries[len(entries)-1]
	if last.Base.Type != "compaction" || last.Base.ParentID == nil || *last.Base.ParentID != original {
		t.Fatalf("compaction = %+v", last.Base)
	}
	if got := assistantMessageTexts(sess.Messages()); !slices.Contains(got, "second assistant") {
		t.Fatalf("retained messages = %q", got)
	}
}
