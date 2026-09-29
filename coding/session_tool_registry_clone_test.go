package coding

import (
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestSessionToolRegistryClonePreservesMetadata(t *testing.T) {
	session := newRegistryPortSession(t, []string{"read"}, SessionOptions{CustomTools: []extension.ToolDefinition{registryTool("sdk_tool", "SDK Tool", "SDK tool", "")}, ExcludedTools: map[string]struct{}{"ls": {}}}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
		registerPortTool(registered, dynamicRegistryTool())
	})
	bindRegistryPort(t, session)
	session.SetActiveToolsByName([]string{"dynamic_tool"})
	appendAsst(t, session, "saved reply")
	clone, err := session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clone.Close(); err != nil {
			t.Error(err)
		}
	})
	if !reflect.DeepEqual(clone.GetAllTools(), session.GetAllTools()) {
		t.Fatalf("clone metadata = %+v, source = %+v", clone.GetAllTools(), session.GetAllTools())
	}
	if !slices.Equal(clone.ActiveToolNames(), session.ActiveToolNames()) {
		t.Fatalf("clone active = %v", clone.ActiveToolNames())
	}
}
