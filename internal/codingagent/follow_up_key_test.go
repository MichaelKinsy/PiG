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

// hostKittyFollowUpInput is the Kitty-protocol input for the host platform's first default app.message.followUp key: Alt+Enter, or Ctrl+Q with Windows
// keybindings (native Windows and WSL).
func hostKittyFollowUpInput() string {
	if keybindingDefinitionsFor(tui.HostKeybindingPlatform())["app.message.followUp"].DefaultKeys[0] == "alt+enter" {
		return "\x1b[13;3u"
	}
	return "\x1b[113;5u"
}

// hostCycleBackwardInput is the terminal input for the host platform's first default app.model.cycleBackward key: Shift+Ctrl+P (Kitty), or Alt+P with
// Windows keybindings.
func hostCycleBackwardInput() string {
	if keybindingDefinitionsFor(tui.HostKeybindingPlatform())["app.model.cycleBackward"].DefaultKeys[0] == "shift+ctrl+p" {
		return "\x1b[112;6u"
	}
	return "\x1bp"
}
