package coding

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Each sample owns a fresh Session, an operations-backed bash call, output-event delivery, and joined shutdown. It does not measure shell startup or growing-history append cost.
func BenchmarkSessionBashLifecycle(b *testing.B) {
	b.Setenv("PIG_HOME", b.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	for _, size := range []int{0, 64, 4096, 16384} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			data := []byte(strings.Repeat("x", size))
			operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
				options.OnData(data)
				return extension.BashOperationsResult{ExitCode: new(0)}, nil
			})
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				session, err := NewSession(services, SessionOptions{NoSession: true, SkipBuiltinTools: true})
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
				_, runErr := session.ExecuteBash(b.Context(), "custom", nil, &ExecuteBashOptions{Operations: operations, ID: new("benchmark")})
				flushErr := session.FlushEvents(b.Context())
				closeErr := session.Close()
				<-done
				if runErr != nil || flushErr != nil || closeErr != nil {
					b.Fatalf("run=%v flush=%v close=%v", runErr, flushErr, closeErr)
				}
			}
		})
	}
}
