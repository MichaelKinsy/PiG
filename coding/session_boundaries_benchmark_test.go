package coding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Each sample owns a Session, a native boundary handler, two provider requests, a committed custom entry, acknowledged event delivery and joined shutdown. It measures before-settle delivery, not disk history or subprocess IPC.
func BenchmarkSessionBeforeSettleBoundary(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{0, 64, 65536} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			content := strings.Repeat("x", size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				handled := false
				runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
					if handled {
						return nil, nil
					}
					handled = true
					return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "custom_message", CustomType: "benchmark", Content: content, Display: false}), nil
				}}}}}, services.CWD())
				provider := &scriptedProvider{responses: []scriptedResponse{boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("second", ai.StopReasonStop, 0)}}
				session, err := NewSession(services, SessionOptions{NoSession: true, SkipBuiltinTools: true, Runner: runner, Model: &ai.Model{ID: "faux-1", Provider: provider}})
				if err != nil {
					b.Fatal(err)
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					for event := range session.Events() {
						AcknowledgeEvent(event)
					}
				}()
				_, sendErr := session.Send(b.Context(), "start")
				flushErr := session.FlushEvents(b.Context())
				closeErr := session.Close()
				<-done
				if sendErr != nil || flushErr != nil || closeErr != nil || provider.callCount() != 2 {
					b.Fatalf("send=%v flush=%v close=%v calls=%d", sendErr, flushErr, closeErr, provider.callCount())
				}
			}
		})
	}
}
