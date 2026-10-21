package session

// pi: packages/durable/src/session/transaction.ts (`this.#context = BACKGROUND_CONTEXT`; Pi 2ec55d80)

import (
	"context"
	"errors"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// A transaction that outlives its commit does not keep the caller's telemetry span reachable, however the commit ends.
func TestSettledTransactionDoesNotRetainTheCallersTelemetrySpan(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	for name, change := range map[string]func(tx *Transaction) (any, error){
		"success": func(*Transaction) (any, error) { return nil, nil },
		"failure": func(*Transaction) (any, error) { return nil, errors.New("rollback") },
	} {
		t.Run(name, func(t *testing.T) {
			kernel := NewSessionImpl(storage.NewMemoryStorage(), Hooks{}, nil, nil)
			defer func() { _ = kernel.Close(context.Background()) }()
			var kept *Transaction
			var inside telemetry.TelemetryContext
			err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) error {
				_, _ = kernel.CommitWith(chordctx.WithTelemetry(context.Background(), span), func(tx *Transaction) (any, error) {
					kept = tx
					inside = chordctx.Telemetry(tx.ctx)
					return change(tx)
				}, TransactionScope{})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if inside == telemetry.TelemetryContext(telemetry.NoopTelemetryContext) {
				t.Fatal("the transaction did not carry the caller's span while it ran")
			}
			if got := chordctx.Telemetry(kept.ctx); got != telemetry.NoopTelemetryContext {
				t.Fatalf("a settled transaction still holds %T", got)
			}
		})
	}
}
