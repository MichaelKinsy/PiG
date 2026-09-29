package subprocess

import (
	"errors"
	"strings"
	"testing"
)

func TestDynamicToolValidationAndRefreshErrors(t *testing.T) {
	h := NewHost(t.TempDir())
	me := &managedExt{config: ExtConfig{Name: "dynamic"}}
	me.ext = h.buildExtension(me, &RegisterPayload{Name: "dynamic", Tools: []ToolDecl{{Name: "tool", Description: "original", Parameters: []byte(`{}`)}}})
	h.exts["dynamic"] = me
	for _, args := range []string{`{"name":"tool","parameters":null}`, `{"name":"tool","parameters":[]}`, `{"name":"tool"}`} {
		if _, err := h.handleToolRegistration(t.Context(), me, me.connection(), &CallPayload{Args: []byte(args)}); err == nil {
			t.Fatal("accepted invalid schema", args)
		}
		if got := me.ext.RegisteredTools(); len(got) != 1 || got[0].Definition.Description != "original" {
			t.Fatal("invalid registration replaced a valid tool")
		}
	}
	bridge := NewUIBridge(func() {})
	bridge.SetHostAction("refreshTools", func() error { return errors.New("refresh failed") })
	h.SetUIBridge(bridge)
	if _, err := h.handleToolRegistration(t.Context(), me, me.connection(), &CallPayload{Args: []byte(`{"name":"tool","description":"new","parameters":{}}`)}); err == nil || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("refresh error lost: %v", err)
	}
	delete(h.exts, "dynamic")
	if _, err := h.handleToolRegistration(t.Context(), me, me.connection(), &CallPayload{Args: []byte(`{"name":"late","parameters":{}}`)}); err == nil {
		t.Fatal("retired connection changed registration")
	}
}
