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

// NormalizeNativeShiftEnterInput mirrors terminal.ts
// normalizeNativeShiftEnterInput: a plain Return read while the native Shift
// key is held becomes the enhanced Shift+Enter sequence.
func NormalizeNativeShiftEnterInput(data string, shouldDetectNativeShiftEnter, isShiftPressed bool) string {
	if shouldDetectNativeShiftEnter && data == "\r" && isShiftPressed {
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
	shouldDetectNativeShiftEnter := sequence == "\r" && (isAppleTerminalSessionFor(goos, termProgram) || goos == "windows")
	return NormalizeNativeShiftEnterInput(
		sequence,
		shouldDetectNativeShiftEnter,
		shouldDetectNativeShiftEnter && isModifierPressed(ModifierShift),
	)
}
