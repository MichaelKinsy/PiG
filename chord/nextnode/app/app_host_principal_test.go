package app

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/telemetryingest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// host.ts `principalAttributes`: the hook the app gives (`AppOptions.PrincipalAttributes`, `createHost`'s `principalAttributes`) is the server
// host's: a span the host records for a principal's work carries the attributes the hook returns for that principal. Without the hook
// the host stamps `chord.principal` (the principal's key), so the hook's value in its place shows the host used it.
func TestTheAppsHookStampsTheServerHostsOwnSpans(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	store := &ingested{TelemetryContext: recorder}
	var asked []data.Principal
	s := startTestApp(t, nil, func(o *AppOptions) {
		o.Telemetry = store
		o.PrincipalAttributes = func(principal data.Principal) telemetry.SpanAttributes {
			asked = append(asked, principal)
			return telemetry.SpanAttributes{telemetry.Attr("chord.principal", "the-hook")}
		}
	})
	_, err := s.app.Host.Root(func(ctx host.Ctx) (any, error) {
		page, err := ctx.Get(telemetryingest.PageTelemetry, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return page.Call("ingest", delta.JsonObjectOf("spans", []any{}), ctx)
	}, background, host.RootOptions{Principal: testOwner})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) == 0 {
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
