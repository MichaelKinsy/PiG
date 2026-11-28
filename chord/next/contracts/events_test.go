package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestRemoteAndLocalEvents(t *testing.T) {
	c, err := contracts.Remote("ev", contracts.RemoteDeclaration{
		Version:     1,
		Params:      props(),
		HostAddress: serverAddress,
		Events:      []contracts.RemoteEventEntry{contracts.EventEntry("ping", contracts.Str())},
	})
	wantNoError(t, err)
	if !currentMembers(t, c).Events.Has("ping") {
		t.Fatal("missing event ping")
	}
	// Bytes in event refused
	_, err = contracts.Remote("ev.bad", contracts.RemoteDeclaration{
		Version:     1,
		Params:      props(),
		HostAddress: serverAddress,
		Events:      []contracts.RemoteEventEntry{contracts.EventEntry("bin", contracts.Bytes())},
	})
	wantError(t, err, "JSON only")
}
