package e2e_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// BenchmarkStartPlugins is the startup of BenchmarkStartFixture with a growing plugin set: the plugins M1 shipped with, then each
// plugin added since, one at a time and cumulative. The cost of a plugin is the difference of its row to the one before; the
// first row is the base the later ones are compared with (benchstat over -count=10, one container, interleaved).
func BenchmarkStartPlugins(b *testing.B) {
	set := []string{"chat", "drafts", "envs", "models", "sessions", "status", "subagents"}
	for _, added := range []string{"", "files", "logs", "sharing", "artifacts", "send-file"} {
		if added != "" {
			set = append(set, added)
		}
		name := "M1"
		if added != "" {
			name = "plus-" + added
		}
		plugins := append([]string(nil), set...)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fixture := p2test.StartFixture(b, hosts.ServerOptions{Plugins: plugins})
				if _, err := p2test.HoldIn(fixture.Browser(), sessionscontract.Sessions, delta.NewJsonObject(0), "owner"); err != nil {
					b.Fatal(err)
				}
				fixture.Close()
			}
		})
	}
}
