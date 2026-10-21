package modeltypes

import (
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// tuiCaps is a capability set as the tui_probe reports it.
func tuiCaps(caps tui.TerminalCapabilities) map[string]any {
	var images any
	if caps.Images != "" {
		images = string(caps.Images)
	}
	return map[string]any{"images": images, "trueColor": caps.TrueColor, "hyperlinks": caps.Hyperlinks}
}

func stringList(items []string) []any {
	out := []any{}
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// registerTuiProbe registers tui_probe: what pi-tui's keybindings and capabilities answer once the host published its state (testdata/model-types.mjs and the Rust and Python fixtures carry the same tool).
func registerTuiProbe(e *sdk.Extension) {
	e.RegisterTool(sdk.ToolDefinition{
		Name: "tui_probe", Label: "tui_probe", Description: "Reads pi-tui's keybindings and terminal capabilities.", Parameters: sdk.Schema{"type": "object"},
		Execute: func(_ sdk.Context, args map[string]any) (any, error) {
			ids, _ := args["ids"].([]any)
			inputs, _ := args["inputs"].([]any)
			keybindings := tui.GetKeybindings()
			keys, definitions, resolvedIds := map[string]any{}, map[string]any{}, []any{}
			for _, raw := range ids {
				id, _ := raw.(string)
				keys[id] = stringList(keybindings.GetKeys(id))
				if definition, ok := keybindings.GetDefinition(id); ok && definition.Description != "" {
					definitions[id] = definition.Description
				} else {
					definitions[id] = nil
				}
			}
			resolved := keybindings.GetResolvedBindings()
			for id := range resolved {
				resolvedIds = append(resolvedIds, id)
			}
			slices.SortFunc(resolvedIds, func(a, b any) int { return compareStrings(a.(string), b.(string)) })
			matches := []any{}
			for _, raw := range inputs {
				input, _ := raw.(map[string]any)
				data, _ := input["data"].(string)
				id, _ := input["id"].(string)
				matches = append(matches, keybindings.Matches(data, id))
			}
			user := map[string]any{}
			for id, bound := range keybindings.GetUserBindings() {
				user[id] = stringList(bound)
			}
			conflicts := []any{}
			for _, conflict := range keybindings.GetConflicts() {
				conflicts = append(conflicts, map[string]any{"key": conflict.Key, "keybindings": stringList(conflict.Keybindings)})
			}
			defaults := map[string]any{"count": len(tui.TUIKeybindings)}
			for _, id := range []string{"tui.input.submit", "tui.select.confirm"} {
				defaults[id] = stringList(tui.TUIKeybindings[id].DefaultKeys)
			}
			caps := tui.GetCapabilities()
			report := map[string]any{
				"keys": keys, "matches": matches, "definitions": definitions, "user": user, "conflicts": conflicts, "resolvedIds": resolvedIds, "defaults": defaults,
				"key":          map[string]any{"ctrl": tui.Key.Ctrl("x"), "alt": tui.Key.Alt(tui.Key.Enter), "shift": tui.Key.Shift(tui.Key.Tab), "escape": tui.Key.Escape, "ctrlShift": tui.Key.CtrlShift("p")},
				"capabilities": tuiCaps(caps), "colorMode": string(tui.GetTerminalColorMode()),
			}

			// Install, then put back what the host published.
			installed := tui.NewKeybindingsManager(tui.KeybindingDefinitions{"x.probe": {DefaultKeys: []string{"ctrl+q"}}}, nil)
			tui.SetKeybindings(installed)
			report["installed"] = map[string]any{"keys": stringList(tui.GetKeybindings().GetKeys("x.probe")), "hostKeys": stringList(tui.GetKeybindings().GetKeys("tui.input.submit"))}
			installed.SetUserBindings(tui.KeybindingsConfig{"x.probe": {"ctrl+w"}})
			report["installed"].(map[string]any)["rebound"] = stringList(tui.GetKeybindings().GetKeys("x.probe"))
			tui.SetKeybindings(keybindings)
			report["installed"].(map[string]any)["restored"] = stringList(tui.GetKeybindings().GetKeys("tui.input.submit"))

			tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
			swapped := map[string]any{"capabilities": tuiCaps(tui.GetCapabilities()), "colorMode": string(tui.GetTerminalColorMode())}
			hyperlinks := true
			tui.SetCapabilityOverrides(tui.CapabilityOverrides{Hyperlinks: &hyperlinks})
			swapped["overridden"] = tui.GetCapabilities().Hyperlinks
			tui.SetCapabilityOverrides(tui.CapabilityOverrides{})
			tui.ResetCapabilitiesCache()
			// detectCapabilities reads the environment, which the host's state does not replace; the probe only proves the call answers.
			tui.DetectCapabilities(func() bool { return false })
			swapped["detected"] = true
			tui.SetCapabilities(caps)
			swapped["restored"] = tuiCaps(tui.GetCapabilities())
			before := tui.IsKittyProtocolActive()
			tui.SetKittyProtocolActive(true)
			after := tui.IsKittyProtocolActive()
			tui.SetKittyProtocolActive(before)
			swapped["kitty"] = []any{before, after, tui.IsKittyProtocolActive()}
			report["swapped"] = swapped
			return text(report)
		},
	})
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
