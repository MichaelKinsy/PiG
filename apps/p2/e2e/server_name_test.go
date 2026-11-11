// Ports test/p2/server-name.test.ts (Pi main b22e525b3), the cases that run a server: the owner's name for it in
// identity.json, else the device's. (The file's own cases are runtime/server_name_test.go; Radius following a rename is in
// radius_test.go.)

package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// serverNames holds the owner's ServerNameSettings on fixture.
func serverNames(t *testing.T, fixture *p2test.Fixture) *host.Reference {
	t.Helper()
	ref, err := p2test.HoldIn(fixture.Browser(), p2runtime.ServerNameSettings, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

// deviceNameValue is the device's name as the server states it: the text, or nil when the device has none.
func deviceNameValue() any {
	if name, ok := p2runtime.DeviceName(); ok {
		return name
	}
	return nil
}

func serverNameIs(t *testing.T, ref *host.Reference, member string, want any) {
	t.Helper()
	if _, err := p2test.Until(ref.Member(member), func(value delta.JsonValue) bool { return value == want }, 0); err != nil {
		t.Fatalf("%s never became %v: %v", member, want, err)
	}
}

func rename(t *testing.T, ref *host.Reference, name any) {
	t.Helper()
	if _, err := ref.Call("rename", delta.JsonObjectOf("name", name), p2test.Background); err != nil {
		t.Fatal(err)
	}
}

func stateOf(t *testing.T, ref *host.Reference, member string) any {
	t.Helper()
	value, err := ref.Member(member).Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTheOwnerRenamesTheServerAndBackToTheDevicesNameSavedAcrossRestarts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	device := deviceNameValue()
	first := p2test.StartFixture(t, hosts.ServerOptions{DataDir: directory})
	names := serverNames(t, first)
	serverNameIs(t, names, "name", device)
	if got := stateOf(t, names, "chosen"); got != nil {
		t.Fatalf("chosen = %v, want null", got)
	}
	if got := stateOf(t, names, "device"); got != device {
		t.Fatalf("device = %v, want %v", got, device)
	}
	if _, err := os.Stat(filepath.Join(directory, "identity.json")); err == nil {
		t.Fatal("identity.json exists before the owner named the server")
	}

	rename(t, names, "  Studio\tbrain ")
	if got := stateOf(t, names, "name"); got != "Studio brain" {
		t.Fatalf("name = %v, want Studio brain", got)
	}
	if got := stateOf(t, names, "chosen"); got != "Studio brain" {
		t.Fatalf("chosen = %v, want Studio brain", got)
	}
	if text, err := os.ReadFile(filepath.Join(directory, "identity.json")); err != nil || !regexp.MustCompile(`^\{\s*"name": "Studio brain"\s*\}\s*$`).Match(text) {
		t.Fatalf("identity.json = %q (%v)", text, err)
	}

	first.Close()
	second := p2test.StartFixture(t, hosts.ServerOptions{DataDir: directory})
	names = serverNames(t, second)
	serverNameIs(t, names, "name", "Studio brain")

	// Nothing left of a name is no name: the device's stands again, and the file is gone.
	rename(t, names, " \n ")
	if got := stateOf(t, names, "name"); got != device {
		t.Fatalf("name = %v, want the device's %v", got, device)
	}
	if got := stateOf(t, names, "chosen"); got != nil {
		t.Fatalf("chosen = %v, want null", got)
	}
	if _, err := os.Stat(filepath.Join(directory, "identity.json")); err == nil {
		t.Fatal("identity.json is still there after the name was cleared")
	}
	rename(t, names, "Lab")
	rename(t, names, nil)
	if got := stateOf(t, names, "name"); got != device {
		t.Fatalf("name = %v, want the device's %v", got, device)
	}
}

func TestABrokenIdentityJSONDoesNotKeepTheServerFromStartingItGoesByTheDevicesName(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "identity.json"), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := p2test.StartFixture(t, hosts.ServerOptions{DataDir: directory})
	names := serverNames(t, fixture)
	serverNameIs(t, names, "name", deviceNameValue())
	usable := regexp.MustCompile(`identity\.json is not usable`)
	found := false
	for _, notice := range p2runtime.ProcessLogs.Notices() {
		found = found || usable.MatchString(notice.Message)
	}
	if !found {
		t.Fatal("no notice says that identity.json is not usable")
	}
	// A rename replaces the file.
	rename(t, names, "Studio")
	if text, err := os.ReadFile(filepath.Join(directory, "identity.json")); err != nil || !regexp.MustCompile(`^\{\s*"name": "Studio"\s*\}\s*$`).Match(text) {
		t.Fatalf("identity.json = %q (%v)", text, err)
	}
}

func TestOnlyTheOwnerGetsTheServersNameSettings(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	guest := fixture.As(p2runtime.GuestPrincipal{ID: "guest", Name: "Guest", Home: "unknown", Sessions: map[string]p2runtime.GuestMode{"unknown": "full"}, Revision: 1})
	denied, err := guest.Hold(p2test.Background, p2runtime.ServerNameSettings, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
	denied.Release()
}
