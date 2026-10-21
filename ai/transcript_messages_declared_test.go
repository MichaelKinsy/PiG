package ai

import "testing"

// Pi packages/ai/src/utils/transcript.ts:40 TranscriptMessages = readonly { role: string }[]: every replay helper reads only role "system" entries, so a transcript of application-defined roles reaches getCurrentSystemMessage, getDeclaredTools (170-176), hasToolRedefinitions (186-196), hasNonAdditiveToolChanges (200-210) and resolveTranscriptTools (229-237) unchanged. The helpers are driven through the TranscriptMessages type itself.

func customTranscript(system ...SystemMessage) TranscriptMessages[appRoleMessage] {
	var out TranscriptMessages[appRoleMessage]
	for i := range system {
		out = append(out, appRoleMessage{role: "system", system: &system[i]}, appRoleMessage{role: "branchSummary"})
	}
	return out
}

// transcript.ts:170-176 first-declaration order, a later same-name definition replaces the earlier one in place.
func TestGetDeclaredToolsReadsSystemEntriesOfACustomRoleTranscript(t *testing.T) {
	transcript := customTranscript(
		SystemMessage{Content: SystemText("a"), ToolsAdded: []ToolSchema{{Name: "read", Description: "v1"}, {Name: "bash"}}},
		SystemMessage{Content: SystemText("b"), ToolsAdded: []ToolSchema{{Name: "write"}, {Name: "read", Description: "v2"}}},
	)
	got := GetDeclaredTools(transcript)
	if names := toolNames(got); len(names) != 3 || names[0] != "read" || names[1] != "bash" || names[2] != "write" {
		t.Fatalf("declared tools = %v, want [read bash write] in first-declaration order", names)
	}
	if got[0].Description != "v2" {
		t.Fatalf("read description = %q, want the later definition", got[0].Description)
	}
	if got := GetDeclaredTools(TranscriptMessages[appRoleMessage]{{role: "bashExecution"}}); len(got) != 0 {
		t.Fatalf("no system entries declare no tools: %v", got)
	}
}

// transcript.ts:186-196 true only when one name carries two different definitions; an identical redeclaration is not a redefinition.
func TestHasToolRedefinitionsOverACustomRoleTranscript(t *testing.T) {
	same := ToolSchema{Name: "read", Description: "same"}
	if HasToolRedefinitions(customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{same}},
		SystemMessage{ToolsAdded: []ToolSchema{same}},
	)) {
		t.Fatal("an identical redeclaration is not a redefinition")
	}
	if !HasToolRedefinitions(customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{same}},
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "read", Description: "different"}}},
	)) {
		t.Fatal("a different definition under the same name is a redefinition")
	}
}

// transcript.ts:200-210 any removal, or any same-name redeclaration (even identical), is non-additive.
func TestHasNonAdditiveToolChangesOverACustomRoleTranscript(t *testing.T) {
	read := ToolSchema{Name: "read"}
	if HasNonAdditiveToolChanges(customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{read}},
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "write"}}},
	)) {
		t.Fatal("distinct additions are additive")
	}
	if !HasNonAdditiveToolChanges(customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{read}},
		SystemMessage{ToolsRemoved: []ToolReference{{Name: "read"}}},
	)) {
		t.Fatal("a removal is non-additive")
	}
	if !HasNonAdditiveToolChanges(customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{read}},
		SystemMessage{ToolsAdded: []ToolSchema{read}},
	)) {
		t.Fatal("an identical redeclaration is non-additive")
	}
}

// transcript.ts resolveTranscriptTools: additions anchor after the last removal or redeclaration; then the request carries the tools up to that boundary, otherwise the current tool list.
func TestResolveTranscriptToolsOverACustomRoleTranscript(t *testing.T) {
	additive := customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "read"}}},
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "write"}}},
	)
	anchored := ResolveTranscriptTools(additive, TranscriptToolSupportAdditions)
	if anchored.FoldedSystemMessages != 1 || len(anchored.RequestTools) != 1 || anchored.RequestTools[0].Name != "read" {
		t.Fatalf("anchored = %+v, want only the initial tools", anchored)
	}
	flat := ResolveTranscriptTools(additive, TranscriptToolSupportNone)
	if flat.FoldedSystemMessages != 2 || len(flat.RequestTools) != 2 {
		t.Fatalf("unsupported transport = %+v, want the complete current tool list", flat)
	}
	removing := customTranscript(
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "read"}, {Name: "bash"}}},
		SystemMessage{ToolsRemoved: []ToolReference{{Name: "bash"}}},
	)
	got := ResolveTranscriptTools(removing, TranscriptToolSupportAdditions)
	if got.FoldedSystemMessages != 2 || len(got.RequestTools) != 1 || got.RequestTools[0].Name != "read" {
		t.Fatalf("non-additive history = %+v, want the current list [read]", got)
	}
	if changes := ResolveTranscriptTools(removing, TranscriptToolSupportChanges); changes.FoldedSystemMessages != 0 || len(changes.RequestTools) != 0 {
		t.Fatalf("changes = %+v, want every tool declared in place", changes)
	}
}

// transcript.ts:73-97 getCurrentSystemMessage replays every system entry among custom roles into one: contents joined by a blank line, the first timestamp, and the current tools.
func TestGetCurrentSystemMessageOverACustomRoleTranscript(t *testing.T) {
	got := GetCurrentSystemMessage(customTranscript(
		SystemMessage{Content: SystemText("first"), Timestamp: 7, ToolsAdded: []ToolSchema{{Name: "read"}}},
		SystemMessage{Content: SystemText("second"), Timestamp: 9},
	))
	if got == nil || GetCurrentSystemPrompt([]Message{*got}) != "first\n\nsecond" || got.Timestamp != 7 {
		t.Fatalf("current system message = %+v, want both entries replayed with the first timestamp", got)
	}
	if names := toolNames(got.ToolsAdded); len(names) != 1 || names[0] != "read" {
		t.Fatalf("current tools = %v, want [read]", names)
	}
	if GetCurrentSystemMessage(TranscriptMessages[appRoleMessage]{{role: "bashExecution"}}) != nil {
		t.Fatal("no system entry means no current system message")
	}
}
