package store

// pi: packages/durable/src/storage/store/tables.ts
// The row builders and the indexed-string encoding of tables.ts (`taskRow`, `submissionRow`, `encodeIndexed`) are Pi module internals
// that no public entry point exports: the columns the store indexes (status, abort_requested, background, kind, request_id) are
// asserted here against the text of tables.ts.

import (
	"testing"

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
