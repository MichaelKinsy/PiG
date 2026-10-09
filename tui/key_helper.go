package tui

// KeyHelper ports the shape of upstream's Key object (keys.ts:163): named key identifiers for special and symbol keys, and builders
// that prefix a base key with modifiers. Upstream's template-literal return types become plain KeyID strings.
type KeyHelper struct {
	// Special keys.
	Escape, Esc, Enter, Return, Tab, Space, Backspace, Delete, Insert, Clear, Home, End KeyID
	PageUp, PageDown, Up, Down, Left, Right                                             KeyID
	F1, F2, F3, F4, F5, F6, F7, F8, F9, F10, F11, F12                                   KeyID

	// Symbol keys.
	Backtick, Hyphen, Equals, Leftbracket, Rightbracket, Backslash, Semicolon, Quote, Comma, Period, Slash KeyID
	Exclamation, At, Hash, Dollar, Percent, Caret, Ampersand, Asterisk, Leftparen, Rightparen, Underscore  KeyID
	Plus, Pipe, Tilde, Leftbrace, Rightbrace, Colon, Lessthan, Greaterthan, Question                       KeyID
}

// Key is upstream's Key helper object. Key.Escape and Key.Ctrl("c") produce the identifiers MatchesKeyID and the keybinding tables take.
var Key = KeyHelper{
	Escape: "escape", Esc: "esc", Enter: "enter", Return: "return", Tab: "tab", Space: "space", Backspace: "backspace",
	Delete: "delete", Insert: "insert", Clear: "clear", Home: "home", End: "end", PageUp: "pageUp", PageDown: "pageDown",
	Up: "up", Down: "down", Left: "left", Right: "right",
	F1: "f1", F2: "f2", F3: "f3", F4: "f4", F5: "f5", F6: "f6", F7: "f7", F8: "f8", F9: "f9", F10: "f10", F11: "f11", F12: "f12",

	Backtick: "`", Hyphen: "-", Equals: "=", Leftbracket: "[", Rightbracket: "]", Backslash: "\\", Semicolon: ";", Quote: "'",
	Comma: ",", Period: ".", Slash: "/", Exclamation: "!", At: "@", Hash: "#", Dollar: "$", Percent: "%", Caret: "^",
	Ampersand: "&", Asterisk: "*", Leftparen: "(", Rightparen: ")", Underscore: "_", Plus: "+", Pipe: "|", Tilde: "~",
	Leftbrace: "{", Rightbrace: "}", Colon: ":", Lessthan: "<", Greaterthan: ">", Question: "?",
}

// Ctrl returns "ctrl+<key>".
func (KeyHelper) Ctrl(key KeyID) KeyID { return "ctrl+" + key }

// Shift returns "shift+<key>".
func (KeyHelper) Shift(key KeyID) KeyID { return "shift+" + key }

// Alt returns "alt+<key>".
func (KeyHelper) Alt(key KeyID) KeyID { return "alt+" + key }

// Super returns "super+<key>".
func (KeyHelper) Super(key KeyID) KeyID { return "super+" + key }

// CtrlShift returns "ctrl+shift+<key>".
func (KeyHelper) CtrlShift(key KeyID) KeyID { return "ctrl+shift+" + key }

// ShiftCtrl returns "shift+ctrl+<key>".
func (KeyHelper) ShiftCtrl(key KeyID) KeyID { return "shift+ctrl+" + key }

// CtrlAlt returns "ctrl+alt+<key>".
func (KeyHelper) CtrlAlt(key KeyID) KeyID { return "ctrl+alt+" + key }

// AltCtrl returns "alt+ctrl+<key>".
func (KeyHelper) AltCtrl(key KeyID) KeyID { return "alt+ctrl+" + key }

// ShiftAlt returns "shift+alt+<key>".
func (KeyHelper) ShiftAlt(key KeyID) KeyID { return "shift+alt+" + key }

// AltShift returns "alt+shift+<key>".
func (KeyHelper) AltShift(key KeyID) KeyID { return "alt+shift+" + key }

// CtrlSuper returns "ctrl+super+<key>".
func (KeyHelper) CtrlSuper(key KeyID) KeyID { return "ctrl+super+" + key }

// SuperCtrl returns "super+ctrl+<key>".
func (KeyHelper) SuperCtrl(key KeyID) KeyID { return "super+ctrl+" + key }

// ShiftSuper returns "shift+super+<key>".
func (KeyHelper) ShiftSuper(key KeyID) KeyID { return "shift+super+" + key }

// SuperShift returns "super+shift+<key>".
func (KeyHelper) SuperShift(key KeyID) KeyID { return "super+shift+" + key }

// AltSuper returns "alt+super+<key>".
func (KeyHelper) AltSuper(key KeyID) KeyID { return "alt+super+" + key }

// SuperAlt returns "super+alt+<key>".
func (KeyHelper) SuperAlt(key KeyID) KeyID { return "super+alt+" + key }

// CtrlShiftAlt returns "ctrl+shift+alt+<key>".
func (KeyHelper) CtrlShiftAlt(key KeyID) KeyID { return "ctrl+shift+alt+" + key }

// CtrlShiftSuper returns "ctrl+shift+super+<key>".
func (KeyHelper) CtrlShiftSuper(key KeyID) KeyID { return "ctrl+shift+super+" + key }
