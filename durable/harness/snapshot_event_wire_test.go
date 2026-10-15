package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// events.ts:141-156 snapshotOf builds the SnapshotEvent a late attacher receives; events.ts:36-59 fixes its members and their JSON order:
// type, entries, run? (only inputs), generation?, tools, nestedTools (Pi 1.2.0 events.ts:48-49,149; not in 1.1.0), compactions,
// compactionFailure? (events.ts:151), inbox (queued: id and mode only), agent ({} when absent) and usage (the usage document's initial
// { models: {}, tools: {} } when absent). A new subscriber reads the event as JSON, so the wire form is the contract.
func TestSnapshotEventWireShapeFollowsPisSnapshotOf(t *testing.T) {
	entries := []durable.EntryRecord{}
	for _, tc := range []struct {
		name string
		docs ViewDocs
		want string
	}{
		{
			// No documents mounted: tools, nestedTools, compactions and inbox are empty arrays (live.tools ?? []), never null; agent is {} and usage is initial.
			name: "no documents",
			docs: ViewDocs{},
			want: `{"type":"snapshot","entries":[],"tools":[],"nestedTools":[],"compactions":[],"inbox":[],"agent":{},"usage":{"models":{},"tools":{}}}`,
		},
		{
			// run keeps only inputs, generation is passed through, an inbox item is reduced to {id, mode} (events.ts:158-160 queued).
			name: "live run, generation, tool slot and queued input",
			docs: ViewDocsOf(ViewDocsOf(ViewDocs{}, "pi.live", delta.JsonObjectOf(
				"run", delta.JsonObjectOf("inputs", []any{float64(1), float64(2)}, "other", "dropped"),
				"generation", delta.JsonObjectOf("attempt", float64(2), "retry", delta.JsonObjectOf("at", float64(5), "error", "boom"), "deferred", delta.JsonObjectOf("pollAt", float64(9))),
				"tools", []any{delta.JsonObjectOf("callId", "c", "name", "read", "status", "running")},
			)), "pi.inbox", delta.JsonObjectOf(
				"items", []any{delta.JsonObjectOf("id", float64(3), "mode", "steer", "content", "hello")},
			)),
			want: `{"type":"snapshot","entries":[],"run":{"inputs":[1,2]},"generation":{"attempt":2,"retry":{"at":5,"error":"boom"},"deferred":{"pollAt":9}},` +
				`"tools":[{"callId":"c","name":"read","status":"running"}],"nestedTools":[],"compactions":[],"inbox":[{"id":3,"mode":"steer"}],"agent":{},"usage":{"models":{},"tools":{}}}`,
		},
		{
			// nestedTools and compactionFailure are passed through in Pi's member order (events.ts:149-151); the nested slot has the
			// members tool.ts:718-726 writes, in that order.
			name: "nested tool slot and compaction failure",
			docs: ViewDocsOf(ViewDocs{}, "pi.live", delta.JsonObjectOf(
				"nestedTools", []any{delta.JsonObjectOf("callId", "n", "parentCallId", "c", "parentTaskId", float64(2), "name", "read", "taskId", float64(4),
					"arguments", delta.JsonObjectOf("path", "a"), "status", "running")},
				"compactionFailure", delta.JsonObjectOf("taskId", float64(5), "reason", "threshold", "error", "boom"),
			)),
			want: `{"type":"snapshot","entries":[],"tools":[],"nestedTools":[{"callId":"n","parentCallId":"c","parentTaskId":2,"name":"read","taskId":4,"arguments":{"path":"a"},"status":"running"}],"compactions":[],` +
				`"compactionFailure":{"taskId":5,"reason":"threshold","error":"boom"},"inbox":[],"agent":{},"usage":{"models":{},"tools":{}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(snapshotOf(ConversationView{Entries: entries, Docs: tc.docs}))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("snapshot JSON\n got  %s\n want %s", got, tc.want)
			}
		})
	}
}
