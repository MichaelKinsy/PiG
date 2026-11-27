package codingagent

import (
	"strings"
	"testing"
)

// d2ProductNameTexts maps the fragments of Pi's messages that name the product to PiG's (D2, "Product name in messages",
// docs/parity/divergences/D2.md). A test that states Pi's text compares with PiG's through d2Text, so the one difference
// between the two is this table.
var d2ProductNameTexts = [][2]string{
	{"Session file is not a valid pi session: ", "Session file is not a valid pig session: "},
	{"Pi works best with csi-u", "PiG works best with csi-u"},
	{"instead of Pi's drawn cursor", "instead of PiG's drawn cursor"},
}

// d2Text is Pi's text with each mapped fragment replaced by PiG's. A text that holds no mapped fragment is a mistake in the
// test: it panics.
func d2Text(piText string) string {
	pigText := piText
	for _, pair := range d2ProductNameTexts {
		pigText = strings.ReplaceAll(pigText, pair[0], pair[1])
	}
	if pigText == piText {
		panic("d2Text: no mapped product-name fragment in " + piText)
	}
	return pigText
}

// Pi's tmux warning (interactive-mode.ts checkTmuxKeyboardSetup) says "Pi works best with csi-u".
func TestTmuxFormatWarningNamesPiG(t *testing.T) {
	got := tmuxKeyboardSetup(func(option string) string {
		if option == "extended-keys-format" {
			return "xterm"
		}
		return "on"
	})
	want := d2Text("tmux extended-keys-format is xterm. Pi works best with csi-u. Add `set -g extended-keys-format csi-u` to ~/.tmux.conf and restart tmux.")
	if got != want {
		t.Fatalf("warning = %q, want %q", got, want)
	}
}

// Pi's settings-selector.ts describes the row as "Use the terminal cursor instead of Pi's drawn cursor".
func TestShowHardwareCursorDescriptionNamesPiG(t *testing.T) {
	item := settingsSelectorRow(t, SettingsConfig{}, "show-hardware-cursor")
	if want := d2Text("Use the terminal cursor instead of Pi's drawn cursor"); item.Description != want {
		t.Fatalf("description = %q, want %q", item.Description, want)
	}
}

// PIG_TELEMETRY is PiG's name beside Pi's PI_TELEMETRY: it decides alone, with the same truthy spellings, and wins when both are set

// The remediation of an installation that cannot update itself names PiG's variable.
func TestUnsupportedRemediationNamesPigSelfUpdateCommand(t *testing.T) {
	if got := UnsupportedRemediation("/opt/pig/bin/pig"); !strings.Contains(got, "set PIG_SELF_UPDATE_COMMAND or the selfUpdateCommand setting.") {
		t.Fatalf("remediation = %q", got)
	}
}
