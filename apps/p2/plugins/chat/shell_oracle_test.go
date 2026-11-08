package chat_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
)

// ShellCommands fingerprints as Pi's `ShellCommands` (plugins/chat/shell.ts, loaded under Node with stubs for its runtime
// imports: oracle/fingerprints.mjs).
func TestShellCommandsFingerprintsAsPi(t *testing.T) {
	const module = "plugins/chat/shell.ts"
	want, ok := oracle.Fingerprints(t, module)[module+"#ShellCommands"]
	if !ok {
		t.Fatal("Pi exports no ShellCommands")
	}
	got := chat.ShellCommands
	if got.Name != want.Name || got.Version != want.Version || got.Fingerprint != want.Fingerprint {
		t.Errorf("ShellCommands = {%s v%d %s}, Pi has {%s v%d %s}", got.Name, got.Version, got.Fingerprint, want.Name, want.Version, want.Fingerprint)
	}
}
