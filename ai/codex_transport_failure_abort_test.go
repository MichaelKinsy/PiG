package ai

import (
	"context"
	"errors"
	"testing"
)

// alwaysFailingReader fails every Read with err.
type alwaysFailingReader struct{ err error }

func (r alwaysFailingReader) Read([]byte) (int, error) { return 0, r.err }

// openai-codex-responses.ts:361, 372: the catch reads `options?.signal?.aborted` when it handles the failure, not when the socket failed, and rethrows without a
// provider_transport_failure diagnostic when it is set. In Go the socket's feed marks a read failure as a transport failure while the request is live and the parser
// handles it afterwards, so an abort can fall between the two: the parser then records nothing, as Pi's catch does, and records the diagnostic when the request is
// still live.
func TestCodexTransportFailureHandledAfterTheAbortRecordsNoDiagnostic(t *testing.T) {
	for _, aborted := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		builder := newAssistantStreamBuilder(ctx, APIOpenAICodexResponses, "openai-codex", "gpt-5.2")
		failure := &codexWebSocketTransportFailure{err: errors.New("socket dropped")}
		reader := &codexTransportFailureReader{Reader: alwaysFailingReader{failure}, ctx: ctx, builder: builder, transport: Transport("websocket"), requestBytes: 7}
		if aborted {
			cancel()
		}
		if _, err := reader.Read(make([]byte, 8)); !errors.Is(err, failure) {
			t.Fatalf("aborted=%v: Read = %v, want the transport failure passed on", aborted, err)
		}
		want := 1
		if aborted {
			want = 0
		}
		if got := len(builder.partial.Diagnostics); got != want {
			t.Errorf("aborted=%v: %d diagnostics %+v, want %d", aborted, got, builder.partial.Diagnostics, want)
		}
		cancel()
	}
}
