package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports the event test of packages/chord/test/host/staging.test.ts (Pi 8d30e6306), and records the
// RecordingSelf.events() analogue: host_staging.go's recordingSelf.Events (Pi's INERT; the handle returned accepts Emit
// and On and does nothing).

var stagingTicks = mustContract(contracts.Local("app.ticks", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(contracts.Obj(contracts.Prop("y", contracts.Int()))))},
}))

func TestStagingHandsOutAnInertEventSourceAnEmitInStagingDeliversNothing(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	facet := func(label string) []HostFacet {
		return []HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(stagingTicks, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				ticks := self.Events()
				// Staging runs this against a recording self: its handle is inert, the emit does nothing.
				_ = ticks.Emit(jsonObject("y", float64(len(label))))
				return contracts.Implementation{Members: contracts.VariantImplementation{
					Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
				}}, nil
			}, ServeOptions{})
		})}}
	}
	_, err := host.Load(facet("v1"), background)
	must(t, err)
	ticks := mustGet(t, host, stagingTicks, jsonObject())
	var seen []any
	on(t, ticks, "ticks", recorder(&seen, ""), background)
	report, err := host.Reload(facet("v2!"), background, ReloadOptions{})
	must(t, err)
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	flushed(host)
	wantSeen(t, seen)
	must(t, host.Close(background))
}
