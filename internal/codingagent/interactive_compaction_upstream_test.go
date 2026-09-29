// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type compactionUIHandle struct {
	recordingCompactHandle
	aborts    int
	inputs    []string
	behaviors []string
}

func (h *compactionUIHandle) RequestAbort() { h.aborts++ }
func (h *compactionUIHandle) RunInputHandlers(_ context.Context, text string, images []ai.ImageContent, _ extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
	h.inputs = append(h.inputs, text)
	h.behaviors = append(h.behaviors, behavior)
	return text, images, false, nil
}
func compactionUIMode(t *testing.T, notices bool) *InteractiveMode {
	t.Helper()
	session := NewSession("ui", t.TempDir())
	m := cacheWarmingTestMode(t, session, notices)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.statusContainer = tui.NewContainer()
	m.opts.Settings.ShowTerminalProgress = new(false)
	m.toolsExpanded = true
	m.runCtx = t.Context()
	m.abortCtx, m.abortFn = context.WithCancel(t.Context())
	t.Cleanup(m.abortFn)
	return m
}
func compactionUIEntry(t *testing.T, id, parent, summary string, usage *ai.Usage) SessionEntry {
	t.Helper()
	return contextFixtureEntry(t, id, parent, "compaction", map[string]any{"summary": summary, "firstKeptEntryId": "kept", "tokensBefore": 100, "usage": usage})
}
func compactionUIUsage(total float64) *ai.Usage {
	return &ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: .01, Output: .02, CacheRead: .03, CacheWrite: .065, Total: total}}
}

func TestInteractiveCompactionUpstream(t *testing.T) {
	saved := tuiCapabilitiesForTest()
	t.Cleanup(saved.restore)
	tuiSetCapsForTest(false)
	run := func(name string, test func(*testing.T)) {
		t.Run(name, func(t *testing.T) { synctest.Test(t, func(t *testing.T) { test(t); synctest.Wait() }) })
	}
	{
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:10
		run("uses cache miss notice setting for compaction and branch summary costs", func(t *testing.T) {
			for _, enabled := range []bool{true, false} {
				m := compactionUIMode(t, enabled)
				usage := compactionUIUsage(.125)
				entries := []SessionEntry{compactionUIEntry(t, "c", "", "summary", usage), contextFixtureEntry(t, "b", "c", "branch_summary", map[string]any{"summary": "branch", "fromId": "old", "usage": usage})}
				m.renderSessionEntryList(entries, false)
				output := stripANSI(strings.Join(m.chatContainer.Render(120), "\n"))
				for _, text := range []string{"Compaction: 100 tokens billed (~$0.13)", "Branch summary: 100 tokens billed (~$0.13)"} {
					if strings.Contains(output, text) != enabled {
						t.Fatalf("enabled=%v missing/unexpected %q: %s", enabled, text, output)
					}
				}
				if !enabled {
					m.chatContainer.Clear()
					m.addCompactionCostNotice("compaction", usage)
					if rows := m.chatContainer.Render(120); len(rows) != 0 {
						t.Fatalf("disabled notice rendered %v", rows)
					}
				}
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:51
		run("renders each compaction cost after its summary", func(t *testing.T) {
			m := compactionUIMode(t, true)
			previous := &ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10, Cost: ai.UsageCost{Input: .001, Output: .002, CacheRead: .003, CacheWrite: .004, Total: .01}}
			currentUsage := compactionUIUsage(.1)
			currentUsage.Cost.CacheWrite = .04
			current := contextFixtureEntry(t, "current", "previous", "compaction", map[string]any{"summary": "current summary", "firstKeptEntryId": "kept", "tokensBefore": 200, "usage": currentUsage})
			m.renderSessionEntryList([]SessionEntry{current, compactionUIEntry(t, "previous", "", "previous summary", previous)}, false)
			output := stripANSI(strings.Join(m.chatContainer.Render(120), "\n"))
			last := -1
			for _, text := range []string{"current summary", "Compaction: 100 tokens billed (~$0.10)", "previous summary", "Compaction: 10 tokens billed (~$0.01)"} {
				index := strings.Index(output, text)
				if index <= last {
					t.Fatalf("missing/out-of-order %q: %s", text, output)
				}
				last = index
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:109
		run("renders retained entries and appends latest summary cost at bottom", func(t *testing.T) {
			m := compactionUIMode(t, true)
			usage := compactionUIUsage(.125)
			latest := contextFixtureEntry(t, "latest", "previous", "compaction", map[string]any{"summary": "summary", "firstKeptEntryId": "kept", "tokensBefore": 123, "usage": usage})
			previous := compactionUIEntry(t, "previous", "", "previous summary", usage)
			m.chatContainer.Add(tui.NewText("old transcript"))
			m.renderCompactionResult(agent.CompactionEndEvent{Reason: "manual", Summary: "summary", TokensBefore: 123, Usage: usage}, []SessionEntry{latest, previous})
			output := stripANSI(strings.Join(m.chatContainer.Render(120), "\n"))
			if strings.Contains(output, "old transcript") {
				t.Fatal("transcript not cleared")
			}
			if strings.Count(output, "Compaction: 100 tokens billed (~$0.13)") != 2 {
				t.Fatal(output)
			}
			if len(m.compactionOrder) != 2 || m.compactionOrder[1] == nil {
				t.Fatal("latest summary not last")
			}
			last := strings.LastIndex(output, "Compaction: 100 tokens billed (~$0.13)")
			if last < strings.Index(output, "previous summary") || strings.Index(output, "Compacted from 123 tokens") <= strings.Index(output, "previous summary") {
				t.Fatal(output)
			}
			caller := compactionUIMode(t, true)
			handle := &compactionUIHandle{recordingCompactHandle: recordingCompactHandle{inner: caller.currentSession()}}
			caller.opts.SessionHandle = handle
			caller.turnActive.Store(true)
			caller.compactionQueue = []compactionQueuedMessage{{text: "queued after", mode: compactionQueueSteer}}
			caller.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Summary: "summary", TokensBefore: 123, Usage: usage})
			if len(caller.compactionQueue) != 0 || !reflect.DeepEqual(handle.inputs, []string{"queued after"}) {
				t.Fatal("compaction end did not flush with willRetry=false")
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:200
		run("updates working state when same run resumes after compaction", func(t *testing.T) {
			m := compactionUIMode(t, false)
			m.opts.Settings.ShowTerminalProgress = new(true)
			old := setTerminalProgress
			var progress []bool
			setTerminalProgress = func(value bool) { progress = append(progress, value) }
			defer func() { setTerminalProgress = old }()
			m.workingVisible = true
			m.handleAgentEvent(agent.TurnStartEvent{})
			if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "working" {
				t.Fatal("working indicator not restored")
			}
			m.workingVisible = false
			m.handleAgentEvent(agent.TurnStartEvent{})
			if m.activeStatusIndicator != nil {
				t.Fatal("hidden working indicator not cleared")
			}
			if !reflect.DeepEqual(progress, []bool{true, true}) {
				t.Fatal(progress)
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:232
		run("routes interactive response aborts through AgentSession", func(t *testing.T) {
			m := compactionUIMode(t, false)
			h := &compactionUIHandle{}
			m.opts.SessionHandle = h
			m.restoreQueuedMessagesToEditor(true)
			if h.aborts != 1 {
				t.Fatalf("Session aborts=%d", h.aborts)
			}
		})
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-compaction.test.ts:249
		run("preserves steering behavior when flushing into an active run", func(t *testing.T) {
			m := compactionUIMode(t, false)
			h := &compactionUIHandle{}
			m.opts.SessionHandle = h
			m.turnActive.Store(true)
			m.compactionQueue = []compactionQueuedMessage{{text: "change direction", mode: compactionQueueSteer}}
			m.flushCompactionQueue(t.Context(), true)
			if len(m.compactionQueue) != 0 || !reflect.DeepEqual(h.inputs, []string{"change direction"}) || !reflect.DeepEqual(h.behaviors, []string{"steer"}) {
				t.Fatalf("inputs=%v modes=%v queue=%v", h.inputs, h.behaviors, m.compactionQueue)
			}
			steering, _ := m.agent.PendingMessages()
			if len(steering) != 1 || extractAgentMessageText(steering[0]) != "change direction" {
				t.Fatal(steering)
			}
		})
	}
}
