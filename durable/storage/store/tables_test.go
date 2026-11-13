package store

// pi: packages/durable/src/storage/store/tables.ts
// The row builders and the indexed-string encoding of tables.ts (`taskRow`, `submissionRow`, `encodeIndexed`) are Pi module internals
// that no public entry point exports: the columns the store indexes (status, abort_requested, background, kind, request_id) are
// asserted here against the text of tables.ts.

import (
	"reflect"
	"strings"
	"testing"

	chordstore "github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/durable"
)

func TestTaskRowColumnsAreTheIndexedFieldsOfTheRecord(t *testing.T) {
	record := func(status durable.TaskStatus, abort, background bool) durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
		return durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{
			Id: 7, ConversationId: 3, Kind: `a"b`, AbortRequested: abort, Background: background,
			State: durable.TaskState[durable.JsonValue, durable.JsonValue]{Status: status},
		}
	}
	cases := []struct {
		name                  string
		record                durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]
		status                string
		abortRequested, bgrnd int64
	}{
		{"neither flag", record(durable.TaskPending, false, false), "pending", 0, 0},
		{"abort requested", record(durable.TaskRunning, true, false), "running", 1, 0},
		{"background", record(durable.TaskSleeping, false, true), "sleeping", 0, 1},
		{"both flags", record(durable.TaskTerminal, true, true), "terminal", 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, err := TaskRow(c.record)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{
				"id": int64(7), "conversation_id": int64(3), "kind": `"a\"b"`,
				"status": c.status, "abort_requested": c.abortRequested, "background": c.bgrnd,
			}
			for column, value := range want {
				if row[column] != value {
					t.Errorf("column %s = %#v, want %#v", column, row[column], value)
				}
			}
		})
	}
}

func TestSubmissionRowColumnsAreTheIndexedFieldsOfTheRecord(t *testing.T) {
	request := "r\n1"
	cases := []struct {
		name      string
		requestID *string
		status    durable.SubmissionStatus
		wantReq   string
	}{
		{"without a request id", nil, durable.SubmissionQueued, ""},
		{"with a request id", &request, durable.SubmissionDone, `"r\n1"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, err := SubmissionRow(durable.SubmissionRecord{Id: 9, ConversationId: 4, RequestId: c.requestID, Status: c.status})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"id": int64(9), "conversation_id": int64(4), "request_id": c.wantReq, "status": string(c.status)}
			for column, value := range want {
				if row[column] != value {
					t.Errorf("column %s = %#v, want %#v", column, row[column], value)
				}
			}
		})
	}
}

func TestEncodeIndexedIsJSONStringifyOfTheString(t *testing.T) {
	cases := map[string]string{
		"":             `""`,
		"plain":        `"plain"`,
		`q"b\`:         `"q\"b\\"`,
		"a\x00b":       `"a\u0000b"`,
		"\xed\xa0\x80": `"\ud800"`,
	}
	for value, want := range cases {
		if got := EncodeIndexed(value); got != want {
			t.Errorf("EncodeIndexed(%q) = %s, want %s", value, got, want)
		}
	}
}

func TestConversationRowColumnsAreTheOwnerOfTheRecordOrZero(t *testing.T) {
	cases := []struct {
		name         string
		owner        *durable.ConversationOwner
		conversation int64
		task         int64
	}{
		// tables.ts: owner?.conversationId ?? 0 and owner?.taskId ?? 0: the sentinel of an absent owner is 0 in both.
		{"without an owner", nil, 0, 0},
		{"owned by a conversation and a task", &durable.ConversationOwner{ConversationId: 4, TaskId: 9}, 4, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, err := ConversationRow(durable.ConversationRecord{Id: 6, Owner: c.owner})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"id": int64(6), "owner_conversation_id": c.conversation, "owner_task_id": c.task}
			for column, value := range want {
				if row[column] != value {
					t.Errorf("column %s = %#v, want %#v", column, row[column], value)
				}
			}
		})
	}
}

func TestEntryRowColumnsAreTheConversationAndAZeroCommitSeq(t *testing.T) {
	row, err := EntryRow(durable.EntryRecord{Id: 8, ConversationId: 3, Kind: "m"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": int64(8), "conversation_id": int64(3), "commit_seq": int64(0)}
	for column, value := range want {
		if row[column] != value {
			t.Errorf("column %s = %#v, want %#v", column, row[column], value)
		}
	}
}

// The columns and indexes of the declared tables are the declarations of tables.ts, in its order.
func TestTablesDeclareTheColumnsAndIndexesOfTablesTS(t *testing.T) {
	type index struct {
		name    string
		columns string
	}
	cases := []struct {
		table   *chordstore.TableDefinition
		columns string
		key     string
		indexes []index
	}{
		{RecordIds, "id record_type", "id", nil},
		{Conversations, "id owner_conversation_id owner_task_id record", "id", []index{{"by_owner_conversation", "owner_conversation_id"}, {"by_owner_task", "owner_task_id"}}},
		{Entries, "id conversation_id commit_seq record", "id", []index{{"by_conversation", "conversation_id"}}},
		{EntryHeads, "id conversation_id", "id", []index{{"by_conversation", "conversation_id"}}},
		{Tasks, "id conversation_id kind status abort_requested background record", "id", []index{
			{"by_status", "status"}, {"by_conversation", "conversation_id"}, {"by_kind", "kind"},
			{"by_abort_requested", "abort_requested"}, {"by_background", "background"},
		}},
		{Submissions, "id conversation_id request_id status record", "id", []index{
			{"by_request", "conversation_id request_id"}, {"by_conversation", "conversation_id"}, {"by_status", "status"},
		}},
	}
	for _, c := range cases {
		var columns []string
		for _, column := range c.table.Columns {
			columns = append(columns, column.Name)
		}
		if got := strings.Join(columns, " "); got != c.columns {
			t.Errorf("%s columns = %q, want %q", c.table.Name, got, c.columns)
		}
		if got := strings.Join(c.table.Key, " "); got != c.key {
			t.Errorf("%s key = %q, want %q", c.table.Name, got, c.key)
		}
		var indexes []index
		for _, declared := range c.table.Indexes {
			indexes = append(indexes, index{declared.Name, strings.Join(declared.Columns, " ")})
		}
		if !reflect.DeepEqual(indexes, c.indexes) {
			t.Errorf("%s indexes = %v, want %v", c.table.Name, indexes, c.indexes)
		}
	}
}
