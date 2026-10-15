package extension

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// pi-tui Component (render, invalidate) is what a ToolDefinition renderer, an EntryRenderer and a MessageRenderer return; the host mounts it as it is, without an assertion.
func TestComponentIsTheTUIComponent(t *testing.T) {
	if reflect.TypeFor[Component]() != reflect.TypeFor[tui.Component]() {
		t.Fatalf("extension.Component = %v, want tui.Component", reflect.TypeFor[Component]())
	}
}
