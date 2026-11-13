package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A Runner asks the UI context it is bound to whether the caller runs the input and render loop. A Runner without such a context,
// as in print and RPC modes, never reports a caller on a loop.
func TestRunnerCallerOnOwnerLoopAsksTheBoundUIContext(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{}, t.TempDir(), extension.CreateExtensionRuntime())
	if runner.CallerOnOwnerLoop() {
		t.Fatal("a Runner with no UI context reported a caller on the owner loop")
	}
	runner.SetUIContext(ownerLoopUI{extension.NoopUIContext}, extension.ModeTUI)
	if !runner.CallerOnOwnerLoop() {
		t.Fatal("a Runner bound to a UI context on its loop did not report the caller on it")
	}
	runner.SetUIContext(extension.NoopUIContext, extension.ModePrint)
	if runner.CallerOnOwnerLoop() {
		t.Fatal("a Runner bound to a UI context without a loop reported a caller on the owner loop")
	}
}
