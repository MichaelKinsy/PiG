// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// .upstream/v0.87.1/packages/coding-agent/test/session-manager/save-entry.test.ts:5
func TestSessionSavesCustomEntriesAndIncludesThemInTreeTraversal(t *testing.T) {
	session := NewSession("test", "/project")
	user := mkUserMsg("hello")
	user.User.Timestamp = 1
	msgID, err := session.AppendMessage(user)
	if err != nil {
		t.Fatal(err)
	}
	customID, err := session.generateEntryID()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendEntry(CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", ID: customID, ParentID: session.LeafID(), Timestamp: RFC3339NowNano()}, CustomType: "my_data", Data: map[string]any{"foo": "bar"}}); err != nil {
		t.Fatal(err)
	}
	assistant := mkAssistantMsg("hi")
	assistant.Assistant.Timestamp = 2
	assistant.Assistant.API = "anthropic-messages"
	assistant.Assistant.Provider = "anthropic"
	assistant.Assistant.ModelID = "test"
	assistant.Assistant.Usage = &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
	assistant.Assistant.StopReason = ai.StopReasonStop
	msg2ID, err := session.AppendMessage(assistant)
	if err != nil {
		t.Fatal(err)
	}
	entries := session.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %v", entries)
	}
	var custom CustomEntry
	if err := json.Unmarshal(entries[1].Raw(), &custom); err != nil {
		t.Fatal(err)
	}
	// An object in `data` is held as the raw JSON it was written as, so its members keep their order; toEqual({foo: "bar"}) compares its members.
	data, _ := orderedjson.Map(custom.Data)
	if custom.Type != "custom" || custom.ID != customID || custom.ParentID == nil || *custom.ParentID != msgID || custom.CustomType != "my_data" || !reflect.DeepEqual(data, map[string]any{"foo": "bar"}) {
		t.Fatalf("custom entry = %+v", custom)
	}
	path := session.GetBranch()
	var ids []string
	for _, e := range path {
		ids = append(ids, e.Base.ID)
	}
	if !reflect.DeepEqual(ids, []string{msgID, customID, msg2ID}) {
		t.Fatalf("branch = %v", ids)
	}
	if got := session.BuildContext(nil); len(got) != 2 {
		t.Fatalf("context = %+v", got)
	}
}
