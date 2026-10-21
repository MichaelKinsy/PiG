package tui

import "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/engine"

// KeyID is pi-tui's KeyId: a key name such as "enter", "ctrl+c" or "ctrl+shift+f5".
type KeyID = engine.KeyID

// KeyEventType is pi-tui's KeyEventType.
type KeyEventType = engine.KeyEventType

// The KeyEventType values.
const (
	KeyEventPress   = engine.KeyEventPress
	KeyEventRepeat  = engine.KeyEventRepeat
	KeyEventRelease = engine.KeyEventRelease
)

// KeyHelper is the type of [Key].
type KeyHelper = engine.KeyHelper

// Key is pi-tui's Key helper object: Key.Escape, Key.Enter and the symbol keys name key identifiers, and Key.Ctrl("c"),
// Key.CtrlShift("p") and the other modifier builders prefix one.
var Key = engine.Key

// MatchesKey is pi-tui's matchesKey: whether raw terminal data (legacy sequence, Kitty CSI-u or modifyOtherKeys) is the key keyID.
// The Kitty keyboard protocol state ([SetKittyProtocolActive]) and the Windows Terminal environment decide the ambiguous bytes.
func MatchesKey(data string, keyID KeyID) bool { return engine.MatchesKeyID(data, keyID) }

// ParseKey is pi-tui's parseKey: the key identifier raw terminal data stands for, or false when it is none.
func ParseKey(data string) (KeyID, bool) { return engine.ParseKey(data) }

// IsKeyRelease is pi-tui's isKeyRelease: whether data is a Kitty key-release event.
func IsKeyRelease(data string) bool { return engine.IsKeyRelease(data) }

// IsKeyRepeat is pi-tui's isKeyRepeat: whether data is a Kitty key-repeat event.
func IsKeyRepeat(data string) bool { return engine.IsKeyRepeat(data) }

// DecodeKittyPrintable is pi-tui's decodeKittyPrintable: the character of a plain or shifted Kitty CSI-u key event.
func DecodeKittyPrintable(data string) (string, bool) { return engine.DecodeKittyPrintable(data) }

// DecodePrintableKey is pi-tui's decodePrintableKey: DecodeKittyPrintable, or the character of a modifyOtherKeys event.
func DecodePrintableKey(data string) (string, bool) { return engine.DecodePrintableKey(data) }

// SetKittyProtocolActive is pi-tui's setKittyProtocolActive.
func SetKittyProtocolActive(active bool) { engine.SetKittyProtocolActive(active) }

// IsKittyProtocolActive is pi-tui's isKittyProtocolActive.
func IsKittyProtocolActive() bool { return engine.IsKittyProtocolActive() }
