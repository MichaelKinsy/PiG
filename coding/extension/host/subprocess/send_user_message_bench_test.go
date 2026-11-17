package subprocess

import (
	"encoding/json"
	"testing"
)

// BenchmarkSendUserMessageAccepted is the host's handling of a sendUserMessage the Session accepts (a delivery named, so it cannot be refused): the
// parse of the call, the options and the bound action. The send pays for no failure report (sync_run_report.go).
func BenchmarkSendUserMessageAccepted(b *testing.B) {
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("sendUserMessage", func(any, SendUserMessageOptions) error { return nil })
	call := &CallPayload{Method: "sendUserMessage", Args: json.RawMessage(`{"content":"hello","options":{"deliverAs":"steer"}}`)}
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := bridge.handleCall(ctx, "ext", nil, call); err != nil {
			b.Fatal(err)
		}
	}
}
