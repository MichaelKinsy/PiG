package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type recordingExtensionHost struct{ name string }

func (*recordingExtensionHost) UIBridge() *subprocess.UIBridge     { return nil }
func (*recordingExtensionHost) FlushCommands()                     {}
func (*recordingExtensionHost) SetRuntimeDrainHandler(func())      {}
func (*recordingExtensionHost) SetSettleTailHandler(func(int))     {}
func (*recordingExtensionHost) SetCommandSuspendHandler(func(int)) {}
func (*recordingExtensionHost) SetCommandWindowHandler(func(int))  {}
func (*recordingExtensionHost) EndInput()                          {}
func (*recordingExtensionHost) HoldRetirements()                   {}
func (*recordingExtensionHost) TerminateProcesses()                {}

// A mode reaches the extension host of a Session through the Session itself, as Pi's modes reach the runner through AgentSession: every Session a Runtime creates, and every clone of it, carries RuntimeOptions.ExtensionHost.
func TestSessionCarriesTheExtensionHostOfItsRuntime(t *testing.T) {
	host := &recordingExtensionHost{name: "build"}
	rt, err := NewRuntime(RuntimeOptions{Services: newTestServices(t), ExtensionHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if got := sess.ExtensionHost(); got != ExtensionHost(host) {
		t.Fatalf("Session.ExtensionHost() = %v, want the Runtime's host", got)
	}
	appendUser(t, sess, "turn")
	appendAsst(t, sess, "reply")
	clone, err := sess.Clone()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clone.Close() }()
	if got := clone.ExtensionHost(); got != ExtensionHost(host) {
		t.Fatalf("clone ExtensionHost() = %v, want the source Session's host", got)
	}

	bare, err := NewRuntime(RuntimeOptions{Services: newTestServices(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bare.Close() }()
	other, err := bare.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if got := other.ExtensionHost(); got != nil {
		t.Fatalf("Session of a Runtime without a host: ExtensionHost() = %v, want nil", got)
	}
}
