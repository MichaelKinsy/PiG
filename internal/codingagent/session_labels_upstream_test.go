// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func upstreamSessionUser(t *testing.T, s *Session, text string) string {
	t.Helper()
	m := mkUserMsg(text)
	m.User.Timestamp = 1
	id, err := s.AppendMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func upstreamSessionAssistant(t *testing.T, s *Session, text string) string {
	t.Helper()
	m := mkAssistantMsg(text)
	m.Assistant.Timestamp = 2
	m.Assistant.API = "anthropic-messages"
	m.Assistant.Provider = "anthropic"
	m.Assistant.ModelID = "test"
	m.Assistant.Usage = &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
	m.Assistant.StopReason = ai.StopReasonStop
	id, err := s.AppendMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func upstreamLabel(t *testing.T, s *Session, id, label string) string {
	t.Helper()
	if err := s.AppendLabelChange(id, &label); err != nil {
		t.Fatal(err)
	}
	return *s.LeafID()
}
func upstreamClone(t *testing.T, s *Session, id string) *Session {
	t.Helper()
	clone, err := NewSessionManagerWithDir(s.CWD(), t.TempDir()).Clone(s, id)
	if err != nil {
		t.Fatal(err)
	}
	return clone
}
func sessionNode(t *testing.T, s *Session, id string) *SessionTreeNode {
	t.Helper()
	var walk func(*SessionTreeNode) *SessionTreeNode
	walk = func(n *SessionTreeNode) *SessionTreeNode {
		if n.Entry.Base.ID == id {
			return n
		}
		for _, child := range n.Children {
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(s.Tree())
}
func requireSessionEntry(t *testing.T, s *Session, id string) SessionEntry {
	t.Helper()
	e, ok := s.EntryByID(id)
	if !ok {
		t.Fatalf("missing entry %s", id)
	}
	return e
}

func TestSessionLabelsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:5
	t.Run("sets and gets labels", func(t *testing.T) {
		s := NewSession("test", "/project")
		id := upstreamSessionUser(t, s, "hello")
		if sessionNode(t, s, id).Label != "" {
			t.Fatal("initial label")
		}
		labelID := upstreamLabel(t, s, id, "checkpoint")
		if sessionNode(t, s, id).Label != "checkpoint" {
			t.Fatal("label missing")
		}
		var label LabelEntry
		if err := json.Unmarshal(requireSessionEntry(t, s, labelID).Raw(), &label); err != nil {
			t.Fatal(err)
		}
		if label.ID != labelID || label.TargetID != id || label.Label == nil || *label.Label != "checkpoint" {
			t.Fatalf("label=%+v", label)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:26
	t.Run("clears labels with undefined", func(t *testing.T) {
		for _, clear := range []*string{nil, new("")} {
			s := NewSession("test", "/project")
			id := upstreamSessionUser(t, s, "hello")
			upstreamLabel(t, s, id, "checkpoint")
			if sessionNode(t, s, id).Label != "checkpoint" {
				t.Fatal("label missing")
			}
			if err := s.AppendLabelChange(id, clear); err != nil {
				t.Fatal(err)
			}
			n := sessionNode(t, s, id)
			if n.Label != "" || n.LabelTimestamp != "" {
				t.Fatalf("label not cleared: %+v", n)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:39
	t.Run("last label wins", func(t *testing.T) {
		s := NewSession("test", "/project")
		id := upstreamSessionUser(t, s, "hello")
		upstreamLabel(t, s, id, "first")
		upstreamLabel(t, s, id, "second")
		last := upstreamLabel(t, s, id, "third")
		n := sessionNode(t, s, id)
		if n.Label != "third" || n.LabelTimestamp != requireSessionEntry(t, s, last).Base.Timestamp {
			t.Fatalf("label=%+v", n)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:57
	t.Run("labels are included in tree nodes", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		b := upstreamSessionAssistant(t, s, "hi")
		la := upstreamLabel(t, s, a, "start")
		lb := upstreamLabel(t, s, b, "response")
		na, nb := sessionNode(t, s, a), sessionNode(t, s, b)
		if na.Label != "start" || nb.Label != "response" || na.LabelTimestamp != requireSessionEntry(t, s, la).Base.Timestamp || nb.LabelTimestamp != requireSessionEntry(t, s, lb).Base.Timestamp || len(na.Children) != 1 || na.Children[0].Entry.Base.ID != b {
			t.Fatal("tree labels")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:98
	t.Run("labels are preserved in createBranchedSession", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		b := upstreamSessionAssistant(t, s, "hi")
		la := upstreamLabel(t, s, a, "important")
		lb := upstreamLabel(t, s, b, "also-important")
		ta, tb := requireSessionEntry(t, s, la).Base.Timestamp, requireSessionEntry(t, s, lb).Base.Timestamp
		s = upstreamClone(t, s, b)
		na, nb := sessionNode(t, s, a), sessionNode(t, s, b)
		if na.Label != "important" || nb.Label != "also-important" || na.LabelTimestamp != ta || nb.LabelTimestamp != tb {
			t.Fatal("fork lost labels or timestamps")
		}
		n := 0
		for _, e := range s.Entries() {
			if e.Base.Type == "label" {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("labels=%d", n)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:145
	t.Run("rewires children of removed labels when forking", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		upstreamLabel(t, s, a, "checkpoint")
		if err := s.AppendModelSwitch("anthropic", "claude-test", ""); err != nil {
			t.Fatal(err)
		}
		model := *s.LeafID()
		b := upstreamSessionUser(t, s, "followup")
		s = upstreamClone(t, s, b)
		parent := requireSessionEntry(t, s, model).Base.ParentID
		if parent == nil || *parent != a {
			t.Fatalf("model parent=%v", parent)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:158
	t.Run("labels not on path are not preserved in createBranchedSession", func(t *testing.T) {
		s := NewSession("test", "/project")
		a := upstreamSessionUser(t, s, "hello")
		b := upstreamSessionAssistant(t, s, "hi")
		c := upstreamSessionUser(t, s, "followup")
		upstreamLabel(t, s, a, "first")
		upstreamLabel(t, s, b, "second")
		upstreamLabel(t, s, c, "third")
		s = upstreamClone(t, s, b)
		if sessionNode(t, s, a).Label != "first" || sessionNode(t, s, b).Label != "second" || sessionNode(t, s, c) != nil {
			t.Fatal("fork path labels")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:195
	t.Run("labels are not included in buildSessionContext", func(t *testing.T) {
		s := NewSession("test", "/project")
		id := upstreamSessionUser(t, s, "hello")
		upstreamLabel(t, s, id, "checkpoint")
		messages := s.BuildContext(nil)
		if len(messages) != 1 || messages[0].User == nil {
			t.Fatalf("context=%+v", messages)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/session-manager/labels.test.ts:206
	t.Run("throws when labeling non-existent entry", func(t *testing.T) {
		s := NewSession("test", "/project")
		label := "label"
		err := s.AppendLabelChange("non-existent", &label)
		if err == nil || err.Error() != "Entry non-existent not found" {
			t.Fatalf("error=%v", err)
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8989-fork-compaction-label-boundary.test.ts:6
func TestForkPreservesCompactionContextWhenBoundaryLabelIsRemoved(t *testing.T) {
	s := NewSession("test", "/project")
	old := upstreamSessionUser(t, s, "old")
	label := upstreamLabel(t, s, old, "checkpoint")
	kept := upstreamSessionUser(t, s, "kept")
	compID, err := s.AppendCompaction("summary", label, 100, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf := upstreamSessionUser(t, s, "after")
	s = upstreamClone(t, s, leaf)
	var comp CompactionEntry
	if err := json.Unmarshal(requireSessionEntry(t, s, compID).Raw(), &comp); err != nil {
		t.Fatal(err)
	}
	if comp.FirstKeptEntryID != kept {
		t.Fatalf("boundary=%s, want %s", comp.FirstKeptEntryID, kept)
	}
	msgs := s.BuildContext(nil)
	if len(msgs) != 3 || msgs[0].Custom["summary"] != "summary" || msgs[0].Role() != "compactionSummary" {
		t.Fatalf("context=%+v", msgs)
	}
	texts := []string{extractUserText(msgs[1]), extractUserText(msgs[2])}
	if !reflect.DeepEqual(texts, []string{"kept", "after"}) {
		t.Fatal(texts)
	}
	fmt.Println("SESSION_FORK boundary=kept context=summary,kept,after")
}

func BenchmarkCloneLabeledSession(b *testing.B) {
	source := NewSession("bench", "/project")
	for i := range 1000 {
		id, err := source.AppendMessage(mkUserMsg(fmt.Sprintf("message %d", i)))
		if err != nil {
			b.Fatal(err)
		}
		if i%10 == 0 {
			label := fmt.Sprintf("checkpoint %d", i)
			if err := source.AppendLabelChange(id, &label); err != nil {
				b.Fatal(err)
			}
		}
	}
	manager := NewSessionManagerWithDir("/project", b.TempDir())
	leaf := *source.LeafID()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := manager.Clone(source, leaf); err != nil {
			b.Fatal(err)
		}
	}
}
