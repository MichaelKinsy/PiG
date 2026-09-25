package subprocess

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type loginRecordingUI struct {
	*testUIContext
	logins chan extension.LoginDefinition
}

func (ui *loginRecordingUI) SetLogin(definition extension.LoginDefinition) error {
	ui.logins <- definition
	return nil
}

func TestNodeRuntimeSetLoginSendsCanonicalDefinitionOnce(t *testing.T) {
	shortSockDir(t)

	ui := &loginRecordingUI{
		testUIContext: newTestUIContext(),
		logins:        make(chan extension.LoginDefinition, 2),
	}
	h := newTestHost(t)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	h.SetUIBridge(bridge)
	defer h.Shutdown("test done")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ext, err := h.Load(ctx, ExtConfig{
		Name:    "node-login",
		Source:  filepath.Join("testdata", "node-login.mjs"),
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := ext.Commands["set_node_login"].Handler(ctx, ""); err != nil {
		t.Fatalf("set_node_login: %v", err)
	}

	select {
	case definition := <-ui.logins:
		if definition.Name != "Node Login" || definition.Description != "runtime adapter" || definition.Tagline != "one host call" {
			t.Fatalf("definition = %#v", definition)
		}
	case <-ctx.Done():
		t.Fatal("ui.setLogin did not reach the host")
	}

	invalid := ext.Commands["set_invalid_node_login"]
	if invalid.Handler == nil {
		t.Fatal("set_invalid_node_login command not registered")
	}
	if err := invalid.Handler(ctx, ""); err == nil || !strings.Contains(err.Error(), "invalid login definition") {
		t.Fatalf("invalid Node login error = %v", err)
	}

	h.BroadcastStateUpdate()
	select {
	case definition := <-ui.logins:
		t.Fatalf("ui.setLogin entered a render/update loop: %#v", definition)
	case <-time.After(200 * time.Millisecond):
	}
}
