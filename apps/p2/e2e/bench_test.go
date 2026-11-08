package e2e_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// BenchmarkStartFixture is the server's startup: every registered plugin loaded under the fake provider, from nothing
// to a server that answers its first client (a browser holds Sessions, live), then closed. Run it with benchstat over
// -count=10 on the base and the tip, interleaved in one container.
func BenchmarkStartFixture(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		fixture := p2test.StartFixture(b, hosts.ServerOptions{})
		if _, err := p2test.HoldIn(fixture.Browser(), sessionscontract.Sessions, delta.NewJsonObject(0), "owner"); err != nil {
			b.Fatal(err)
		}
		fixture.Close()
	}
}
