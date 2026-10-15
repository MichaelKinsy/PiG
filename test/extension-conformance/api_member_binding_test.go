package extensionconformance

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// requireAPIMember fails unless member is the extension.API method expression named want. The conformance tests bind each Pi
// ExtensionAPI member they drive over the SDK wire to the Go reference member of the same name.
func requireAPIMember(t *testing.T, want string, member any) {
	t.Helper()
	if reflect.TypeOf(member).Kind() != reflect.Func {
		t.Errorf("%s: not a method expression", want)
		return
	}
	if name := runtime.FuncForPC(reflect.ValueOf(member).Pointer()).Name(); !strings.HasSuffix(name, "."+want) {
		t.Errorf("bound to %s, want a member named %s", name, want)
	}
}

// requireEventsMember binds ExtensionAPI.events to the Go reference member that returns the EventBus every SDK's bus bridges to.
func requireEventsMember(t *testing.T) {
	t.Helper()
	requireAPIMember(t, "Events", extension.API.Events)
}
