package ghostty

import "strings"

// keyCodes lists the physical keys in GhosttyKey order (include/ghostty/vt/key/event.h): the enum value is the index
// plus one. Named by W3C KeyboardEvent.code.
var keyCodes = func() map[string]int {
	names := strings.Fields(`Backquote Backslash BracketLeft BracketRight Comma Digit0 Digit1 Digit2 Digit3 Digit4 Digit5 Digit6 Digit7
Digit8 Digit9 Equal IntlBackslash IntlRo IntlYen KeyA KeyB KeyC KeyD KeyE KeyF KeyG KeyH KeyI KeyJ KeyK KeyL
KeyM KeyN KeyO KeyP KeyQ KeyR KeyS KeyT KeyU KeyV KeyW KeyX KeyY KeyZ Minus Period Quote Semicolon Slash
AltLeft AltRight Backspace CapsLock ContextMenu ControlLeft ControlRight Enter MetaLeft MetaRight ShiftLeft
ShiftRight Space Tab Convert KanaMode NonConvert Delete End Help Home Insert PageDown PageUp ArrowDown
ArrowLeft ArrowRight ArrowUp NumLock Numpad0 Numpad1 Numpad2 Numpad3 Numpad4 Numpad5 Numpad6 Numpad7 Numpad8
Numpad9 NumpadAdd NumpadBackspace NumpadClear NumpadClearEntry NumpadComma NumpadDecimal NumpadDivide
NumpadEnter NumpadEqual NumpadMemoryAdd NumpadMemoryClear NumpadMemoryRecall NumpadMemoryStore
NumpadMemorySubtract NumpadMultiply NumpadParenLeft NumpadParenRight NumpadSubtract NumpadSeparator NumpadUp
NumpadDown NumpadRight NumpadLeft NumpadBegin NumpadHome NumpadEnd NumpadInsert NumpadDelete NumpadPageUp
NumpadPageDown Escape F1 F2 F3 F4 F5 F6 F7 F8 F9 F10 F11 F12 F13 F14 F15 F16 F17 F18 F19 F20 F21 F22 F23 F24
F25 Fn FnLock PrintScreen ScrollLock Pause`)
	m := make(map[string]int, len(names))
	for i, name := range names {
		m[name] = i + 1
	}
	return m
}()
