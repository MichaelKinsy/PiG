package subprocess

import (
	"encoding/json"
	"slices"
)

// terminalCapabilitiesEnv carries the host terminal's resolved capabilities to a Node extension process as TerminalCapabilitiesPayload JSON.
//
// Pi resolves terminal capabilities once per process, and the extensions it loads share that cache (packages/tui/src/terminal-image.ts:34,160-169). Pi's theme initialization reads them (packages/coding-agent/src/main.ts:853,887; modes/interactive/theme/theme.ts:529). The host owns PiG's terminal, so the Node runtime seeds pi-tui's cache from this value before it initializes Pi's theme and loads an extension, instead of probing the terminal a second time. Under tmux that probe is a synchronous subprocess (terminal-image.ts:53-67).
const terminalCapabilitiesEnv = "PIG_TERMINAL_CAPABILITIES"

// withNodeRuntimeEnv returns env for a Node runtime process. It replaces any inherited capability seed with this host's resolved capabilities, or removes it when the host has no capability source.
func (h *Host) withNodeRuntimeEnv(env []string) []string {
	env = slices.DeleteFunc(env, func(value string) bool { return envHasKey(value, terminalCapabilitiesEnv) })
	if h.uiBridge == nil {
		return env
	}
	h.uiBridge.mu.RLock()
	capabilities := h.uiBridge.terminalCapabilities
	h.uiBridge.mu.RUnlock()
	if capabilities == nil {
		return env
	}
	data, err := json.Marshal(capabilities())
	if err != nil {
		return env
	}
	return append(env, terminalCapabilitiesEnv+"="+string(data))
}
