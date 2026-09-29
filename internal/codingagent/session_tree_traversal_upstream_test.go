// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func upstreamEntryIDs(entries []SessionEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Base.ID)
	}
	return out
}
func requireEntryParent(t *testing.T, s *Session, id string, parent *string) {
	t.Helper()
	e := requireSessionEntry(t, s, id)
	if !reflect.DeepEqual(e.Base.ParentID, parent) {
		t.Fatalf("entry %s parent=%v want=%v", id, e.Base.ParentID, parent)
	}
}
func upstreamThinking(t *testing.T, s *Session) string {
	t.Helper()
	if err := s.AppendThinkingLevelChange("high"); err != nil {
		t.Fatal(err)
	}
	return *s.LeafID()
}
func upstreamCustom(t *testing.T, s *Session, typ string, data any) string {
	t.Helper()
	id, err := s.generateEntryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEntry(CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", ID: id, ParentID: s.LeafID(), Timestamp: RFC3339NowNano()}, CustomType: typ, Data: data}); err != nil {
		t.Fatal(err)
	}
	return id
}
func upstreamSummaryUsage() *ai.Usage {
	return &ai.Usage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100, Cost: ai.UsageCost{Input: .1, Output: .2, CacheRead: .3, CacheWrite: .4, Total: 1}}
}

func TestSessionTreeTraversalUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:10
	t.Run("appendMessage creates entry with correct parentId chain", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "first")
		b := upstreamSessionAssistant(t, s, "second")
		c := upstreamSessionUser(t, s, "third")
		if !slices.Equal(upstreamEntryIDs(s.Entries()), []string{a, b, c}) {
			t.Fatal("entry order")
		}
		requireEntryParent(t, s, a, nil)
		requireEntryParent(t, s, b, &a)
		requireEntryParent(t, s, c, &b)
		if s.Entries()[0].Base.Type != "message" {
			t.Fatal("type")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:31
	t.Run("appendThinkingLevelChange integrates into tree", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		b := upstreamThinking(t, s)
		c := upstreamSessionAssistant(t, s, "response")
		if len(s.Entries()) != 3 || requireSessionEntry(t, s, b).Base.Type != "thinking_level_change" {
			t.Fatal("thinking entry")
		}
		requireEntryParent(t, s, b, &a)
		requireEntryParent(t, s, c, &b)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:49
	t.Run("appendModelChange integrates into tree", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		if err := s.AppendModelSwitch("openai", "gpt-4", ""); err != nil {
			t.Fatal(err)
		}
		b := *s.LeafID()
		c := upstreamSessionAssistant(t, s, "response")
		var e ModelChangeEntry
		if err := json.Unmarshal(requireSessionEntry(t, s, b).Raw(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Type != "model_change" || e.Provider != "openai" || e.ModelID != "gpt-4" {
			t.Fatal(e)
		}
		requireEntryParent(t, s, b, &a)
		requireEntryParent(t, s, c, &b)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:69
	t.Run("appendCompaction integrates into tree", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		id, err := s.AppendCompaction("summary", a, 1000, nil, false, upstreamSummaryUsage())
		if err != nil {
			t.Fatal(err)
		}
		c := upstreamSessionUser(t, s, "3")
		var e CompactionEntry
		if err := json.Unmarshal(requireSessionEntry(t, s, id).Raw(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Summary != "summary" || e.FirstKeptEntryID != a || e.TokensBefore != 1000 || !reflect.DeepEqual(e.Usage, upstreamSummaryUsage()) {
			t.Fatal(e)
		}
		requireEntryParent(t, s, id, &b)
		requireEntryParent(t, s, c, &id)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:100
	t.Run("appendCustomEntry integrates into tree", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		b := upstreamCustom(t, s, "my_data", map[string]any{"key": "value"})
		c := upstreamSessionAssistant(t, s, "response")
		var e CustomEntry
		if err := json.Unmarshal(requireSessionEntry(t, s, b).Raw(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Type != "custom" || e.CustomType != "my_data" || !reflect.DeepEqual(e.Data, map[string]any{"key": "value"}) {
			t.Fatal(e)
		}
		requireEntryParent(t, s, b, &a)
		requireEntryParent(t, s, c, &b)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:118
	t.Run("leaf pointer advances after each append", func(t *testing.T) {
		s := NewSession("test", "/project")
		if s.LeafID() != nil {
			t.Fatal("initial leaf")
		}
		for _, appendEntry := range []func() string{func() string { return upstreamSessionUser(t, s, "1") }, func() string { return upstreamSessionAssistant(t, s, "2") }, func() string { return upstreamThinking(t, s) }} {
			id := appendEntry()
			if s.LeafID() == nil || *s.LeafID() != id {
				t.Fatal("leaf did not advance")
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:135
	t.Run("getPath returns empty array for empty session", func(t *testing.T) {
		if got := NewSession("test", "/project").GetBranch(); len(got) != 0 {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:140
	t.Run("getPath returns single entry path", func(t *testing.T) {
		s := NewSession("test", "/project")
		id := upstreamSessionUser(t, s, "hello")
		if !slices.Equal(upstreamEntryIDs(s.GetBranch()), []string{id}) {
			t.Fatal("path")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:149
	t.Run("getPath returns full path from root to leaf", func(t *testing.T) {
		s := NewSession("test", "/project")
		ids := []string{upstreamSessionUser(t, s, "1"), upstreamSessionAssistant(t, s, "2"), upstreamThinking(t, s), upstreamSessionUser(t, s, "3")}
		if !slices.Equal(upstreamEntryIDs(s.GetBranch()), ids) {
			t.Fatal("path")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:162
	t.Run("getPath returns path from specified entry to root", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		upstreamSessionUser(t, s, "3")
		upstreamSessionAssistant(t, s, "4")
		if !slices.Equal(upstreamEntryIDs(s.Branch(b)), []string{a, b}) {
			t.Fatal("path")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:177
	t.Run("getTree returns empty array for empty session", func(t *testing.T) {
		if len(NewSession("test", "/project").Tree().Children) != 0 {
			t.Fatal("tree")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:182
	t.Run("getTree returns single root for linear session", func(t *testing.T) {
		s := NewSession("test", "/project")
		ids := []string{upstreamSessionUser(t, s, "1"), upstreamSessionAssistant(t, s, "2"), upstreamSessionUser(t, s, "3")}
		nodes := s.Tree().Children
		for _, id := range ids {
			if len(nodes) != 1 || nodes[0].Entry.Base.ID != id {
				t.Fatal("linear tree")
			}
			nodes = nodes[0].Children
		}
		if len(nodes) != 0 {
			t.Fatal("extra tree children")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:201
	t.Run("getTree returns tree with branches after branch", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		c := upstreamSessionUser(t, s, "3")
		if err := s.Fork(b); err != nil {
			t.Fatal(err)
		}
		d := upstreamSessionUser(t, s, "4-branch")
		roots := s.Tree().Children
		if len(roots) != 1 || roots[0].Entry.Base.ID != a || len(roots[0].Children) != 1 || roots[0].Children[0].Entry.Base.ID != b {
			t.Fatal("roots")
		}
		children := roots[0].Children[0].Children
		if len(children) != 2 {
			t.Fatal("branches")
		}
		got := []string{children[0].Entry.Base.ID, children[1].Entry.Base.ID}
		slices.Sort(got)
		want := []string{c, d}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatal(got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:228
	t.Run("handles multiple branches at same point", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "root")
		b := upstreamSessionAssistant(t, s, "response")
		var ids []string
		for _, text := range []string{"branch-A", "branch-B", "branch-C"} {
			if err := s.Fork(b); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, upstreamSessionUser(t, s, text))
		}
		node := s.Tree().Children[0].Children[0]
		var got []string
		for _, child := range node.Children {
			got = append(got, child.Entry.Base.ID)
		}
		slices.Sort(got)
		slices.Sort(ids)
		if node.Entry.Base.ID != b || !slices.Equal(got, ids) {
			t.Fatal("branches")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:255
	t.Run("handles deep branching", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		c := upstreamSessionUser(t, s, "3")
		upstreamSessionAssistant(t, s, "4")
		if err := s.Fork(b); err != nil {
			t.Fatal(err)
		}
		e := upstreamSessionUser(t, s, "5")
		upstreamSessionAssistant(t, s, "6")
		if err := s.Fork(e); err != nil {
			t.Fatal(err)
		}
		upstreamSessionUser(t, s, "7")
		if len(sessionNode(t, s, b).Children) != 2 || len(sessionNode(t, s, e).Children) != 2 || len(sessionNode(t, s, c).Children) != 1 {
			t.Fatal("deep tree")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:288
	t.Run("branch moves leaf pointer to specified entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		upstreamSessionAssistant(t, s, "2")
		c := upstreamSessionUser(t, s, "3")
		if *s.LeafID() != c {
			t.Fatal("leaf")
		}
		if err := s.Fork(a); err != nil {
			t.Fatal(err)
		}
		if *s.LeafID() != a {
			t.Fatal("leaf")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:301
	t.Run("branch throws for non-existent entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "hello")
		if err := s.Fork("nonexistent"); err == nil || err.Error() != "Entry nonexistent not found" {
			t.Fatalf("error=%v", err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:308
	t.Run("new appends become children of branch point", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		upstreamSessionAssistant(t, s, "2")
		if err := s.Fork(a); err != nil {
			t.Fatal(err)
		}
		c := upstreamSessionUser(t, s, "branched")
		requireEntryParent(t, s, c, &a)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:324
	t.Run("branchWithSummary inserts source destination and advances leaf", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		upstreamSessionAssistant(t, s, "2")
		c := upstreamSessionUser(t, s, "3")
		id, err := s.AppendBranchSummary(&a, "Summary of abandoned work", nil, false, upstreamSummaryUsage())
		if err != nil {
			t.Fatal(err)
		}
		var e BranchSummaryEntry
		if err := json.Unmarshal(requireSessionEntry(t, s, id).Raw(), &e); err != nil {
			t.Fatal(err)
		}
		if *s.LeafID() != id || e.Type != "branch_summary" || e.FromID != c || e.Summary != "Summary of abandoned work" || !reflect.DeepEqual(e.Usage, upstreamSummaryUsage()) {
			t.Fatal(e)
		}
		requireEntryParent(t, s, id, &a)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:354
	t.Run("branchWithSummary throws for non-existent entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "hello")
		_, err := s.AppendBranchSummary(new("nonexistent"), "summary", nil, false, nil)
		if err == nil || err.Error() != "Entry nonexistent not found" {
			t.Fatal(err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:363
	t.Run("getLeafEntry returns undefined for empty session", func(t *testing.T) {
		s := NewSession("test", "/project")
		if s.LeafID() != nil {
			t.Fatal("leaf")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:368
	t.Run("getLeafEntry returns current leaf entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "1")
		id := upstreamSessionAssistant(t, s, "2")
		if s.LeafID() == nil || requireSessionEntry(t, s, *s.LeafID()).Base.ID != id {
			t.Fatal("leaf entry")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:381
	t.Run("getEntry returns undefined for non-existent id", func(t *testing.T) {
		if _, ok := NewSession("test", "/project").EntryByID("nonexistent"); ok {
			t.Fatal("unexpected entry")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:386
	t.Run("getEntry returns entry by id", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "first")
		b := upstreamSessionAssistant(t, s, "second")
		ea, ok := requireSessionEntry(t, s, a).AsMessage()
		if !ok || extractUserText(ea.Message) != "first" {
			t.Fatal("user")
		}
		eb, ok := requireSessionEntry(t, s, b).AsMessage()
		if !ok || eb.Message.Assistant == nil || eb.Message.Assistant.Content[0].(ai.TextContent).Text != "second" {
			t.Fatal("assistant")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:408
	t.Run("buildSessionContext returns messages from current branch only", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "msg1")
		b := upstreamSessionAssistant(t, s, "msg2")
		upstreamSessionUser(t, s, "msg3")
		if err := s.Fork(b); err != nil {
			t.Fatal(err)
		}
		upstreamSessionAssistant(t, s, "msg4-branch")
		m := s.BuildContext(nil)
		if len(m) != 3 || extractUserText(m[0]) != "msg1" || m[1].Assistant.Content[0].(ai.TextContent).Text != "msg2" || m[2].Assistant.Content[0].(ai.TextContent).Text != "msg4-branch" {
			t.Fatal("branch context")
		}
	})
}

func TestCreateBranchedSessionUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:431
	t.Run("throws for non-existent entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		upstreamSessionUser(t, s, "hello")
		_, err := NewSessionManagerWithDir("/project", t.TempDir()).Clone(s, "nonexistent")
		if err == nil || err.Error() != "Entry nonexistent not found" {
			t.Fatal(err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:438
	t.Run("creates new session with path to specified leaf in memory", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		c := upstreamSessionUser(t, s, "3")
		upstreamSessionAssistant(t, s, "4")
		if err := s.Fork(c); err != nil {
			t.Fatal(err)
		}
		upstreamSessionUser(t, s, "5")
		s = upstreamClone(t, s, b)
		if s.Path() != "" || !slices.Equal(upstreamEntryIDs(s.Entries()), []string{a, b}) {
			t.Fatal("memory fork")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:462
	t.Run("extracts correct path from branched tree", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "1")
		b := upstreamSessionAssistant(t, s, "2")
		upstreamSessionUser(t, s, "3")
		if err := s.Fork(b); err != nil {
			t.Fatal(err)
		}
		d := upstreamSessionUser(t, s, "4")
		e := upstreamSessionAssistant(t, s, "5")
		s = upstreamClone(t, s, e)
		if !slices.Equal(upstreamEntryIDs(s.Entries()), []string{a, b, d, e}) {
			t.Fatal("fork path")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:483
	t.Run("does not duplicate entries when forking from first user message", func(t *testing.T) {
		sm := tempSessionMgr(t)
		s, err := sm.Create("source", "")
		if err != nil {
			t.Fatal(err)
		}
		a := upstreamSessionUser(t, s, "first question")
		upstreamSessionAssistant(t, s, "first answer")
		upstreamSessionUser(t, s, "second question")
		upstreamSessionAssistant(t, s, "second answer")
		s, err = sm.Clone(s, a)
		if err != nil {
			t.Fatal(err)
		}
		if s.Path() == "" {
			t.Fatal("missing fork path")
		}
		if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
			t.Fatalf("fork should be deferred: %v", err)
		}
		upstreamCustom(t, s, "preset-state", map[string]any{"name": "plan"})
		upstreamSessionAssistant(t, s, "new answer")
		rows := readJSONLLines(t, s.Path())
		headers := 0
		ids := map[string]bool{}
		for _, row := range rows {
			if row["type"] == "session" {
				headers++
				continue
			}
			id := row["id"].(string)
			if ids[id] {
				t.Fatal("duplicate entry ID")
			}
			ids[id] = true
		}
		if headers != 1 || len(ids) != 3 {
			t.Fatalf("records=%v", rows)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:527
	t.Run("preserves tool and summary usage across a file-backed reload", func(t *testing.T) {
		sm := tempSessionMgr(t)
		s, err := sm.Create("source", "")
		if err != nil {
			t.Fatal(err)
		}
		root := upstreamSessionUser(t, s, "question")
		upstreamSessionAssistant(t, s, "answer")
		raw := map[string]any{"type": "message", "id": "tool", "parentId": s.LeafID(), "timestamp": RFC3339NowNano(), "message": map[string]any{"role": "toolResult", "toolCallId": "call-1", "toolName": "nested-model", "content": []any{map[string]any{"type": "text", "text": "result"}}, "isError": false, "usage": upstreamSummaryUsage(), "timestamp": 1}}
		if err := s.AppendEntry(raw); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendCompaction("summary", root, 100, nil, false, upstreamSummaryUsage()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendBranchSummary(&root, "branch summary", nil, false, upstreamSummaryUsage()); err != nil {
			t.Fatal(err)
		}
		s, err = sm.Load(s.Path())
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, e := range s.Entries() {
			var wire struct {
				Type    string    `json:"type"`
				Usage   *ai.Usage `json:"usage"`
				Message struct {
					Role  string    `json:"role"`
					Usage *ai.Usage `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(e.Raw(), &wire); err != nil {
				t.Fatal(err)
			}
			switch wire.Type {
			case "compaction", "branch_summary":
				if !reflect.DeepEqual(wire.Usage, upstreamSummaryUsage()) {
					t.Fatal("summary usage")
				}
				seen[wire.Type] = true
			case "message":
				if wire.Message.Role == "toolResult" {
					if !reflect.DeepEqual(wire.Message.Usage, upstreamSummaryUsage()) {
						t.Fatal("tool usage")
					}
					seen["toolResult"] = true
				}
			}
		}
		if len(seen) != 3 {
			t.Fatal("missing usage entries")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/tree-traversal.test.ts:573
	t.Run("writes file immediately when forking with assistant messages", func(t *testing.T) {
		sm := tempSessionMgr(t)
		s, err := sm.Create("source", "")
		if err != nil {
			t.Fatal(err)
		}
		upstreamSessionUser(t, s, "first question")
		a := upstreamSessionAssistant(t, s, "first answer")
		upstreamSessionUser(t, s, "second question")
		upstreamSessionAssistant(t, s, "second answer")
		s, err = sm.Clone(s, a)
		if err != nil {
			t.Fatal(err)
		}
		headers := 0
		for _, row := range readJSONLLines(t, s.Path()) {
			if row["type"] == "session" {
				headers++
			}
		}
		if headers != 1 {
			t.Fatal("fork headers")
		}
	})
}
