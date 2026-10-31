package tui

import (
	"os"
	"runtime"
)

// nativeShiftEnterSequence mirrors terminal.ts NATIVE_SHIFT_ENTER_SEQUENCE.
const nativeShiftEnterSequence = "\x1b[13;2u"

// IsAppleTerminalSession mirrors terminal.ts isAppleTerminalSession.
func IsAppleTerminalSession() bool {
	return isAppleTerminalSessionFor(runtime.GOOS, os.Getenv("TERM_PROGRAM"))
}

func isAppleTerminalSessionFor(goos, termProgram string) bool {
	return goos == "darwin" && termProgram == "Apple_Terminal"
}

// isNativeShiftEnterCandidate mirrors terminal.ts isNativeShiftEnterCandidate: Return, or ESC+Return, which some Apple Terminal setups
// send for Shift+Enter.
// pig divergence (D217): Pi 86dc686c (#11559) ESC+Return is a candidate as well as a lone Return.
func isNativeShiftEnterCandidate(data string) bool {
	return data == "\r" || data == "\x1b\r"
}

// NormalizeNativeShiftEnterInput mirrors terminal.ts
// normalizeNativeShiftEnterInput: a Return or ESC+Return read while the native
// Shift key is held becomes the enhanced Shift+Enter sequence.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter, isShiftPressed bool) string {
	if shouldDetectNativeShiftEnter && isNativeShiftEnterCandidate(data) && isShiftPressed {
		return nativeShiftEnterSequence
	}
	return data
}

// NormalizeAppleTerminalInput mirrors terminal.ts normalizeAppleTerminalInput.
func NormalizeAppleTerminalInput(data string, isAppleTerminal, isShiftPressed bool) string {
	return NormalizeNativeShiftEnterInput(data, isAppleTerminal, isShiftPressed)
}

// NormalizeProcessInputSequence applies the input normalization of upstream
// ProcessTerminal.forwardInputSequence to one complete StdinBuffer sequence.
// Apple Terminal (and the Windows console) can send plain Return for
// Shift+Enter even with enhanced key reporting requested, so Pi asks the local
// OS whether Shift is held. Pig's input loops split sequences outside
// ProcessTerminal, so each loop calls this before dispatch.
func NormalizeProcessInputSequence(sequence string) string {
	return normalizeProcessInputSequenceFor(sequence, runtime.GOOS, os.Getenv("TERM_PROGRAM"), IsNativeModifierPressed)
}

func normalizeProcessInputSequenceFor(sequence, goos, termProgram string, isModifierPressed func(ModifierKey) bool) string {
	shouldDetectNativeShiftEnter := isNativeShiftEnterCandidate(sequence) && (isAppleTerminalSessionFor(goos, termProgram) || goos == "windows")
	return NormalizeNativeShiftEnterInput(
		sequence,
		shouldDetectNativeShiftEnter,
		shouldDetectNativeShiftEnter && isModifierPressed(ModifierShift),
	)
}
