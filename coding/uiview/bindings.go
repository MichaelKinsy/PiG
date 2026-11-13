package uiview

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// pig additive (D614): bindings. A mount's content may tie keys and gestures to its mount-level intents. The record is
// data only: the host checks it and delivers it; a client fires a binding only while the mount holds the focus, after
// the focused node of the mount has had the key, and never globally. A client that cannot honour a binding reports it
// with [Attachment.Report] (code unsupported, path bindings[i]).

// pig additive (D614): the bounds of the bindings record.
const (
	maxBindings    = 32   // pig additive (D614): bindings of a content
	maxBindingArg  = 1024 // pig additive (D614): bytes of a binding's constant argument
	maxBindingName = 256  // pig additive (D614): bytes of a binding's name
)

// Binding ties one trigger, a key or a gesture, to an intent the content declares without a nodeKey. Arg is the constant
// argument the client raises the intent with; absent, the client raises it with {}. Name is what a client shows for the
// binding (a hint, a button, the accessible name): a label (offLabel) of at most 256 bytes.
type Binding struct {
	Key     string          `json:"key,omitempty"`
	Gesture string          `json:"gesture,omitempty"`
	Intent  string          `json:"intent"`
	Arg     json.RawMessage `json:"arg,omitempty"`
	Name    string          `json:"name,omitempty"`
}

// BindingKey is a parsed key id: the base key and the modifiers held with it.
type BindingKey struct {
	Base             string
	Shift, Ctrl, Alt bool
}

// bindingNamedKeys is the closed set of base keys that have a name, spelled as tui.ParseKey spells them. escape and tab
// are named so that a binding of them is refused as reserved, not as unknown.
var bindingNamedKeys = []string{
	"enter", "space", "backspace", "delete", "insert", "home", "end", "pageUp", "pageDown", "up", "down", "left", "right",
	"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12", "escape", "tab",
}

// bindingSymbolKeys is the closed set of symbol keys, each named by the character it produces. It is Pi's set of symbol
// keys.
const bindingSymbolKeys = "`-=+[]\\;',./!@#$%^&*()_|~{}:<>?"

// bindingGestures is the closed set of gestures.
var bindingGestures = []string{"swipe-left", "swipe-right", "swipe-up", "swipe-down"}

// BindingGestures returns the gestures a binding may name.
func BindingGestures() []string { return slices.Clone(bindingGestures) }

// ReservedKey is one row of the table of keys no binding may take. Base is the key; Ctrl says the row holds only while
// ctrl is held (otherwise it holds with any modifiers); Why is what a client keeps the key for.
type ReservedKey struct {
	Base string
	Ctrl bool
	Why  string
}

// reservedKeys is the one table of reserved keys: the host refuses a binding of a row, and a client keeps the row's key
// ahead of the focused node and of every binding. The rows are the keys the actions have by default. Where a user can
// give the action of a row another key, the client asks when a key is pressed and gives no binding that key either.
var reservedKeys = []ReservedKey{
	{Base: "escape", Why: "leaves the mount"},
	{Base: "tab", Why: "moves the focus"},
	{Base: "f6", Why: "moves the focus to the next mount"},
	{Base: "c", Ctrl: true, Why: "interrupts"},
	{Base: "d", Ctrl: true, Why: "ends the input"},
	{Base: "z", Ctrl: true, Why: "suspends"},
}

// ReservedKeys returns the table of keys no binding may take.
func ReservedKeys() []ReservedKey { return slices.Clone(reservedKeys) }

// Reserved returns the row of the reserved table the key falls under, or nil. It is tested on the parsed key, so the
// order of the modifiers and a further modifier change nothing.
func (k BindingKey) Reserved() *ReservedKey {
	for i := range reservedKeys {
		if r := &reservedKeys[i]; r.Base == k.Base && (!r.Ctrl || k.Ctrl) {
			return r
		}
	}
	return nil
}

// ParseBindingKey reads a key id as tui.ParseKey and registerShortcut write it: the modifiers shift, ctrl and alt in
// any order, each at most once and each followed by "+", then one base key. The base is a lower-case letter, a digit, a
// named key or a symbol key. "+" is a symbol key, as Pi's parseKeyId reads it: "ctrl++" is ctrl held with it. The id is
// lower case but for pageUp and pageDown, and a key has one name: no alias is read.
// Shift goes with a letter or a named key only, because the character a shifted digit or symbol produces depends on the
// keyboard layout; such a key is named by the symbol it produces.
func ParseBindingKey(id string) (BindingKey, error) {
	var k BindingKey
	base := id
	for base != "+" {
		mod, rest, found := strings.Cut(base, "+")
		if !found {
			break
		}
		var held *bool
		switch mod {
		case "shift":
			held = &k.Shift
		case "ctrl":
			held = &k.Ctrl
		case "alt":
			held = &k.Alt
		default:
			return BindingKey{}, fmt.Errorf("key %q has unknown modifier %q; shift, ctrl and alt are known", id, mod)
		}
		if *held {
			return BindingKey{}, fmt.Errorf("key %q names modifier %q twice", id, mod)
		}
		*held, base = true, rest
	}
	letter := len(base) == 1 && base[0] >= 'a' && base[0] <= 'z'
	named := slices.Contains(bindingNamedKeys, base)
	single := len(base) == 1 && (base[0] >= '0' && base[0] <= '9' || strings.IndexByte(bindingSymbolKeys, base[0]) >= 0)
	if !letter && !named && !single {
		return BindingKey{}, fmt.Errorf("key %q names no key", id)
	}
	if k.Shift && single {
		return BindingKey{}, fmt.Errorf("key %q holds shift with a digit or a symbol; name the symbol it produces", id)
	}
	k.Base = base
	return k, nil
}

// checkBindings validates the bindings of a content against its intents.
func checkBindings(bindings []Binding, intents []IntentDecl) error {
	if len(bindings) > maxBindings {
		return fmt.Errorf("%d bindings, at most %d", len(bindings), maxBindings)
	}
	keys := make(map[BindingKey]struct{}, len(bindings))
	gestures := make(map[string]struct{}, len(bindings))
	for i := range bindings {
		b := &bindings[i]
		switch {
		case (b.Key == "") == (b.Gesture == ""):
			return fmt.Errorf("binding %d needs a key or a gesture, not both", i)
		case b.Key != "":
			k, err := ParseBindingKey(b.Key)
			if err != nil {
				return fmt.Errorf("binding %d: %w", i, err)
			}
			if r := k.Reserved(); r != nil {
				return fmt.Errorf("binding %d: key %q is reserved: it %s", i, b.Key, r.Why)
			}
			if _, dup := keys[k]; dup {
				return fmt.Errorf("binding %d: key %q is bound twice", i, b.Key)
			}
			keys[k] = struct{}{}
		default:
			if !slices.Contains(bindingGestures, b.Gesture) {
				return fmt.Errorf("binding %d: unknown gesture %q", i, b.Gesture)
			}
			if _, dup := gestures[b.Gesture]; dup {
				return fmt.Errorf("binding %d: gesture %q is bound twice", i, b.Gesture)
			}
			gestures[b.Gesture] = struct{}{}
		}
		if len(b.Name) > maxBindingName {
			return fmt.Errorf("binding %d: name is %d bytes, at most %d", i, len(b.Name), maxBindingName)
		}
		if offLabel(b.Name) {
			return fmt.Errorf("binding %d: name holds %s", i, labelFault)
		}
		if len(b.Arg) > maxBindingArg {
			return fmt.Errorf("binding %d: arg is %d bytes, at most %d", i, len(b.Arg), maxBindingArg)
		}
		at := slices.IndexFunc(intents, func(d IntentDecl) bool { return d.Name == b.Intent })
		if at < 0 {
			return fmt.Errorf("binding %d raises intent %q, which is not declared", i, b.Intent)
		}
		if intents[at].NodeKey != "" {
			return fmt.Errorf("binding %d raises intent %q, which belongs to node %q", i, b.Intent, intents[at].NodeKey)
		}
		if err := intents[at].Arg.Validate(b.raised()); err != nil {
			return fmt.Errorf("binding %d: arg of intent %q: %w", i, b.Intent, err)
		}
	}
	return nil
}

// raised is the argument a client raises the binding's intent with: Arg, or {} when the binding has none.
func (b *Binding) raised() json.RawMessage {
	if len(b.Arg) == 0 {
		return json.RawMessage(`{}`)
	}
	return b.Arg
}
