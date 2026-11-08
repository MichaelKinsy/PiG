package app

import (
	"sync"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// host.ts `principalAttributes`: the hook the app gives (`AppOptions.PrincipalAttributes`, `createHost`'s `principalAttributes`) is the server
// host's: a span the host records for a principal's work carries the attributes the hook returns for that principal. Without the hook
// the host stamps `chord.principal` (the principal's key), so the hook's value in its place shows the host used it.
func TestTheAppsHookStampsTheServerHostsOwnSpans(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := &ingested{TelemetryContext: recorder}
	var mu sync.Mutex
	var asked []data.Principal
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.Telemetry = store
		o.PrincipalAttributes = func(principal data.Principal) telemetry.SpanAttributes {
			mu.Lock()
			asked = append(asked, principal)
			mu.Unlock()
			return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "the-hook")}
		}
	})
	// A call records its span under the telemetry parent its context carries (a context with none records nothing).
	err := recorder.StartSpan(telemetry.SpanOptions{Name: "test.root"}, func(parent telemetry.TelemetrySpan) error {
		_, err := s.app.Host.Root(func(ctx host.Ctx) (any, error) {
			sessions, err := ctx.Get(Sessions, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			// `verify` is a traced method call of the kit's sessions object, made as the owner: its `chord.method` span carries the hook's attributes.
			return sessions.Call("verify", "no-such-token", ctx)
		}, chordctx.WithTelemetry(background, parent), host.RootOptions{Principal: testOwner})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := len(asked)
	mu.Unlock()
	if count == 0 {
		t.Fatal("the app's hook was never asked: the server host does not use AppOptions.PrincipalAttributes")
	}
	stamped := false
	for _, span := range recorder.GetSpans() {
		if value, ok := span.Attributes.Get("chord.principal"); ok && value == "the-hook" {
			stamped = true
		}
	}
	if !stamped {
		t.Fatalf("no span the host recorded carries the hook's attributes: %v", recorder.GetSpans())
	}
}
