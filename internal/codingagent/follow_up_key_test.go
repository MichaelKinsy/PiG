package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// hostFollowUpInput is the legacy terminal input for the host platform's first
// default app.message.followUp key: alt+enter (ESC CR) except with Windows
// keybindings (native Windows and WSL), where it is ctrl+q, as upstream
// keybindings.ts.
func hostFollowUpInput() string {
	key := keybindingDefinitionsFor(tui.HostKeybindingPlatform())["app.message.followUp"].DefaultKeys[0]
	if key == "alt+enter" {
		return "\x1b\r"
	}
	return keyIDInputs[KeyID(key)][0]
}
