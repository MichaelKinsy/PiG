package subprocess

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestUIBridgeStructuredUserMessagePreservesPayloadAndErrors(t *testing.T) {
	bridge := newTestBridge(&mockUIContext{})
	var got any
	calls := 0
	bridge.SetActions(&HostCallbacks{SendUserMessage: func(content any, options SendUserMessageOptions) error {
		calls++
		got = content
		if options.DeliverAs != "steer" {
			t.Fatalf("options %+v", options)
		}
		return errors.New("rejected by session")
	}})
	result, err := call(bridge, "sendUserMessage", `{"content":[{"type":"text","text":"hello"},{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"}],"options":{"deliverAs":"steer"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.Error == nil || result.Error.Code != "send_failed" || result.Error.Message != "rejected by session" {
		t.Fatalf("calls %d result %+v", calls, result)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"text":"hello","type":"text"},{"data":"aW1hZ2U=","mimeType":"image/png","type":"image"}]` {
		t.Fatalf("payload %s", raw)
	}
}
