//go:build !race

package subprocess

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A registerTool made after load costs the host the same at any catalog size: the reply carries none of the catalog, and the next state push is not a full snapshot, because no runtime replica was written outside a state_update. The host actions the registration reaches (getAllTools, as the Session lists its registry) cost the size of the registry whenever they run, so a handler that calls them scales with the catalog and this test fails.
func TestRegisteringAToolAfterLoadAllocatesTheSameAtAnyCatalogSize(t *testing.T) {
	perRegistration := func(existing int) float64 {
		rig := newPushRig(t)
		rig.me.ext = &extension.Extension{Name: "push"}
		rig.me.ext.InitializeToolRegistry()
		rig.bridge.SetHostAction("getAllTools", func() []ToolInfo {
			var out []ToolInfo
			for _, registered := range rig.me.ext.RegisteredTools() {
				out = append(out, ToolInfo{Name: registered.Definition.Name, Description: registered.Definition.Description, Parameters: registered.Definition.Parameters, Exposure: extension.ToolExposureDirect, Source: "extension"})
			}
			return out
		})
		rig.bridge.SetHostAction("getActiveTools", func() []string { return rig.me.ext.RegisteredToolNames() })
		rig.bridge.SetHostAction("refreshTools", func() error { return nil })
		go func() {
			for {
				select {
				case <-rig.frames:
				case <-t.Context().Done():
					return
				}
			}
		}()
		next := 0
		register := func() {
			next++
			args := json.RawMessage(fmt.Sprintf(`{"name":"dyn_%d","description":"A dynamic tool","parameters":{"type":"object","properties":{"path":{"type":"string","description":"The path to the file"}},"required":["path"]}}`, next))
			result, err := rig.h.handleToolRegistration(t.Context(), rig.me, rig.conn, &CallPayload{Method: CallRegisterTool, Args: args})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Result) != 0 {
				t.Errorf("registerTool replied %s, want an empty result", result.Result)
			}
			if err := rig.h.pushStateTo(t.Context(), rig.me, rig.conn); err != nil {
				t.Fatal(err)
			}
		}
		rig.push(t)
		for range existing {
			register()
		}
		return testing.AllocsPerRun(30, register)
	}
	small, large := perRegistration(50), perRegistration(200)
	t.Logf("allocations per registration: %.0f with 50 tools registered, %.0f with 200", small, large)
	if large > small*1.25 {
		t.Fatalf("a registration allocates %.0f times with 50 tools registered and %.0f with 200; its cost must not grow with the catalog", small, large)
	}
}
