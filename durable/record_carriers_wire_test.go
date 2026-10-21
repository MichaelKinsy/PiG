package durable

// pi: packages/durable/src/types.ts (TaskRecordBase.trace/links, SubmissionRecordBase.trace; Pi 2ec55d80)

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// The carriers are optional plain JSON members of the stored records: absent without propagation, kept as given with it,
// and an empty links array stays an empty array, as JSON.stringify keeps one.
func TestRecordCarriersAreOptionalPlainJsonMembers(t *testing.T) {
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	if text := encode(SubmissionRecord{Id: 1, ConversationId: 2, Type: SubmissionTypeInput, Status: SubmissionQueued}); strings.Contains(text, "trace") {
		t.Fatalf("a submission without a carrier encodes %s", text)
	}
	if text := encode(SubmissionRecord{Id: 1, ConversationId: 2, Type: SubmissionTypeInput, Status: SubmissionQueued, Trace: telemetry.TelemetryCarrier{"k": "v"}}); !strings.HasPrefix(text, `{"trace":{"k":"v"},"id":1`) {
		t.Fatalf("a submission with a carrier encodes %s", text)
	}
	bare := TaskRecord[JsonValue, JsonValue, JsonValue]{Id: 1}
	if text := encode(bare); strings.Contains(text, "trace") || strings.Contains(text, "links") {
		t.Fatalf("a task without carriers encodes %s", text)
	}
	bare.Links = []telemetry.TelemetryCarrier{}
	if text := encode(bare); !strings.Contains(text, `"links":[]`) {
		t.Fatalf("a task with empty links encodes %s", text)
	}
	bare.Trace, bare.Links = telemetry.TelemetryCarrier{"a": "1"}, []telemetry.TelemetryCarrier{{"b": "2"}}
	text := encode(bare)
	if !strings.Contains(text, `"trace":{"a":"1"}`) || !strings.Contains(text, `"links":[{"b":"2"}]`) {
		t.Fatalf("a task with carriers encodes %s", text)
	}
	var decoded TaskRecord[JsonValue, JsonValue, JsonValue]
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Trace["a"] != "1" || len(decoded.Links) != 1 || decoded.Links[0]["b"] != "2" {
		t.Fatalf("decoded %+v", decoded)
	}
	// A record written before the carriers decodes without them.
	var old TaskRecord[JsonValue, JsonValue, JsonValue]
	if err := json.Unmarshal([]byte(`{"id":1}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.Trace != nil || old.Links != nil {
		t.Fatalf("a record without carriers decoded %+v", old)
	}
}
