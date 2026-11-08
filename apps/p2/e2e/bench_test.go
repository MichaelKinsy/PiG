package e2e_test

import (
	"os"
	"strconv"
	"strings"
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
	// The process's peak resident set (Linux's VmHWM, KiB): the startup's memory, measured with the rest of the run.
	if status, err := os.ReadFile("/proc/self/status"); err == nil {
		for line := range strings.Lines(string(status)) {
			if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
				if kib, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 64); err == nil {
					b.ReportMetric(kib/1024, "peakRSS-MiB")
				}
			}
		}
	}
}
