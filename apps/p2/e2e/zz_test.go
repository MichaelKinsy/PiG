package e2e_test

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

func dumpLogs(t *testing.T) {
	for _, n := range p2runtime.ProcessLogs.Notices() {
		t.Logf("notice %+v", n)
	}
	for _, s := range p2runtime.ProcessLogs.Sources() {
		for _, l := range p2runtime.ProcessLogs.Lines(s.ID) {
			t.Logf("%s %v", s.ID, l)
		}
	}
}

func TestZZ2(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	t.Cleanup(func() { dumpLogs(t) })
	f.send(`subagent {"action":"spawn","name":"bad","message":"x","cwd":"missing"}`)
	f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool { return hasText(v, hasSubstring("is not an existing")) })
	t.Log("sending second")
	f.send(`subagent {"action":"spawn","name":"bad2","message":"x","cwd":"missing2"}`)
	t.Log("sent")
}
