// SPDX-License-Identifier: MIT

package codingagent

import "testing"

func TestBuildSessionContextExplicitNullLeaf(t *testing.T) {
	entries := []SessionEntry{contextFixtureMessage(t, "1", "", "user", "hello"), contextFixtureMessage(t, "2", "1", "assistant", "hi")}
	context := BuildSessionContext(entries, nil, nil)
	if len(context.Messages) != 0 || context.ThinkingLevel != "off" || context.Model != nil {
		t.Fatal(context)
	}
	if kept := BuildContextEntries(entries, nil, nil); kept == nil || len(kept) != 0 {
		t.Fatal(kept)
	}
	if context := BuildSessionContext(entries, new(""), nil); len(context.Messages) != 2 {
		t.Fatal(context)
	}
}

func BenchmarkBuildSessionContext(b *testing.B) {
	session := NewSession("bench", "/project")
	for i := range 1000 {
		message := mkUserMsg("representative request")
		if i%2 != 0 {
			message = mkAssistantMsg("representative response")
		}
		if _, err := session.AppendMessage(message); err != nil {
			b.Fatal(err)
		}
	}
	entries := session.GetEntries()
	b.ReportAllocs()
	for b.Loop() {
		if got := BuildSessionContext(entries, LastLeaf(), nil); len(got.Messages) != len(entries) {
			b.Fatal("lost messages")
		}
	}
}
