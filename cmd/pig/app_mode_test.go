package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestResolveAppModeTruthTable pins upstream main.ts resolveAppMode:
//
//	if (parsed.mode === "rpc") return "rpc";
//	if (parsed.mode === "json") return "json";
//	if (parsed.print || !stdinIsTTY || !stdoutIsTTY) return "print";
//	return "interactive";
//
// pig ignored stdout, so `pig "q" > file` drew the TUI into the file.
func TestResolveAppModeTruthTable(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		print       bool
		stdin       bool
		stdout      bool
		want        appMode
		wantExtMode extension.ExtensionMode
	}{
		{"rpc", true, false, false, appModeRPC, extension.ModeRPC},
		{"rpc", false, true, true, appModeRPC, extension.ModeRPC},
		{"json", false, true, true, appModeJSON, extension.ModeJSON},
		{"json", true, false, false, appModeJSON, extension.ModeJSON},
		{"", true, true, true, appModePrint, extension.ModePrint},
		{"text", true, true, true, appModePrint, extension.ModePrint},
		{"", false, false, true, appModePrint, extension.ModePrint},
		{"", false, true, false, appModePrint, extension.ModePrint},
		{"text", false, true, false, appModePrint, extension.ModePrint},
		{"", false, false, false, appModePrint, extension.ModePrint},
		{"", false, true, true, appModeInteractive, extension.ModeTUI},
		{"text", false, true, true, appModeInteractive, extension.ModeTUI},
	} {
		got := resolveAppMode(tc.mode, tc.print, tc.stdin, tc.stdout)
		if got != tc.want {
			t.Errorf("resolveAppMode(mode=%q, print=%v, stdinTTY=%v, stdoutTTY=%v) = %q, want %q",
				tc.mode, tc.print, tc.stdin, tc.stdout, got, tc.want)
		}
		if ext := got.extensionMode(); ext != tc.wantExtMode {
			t.Errorf("%q.extensionMode() = %q, want %q", got, ext, tc.wantExtMode)
		}
	}
}
