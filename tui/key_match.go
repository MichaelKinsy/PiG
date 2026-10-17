package tui

// key_match.go: Dynamic key matching, ported from upstream Pi's keys.ts.
//
// matchesKeyDynamic resolves any KeyID string (e.g. "ctrl+shift+left")
// against raw terminal input by parsing both sides and comparing
// codepoints + modifiers. This replaces the static tuiKeyIDInputs map
// for extension shortcuts and handles any modifier+key combination.
//
// Supports three terminal key protocols:
//   - Kitty CSI-u:         \x1b[<codepoint>[:<shifted>[:<base>]];<modifier>u
//   - xterm modifyOtherKeys: \x1b[27;<modifier>;<codepoint>~
//   - Legacy CSI sequences: \x1b[1;<modifier><final> for arrows, \x1b[<n>;<modifier>~ for functional keys
//
// Upstream reference: packages/tui/src/keys.ts matchesKey() (1400 LOC).

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Modifier bit constants: matches Kitty keyboard protocol.
// Modifier values in CSI sequences are 1-indexed (wire = 1 + bitmask).
const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8

	// Lock bits to mask out when comparing modifiers.
	lockMask = 64 + 128 // Caps Lock + Num Lock
)

// Sentinel codepoints for non-printable keys (negative = not real Unicode).
const (
	cpArrowUp    = -1
	cpArrowDown  = -2
	cpArrowRight = -3
	cpArrowLeft  = -4

	cpDelete   = -10
	cpInsert   = -11
	cpPageUp   = -12
	cpPageDown = -13
	cpHome     = -14
	cpEnd      = -15

	cpEscape    = 27
	cpTab       = 9
	cpEnter     = 13
	cpSpace     = 32
	cpBackspace = 127
)

// arrowFinals maps arrow final bytes to sentinel codepoints.
var arrowFinals = map[byte]int{
	'A': cpArrowUp,
	'B': cpArrowDown,
	'C': cpArrowRight,
	'D': cpArrowLeft,
}

// homeEndFinals maps H/F final bytes to sentinel codepoints.
var homeEndFinals = map[byte]int{
	'H': cpHome,
	'F': cpEnd,
}

// funcKeyCodes maps the CSI parameter to sentinel codepoints.
var funcKeyCodes = map[int]int{
	2: cpInsert,
	3: cpDelete,
	5: cpPageUp,
	6: cpPageDown,
	7: cpHome,
	8: cpEnd,
}

// keyNameCodepoints maps KeyID base key names to (codepoint, isArrow) pairs.
// "isArrow" means the key uses \x1b[1;<mod><final> format rather than CSI-u.
var keyNameCodepoints = map[string]struct {
	cp      int
	isArrow bool
}{
	// Arrows
	"up":    {cpArrowUp, true},
	"down":  {cpArrowDown, true},
	"left":  {cpArrowLeft, true},
	"right": {cpArrowRight, true},

	// Functional keys (CSI <n>;<mod>~ format)
	"delete":   {cpDelete, false},
	"insert":   {cpInsert, false},
	"pageup":   {cpPageUp, false},
	"pagedown": {cpPageDown, false},
	"home":     {cpHome, false},
	"end":      {cpEnd, false},

	// Special named keys
	"escape":    {cpEscape, false},
	"esc":       {cpEscape, false},
	"tab":       {cpTab, false},
	"enter":     {cpEnter, false},
	"return":    {cpEnter, false},
	"space":     {cpSpace, false},
	"backspace": {cpBackspace, false},
}

// arrowCPtoFinal maps arrow sentinel codepoints to CSI final bytes.
var arrowCPtoFinal = map[int]byte{
	cpArrowUp:    'A',
	cpArrowDown:  'B',
	cpArrowRight: 'C',
	cpArrowLeft:  'D',
}

// funcCPtoNum maps functional key sentinel codepoints to CSI parameter numbers.
var funcCPtoNum = map[int]int{
	cpDelete:   3,
	cpInsert:   2,
	cpPageUp:   5,
	cpPageDown: 6,
	cpHome:     7,
	cpEnd:      8,
}

// homeCPtoFinal maps home/end sentinel codepoints to final bytes.
var homeCPtoFinal = map[int]byte{
	cpHome: 'H',
	cpEnd:  'F',
}

// cpKPEnter is the Kitty keypad Enter codepoint (keys.ts CODEPOINTS.kpEnter).
const cpKPEnter = 57414

// parsedKey is the result of parsing a CSI sequence from terminal input.
type parsedKey struct {
	codepoint int
	modifier  int // bitmask (0-indexed): shift=1, alt=2, ctrl=4, super=8
	// baseLayoutKey is the Kitty flag-4 alternate that names the key's
	// position on a standard PC-101 layout (keys.ts ParsedKittySequence).
	baseLayoutKey    int
	hasBaseLayoutKey bool
	// shiftedKey is the Kitty flag-4 alternate that names the key with Shift held (keys.ts ParsedKittySequence.shiftedKey).
	shiftedKey    int
	hasShiftedKey bool
	// inexactModifier marks a modifyOtherKeys modifier too large for its int32 view to equal the number keys.ts compares.
	inexactModifier bool
}

// parseCSIu parses a Kitty CSI-u sequence, mirroring the CSI-u branch of
// keys.ts parseKittySequence:
//
//	\x1b[<codepoint>[:<shifted>[:<base>]][;<modifier>[:<event>]]u
//
// Flag 4 appends the shifted key and the base-layout key after the codepoint
// (\x1b[1089::99;5u is Ctrl+С on a Cyrillic layout, base key c). The base-layout
// key and the shifted key are retained for matchesKittySequence and ParseKey; printable decoding reads the shifted key itself
// (DecodeKittyPrintable). Returns nil if data is not CSI-u.
func parseCSIu(data string) *parsedKey {
	// The pattern is anchored: ESC [ ... u. Most input is not that, and the regular expression allocates for each match attempt.
	if len(data) < 4 || data[0] != '\x1b' || data[1] != '[' || data[len(data)-1] != 'u' {
		return nil
	}
	match := kittyCSIURegex.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	modValue := 1.0
	if match[4] != "" {
		modValue = keyNumber(match[4])
	}
	pk := &parsedKey{codepoint: keyCodepoint(keyNumber(match[1])), modifier: jsInt32(modValue-1) &^ lockMask}
	if match[2] != "" {
		pk.shiftedKey, pk.hasShiftedKey = keyCodepoint(keyNumber(match[2])), true
	}
	if match[3] != "" {
		pk.baseLayoutKey, pk.hasBaseLayoutKey = keyCodepoint(keyNumber(match[3])), true
	}
	return pk
}

// keyNumber is parseInt(digits, 10) for a run of ASCII digits that a keys.ts sequence pattern captured. The value is a float64, as in JavaScript, so a run too long for an int still parses.
func keyNumber(digits string) float64 {
	value, err := strconv.ParseFloat(digits, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return value
}

// jsInt32 is JavaScript's ToInt32, the conversion keys.ts's bitwise operators apply to a parsed modifier.
func jsInt32(value float64) int {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	wrapped := math.Mod(math.Trunc(value), 1<<32)
	if wrapped < 0 {
		wrapped += 1 << 32
	}
	return int(int32(uint32(wrapped)))
}

// keyCodepoint holds a parsed codepoint as an int that compares and truncates (String.fromCharCode) as the JavaScript number does. Every number
// from 2^62 up is a multiple of 1024, so its low 16 bits name no key, and one sentinel above every key codepoint stands for all of them.
func keyCodepoint(value float64) int {
	if value < 1<<62 {
		return int(value)
	}
	return 1 << 62
}

// normalizeShiftedLetterIdentityCodepoint mirrors keys.ts: with Shift held an
// uppercase A-Z codepoint names the same key as its lowercase letter.
func normalizeShiftedLetterIdentityCodepoint(codepoint, modifier int) int {
	if (modifier&^lockMask)&modShift != 0 && codepoint >= 'A' && codepoint <= 'Z' {
		return codepoint + 32
	}
	return codepoint
}

// isSymbolCodepoint mirrors keys.ts SYMBOL_KEYS.has(String.fromCharCode(cp)),
// including fromCharCode's truncation of the codepoint to 16 bits.
func isSymbolCodepoint(cp int) bool {
	unit := cp & 0xFFFF
	return unit < 128 && isSymbolKey(byte(unit))
}

// shiftedSymbolKeys is keys.ts SHIFTED_SYMBOL_KEYS: the symbols a terminal reports as their produced character plus Shift.
const shiftedSymbolKeys = "~!@#$%^&*()_+|{}:<>?"

// isShiftedSymbolCodepoint mirrors SHIFTED_SYMBOL_KEYS.has(String.fromCharCode(cp)), including fromCharCode's truncation of the codepoint to 16 bits.
func isShiftedSymbolCodepoint(cp int) bool {
	unit := cp & 0xFFFF
	return unit < 128 && strings.IndexByte(shiftedSymbolKeys, byte(unit)) >= 0
}

// logicalShiftedSymbol is keys.ts getLogicalShiftedSymbolCandidate: a symbol reported as Shift plus its physical key names the produced
// symbol without that implicit Shift. allowed says which codepoints name a symbol the caller accepts; ok is false when the key is not one.
func logicalShiftedSymbol(codepoint int, present bool, modifier int, allowed func(int) bool) (cp, mod int, ok bool) {
	if !present || modifier&modShift == 0 {
		return 0, 0, false
	}
	normalized := normalizeKittyFunctionalCodepoint(codepoint)
	if !allowed(normalized) {
		return 0, 0, false
	}
	return normalized, modifier &^ modShift, true
}

// matchesKittySequence mirrors keys.ts matchesKittySequence for a parsed
// CSI-u key. The physical match needs every modifier equal and the codepoint
// equal after keypad and shifted-letter normalization. The logical match
// takes a symbol reported as Shift plus its physical key ("+" is Shift and
// "=", alternate 43) as the produced symbol: the implicit Shift is consumed
// and Ctrl, Alt and Super are kept. Failing both, with the modifiers equal, the
// base-layout key matches so Ctrl+С on a Cyrillic layout triggers ctrl+c, but
// only when the codepoint is not itself a Latin letter or known symbol: on
// remapped layouts (Dvorak, Colemak) the codepoint is authoritative, so Dvorak
// Ctrl+K (base v) never matches ctrl+v.
func matchesKittySequence(pk *parsedKey, expectedCodepoint, expectedModifier int) bool {
	actualMod, expectedMod := pk.modifier&^lockMask, expectedModifier&^lockMask
	normalized := normalizeShiftedLetterIdentityCodepoint(normalizeKittyFunctionalCodepoint(pk.codepoint), pk.modifier)
	if actualMod == expectedMod && normalized == normalizeShiftedLetterIdentityCodepoint(normalizeKittyFunctionalCodepoint(expectedCodepoint), expectedModifier) {
		return true
	}
	if cp, mod, ok := logicalShiftedSymbol(pk.shiftedKey, pk.hasShiftedKey, actualMod, isSymbolCodepoint); ok &&
		mod&^lockMask == expectedMod && cp == normalizeKittyFunctionalCodepoint(expectedCodepoint) {
		return true
	}
	if actualMod != expectedMod {
		return false
	}
	if pk.hasBaseLayoutKey && pk.baseLayoutKey == expectedCodepoint {
		isLatinLetter := normalized >= 'a' && normalized <= 'z'
		return !isLatinLetter && !isSymbolCodepoint(normalized)
	}
	return false
}

// matchesPrintableModifyOtherKeys mirrors keys.ts matchesPrintableModifyOtherKeys: xterm modifyOtherKeys input for a letter, digit
// or symbol key. The physical match needs the modifier and the codepoint (after shifted-letter normalization) equal; the logical
// match takes "\x1b[27;2;43~" (Shift and "+") as the symbol "+" without the implicit Shift.
func matchesPrintableModifyOtherKeys(pk *parsedKey, expectedKeycode, expectedModifier int) bool {
	if pk == nil {
		return false
	}
	if pk.modifier == expectedModifier && !pk.inexactModifier && normalizeShiftedLetterIdentityCodepoint(pk.codepoint, pk.modifier) == normalizeShiftedLetterIdentityCodepoint(expectedKeycode, expectedModifier) {
		return true
	}
	cp, mod, ok := logicalShiftedSymbol(pk.codepoint, true, pk.modifier, isShiftedSymbolCodepoint)
	return ok && mod == expectedModifier && cp == expectedKeycode
}

// parseModifyOtherKeys parses xterm modifyOtherKeys, keys.ts's /^\x1b\[27;(\d+);(\d+)~$/. The modifier keeps its lock bits: keys.ts matchesModifyOtherKeys compares it exactly, as a number, so inexactModifier marks a modifier whose int32 view differs from that number.
func parseModifyOtherKeys(data string) *parsedKey {
	rest, ok := strings.CutPrefix(data, "\x1b[27;")
	if !ok {
		return nil
	}
	modText, rest := cutDigits(rest)
	if modText == "" || !strings.HasPrefix(rest, ";") {
		return nil
	}
	cpText, rest := cutDigits(rest[1:])
	if cpText == "" || rest != "~" {
		return nil
	}
	modifier := keyNumber(modText) - 1
	pk := &parsedKey{codepoint: keyCodepoint(keyNumber(cpText)), modifier: jsInt32(modifier)}
	pk.inexactModifier = float64(pk.modifier) != modifier
	return pk
}

// cutDigits splits s after its leading run of ASCII digits, the run a \d+ in a keys.ts pattern matches.
func cutDigits(s string) (digits, rest string) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[:end], s[end:]
}

// cutEventType removes the optional ":<digits>" event type that keys.ts's patterns accept after a modifier. It reports false for a ':' with no
// digits after it.
func cutEventType(s string) (string, bool) {
	if !strings.HasPrefix(s, ":") {
		return s, true
	}
	event, rest := cutDigits(s[1:])
	return rest, event != ""
}

// parseModifiedArrow parses a modified arrow, Home or End key, keys.ts's /^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$/ and its [HF] twin.
func parseModifiedArrow(data string) *parsedKey {
	rest, ok := strings.CutPrefix(data, "\x1b[1;")
	if !ok {
		return nil
	}
	modText, rest := cutDigits(rest)
	if modText == "" {
		return nil
	}
	if rest, ok = cutEventType(rest); !ok || len(rest) != 1 {
		return nil
	}
	cp, ok := arrowFinals[rest[0]]
	if !ok {
		if cp, ok = homeEndFinals[rest[0]]; !ok {
			return nil
		}
	}
	return &parsedKey{codepoint: cp, modifier: jsInt32(keyNumber(modText)-1) &^ lockMask}
}

// parseModifiedFunc parses a functional key with an optional modifier and event type, keys.ts's /^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$/.
func parseModifiedFunc(data string) *parsedKey {
	rest, ok := strings.CutPrefix(data, "\x1b[")
	if !ok {
		return nil
	}
	numText, rest := cutDigits(rest)
	if numText == "" {
		return nil
	}
	modValue := 1.0
	if strings.HasPrefix(rest, ";") {
		var modText string
		if modText, rest = cutDigits(rest[1:]); modText == "" {
			return nil
		}
		modValue = keyNumber(modText)
	}
	if rest, ok = cutEventType(rest); !ok || rest != "~" {
		return nil
	}
	num := keyNumber(numText)
	if num > 8 {
		return nil
	}
	cp, ok := funcKeyCodes[int(num)]
	if !ok {
		return nil
	}
	return &parsedKey{codepoint: cp, modifier: jsInt32(modValue-1) &^ lockMask}
}

// parseTerminalInput attempts to parse raw terminal data into a (codepoint, modifier) pair
// by trying the non-Kitty CSI formats (Kitty CSI-u goes through
// matchesKittySequence).
func parseTerminalInput(data string) *parsedKey {
	if pk := parseModifyOtherKeys(data); pk != nil {
		if pk.modifier&modShift != 0 && pk.codepoint >= 65 && pk.codepoint <= 90 {
			pk.codepoint += 32
		}
		return pk
	}
	if pk := parseModifiedArrow(data); pk != nil {
		return pk
	}
	if pk := parseModifiedFunc(data); pk != nil {
		return pk
	}
	return nil
}

// parseKeyID parses a key identifier string like "ctrl+shift+left" into
// (baseKey, modifier bitmask) as keys.ts parseKeyId does: the identifier is lowercased as JavaScript's toLowerCase does, the leading
// "shift+", "alt+", "ctrl+" and "super+" parts are its modifiers, and the rest is the base key, which may be "+" but holds no other "+".
// A repeated modifier, an unrecognized part such as "meta+" and an empty base key parse to nothing.
func parseKeyID(keyID string) (baseKey string, modifier int, ok bool) {
	var lower string
	if isASCII(keyID) {
		lower = strings.ToLower(keyID)
	} else {
		lower = cases.Lower(language.Und).String(keyID)
	}
	modifierNames := [...]struct {
		prefix string
		bit    int
	}{{"shift+", modShift}, {"alt+", modAlt}, {"ctrl+", modCtrl}, {"super+", modSuper}}
	baseKey = lower
	for {
		found := false
		for _, name := range modifierNames {
			if rest, has := strings.CutPrefix(baseKey, name.prefix); has {
				if modifier&name.bit != 0 {
					return "", 0, false
				}
				modifier |= name.bit
				baseKey, found = rest, true
				break
			}
		}
		if !found {
			break
		}
	}
	if baseKey == "" || strings.Contains(baseKey, "+") && baseKey != "+" {
		return "", 0, false
	}
	return baseKey, modifier, true
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// resolveKeyCodepoint converts a base key name to its codepoint.
// Returns the codepoint and true if resolved, 0 and false otherwise.
func resolveKeyCodepoint(baseKey string) (cp int, ok bool) {
	// Check named keys first.
	if entry, found := keyNameCodepoints[baseKey]; found {
		return entry.cp, true
	}
	// Single printable character → its codepoint.
	if len(baseKey) == 1 {
		ch := baseKey[0]
		if ch >= 'a' && ch <= 'z' {
			return int(ch), true
		}
		if ch >= '0' && ch <= '9' {
			return int(ch), true
		}
		// Symbol keys
		if isSymbolKey(ch) {
			return int(ch), true
		}
	}
	// Function keys f1-f12: not CSI-u, legacy-only. Not handled dynamically.
	return 0, false
}

// isPrintableKey reports a base key that keys.ts matchesKey handles as a single letter, digit or symbol.
func isPrintableKey(baseKey string) bool {
	if len(baseKey) != 1 {
		return false
	}
	ch := baseKey[0]
	return ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || isSymbolKey(ch)
}

func isSymbolKey(ch byte) bool {
	switch ch {
	case '`', '-', '=', '[', ']', '\\', ';', '\'', ',', '.', '/',
		'!', '@', '#', '$', '%', '^', '&', '*', '(', ')', '_', '+',
		'|', '~', '{', '}', ':', '<', '>', '?':
		return true
	}
	return false
}

// rawCtrlChar returns the control character for a key (code & 0x1f).
// Returns 0 if no control character mapping exists.
func rawCtrlChar(key byte) byte {
	if key >= 'a' && key <= 'z' {
		return key & 0x1f
	}
	switch key {
	case '[', '\\', ']', '_':
		return key & 0x1f
	case '-':
		return 31 // Same as Ctrl+_
	}
	return 0
}

// matchesKeyDynamic checks if raw terminal input matches a KeyID
// by dynamically computing expected codepoints and modifier bits.
//
// This is the Go port of upstream Pi's matchesKey(data, keyId).
// It handles all three protocols: CSI-u, modifyOtherKeys, legacy CSI.
func matchesKeyDynamic(data, keyID string) bool {
	info := lookupKeyID(keyID)
	if !info.parsed || !info.dynamic {
		return false
	}
	return matchesKeyInfo(data, info)
}

// keyIDInfo is a key identifier parsed once: parseKeyID's result and, for a dynamically representable key, its expected codepoint.
type keyIDInfo struct {
	baseKey  string
	modifier int
	parsed   bool
	// dynamic reports that the base key has a codepoint, so the mode-aware matcher answers for it; codepoint is that codepoint.
	dynamic   bool
	codepoint int
	// legacyKey is the key of the legacy sequence table for a key that is not dynamic, and legacyOK reports that the modifiers allow a match at all.
	legacyKey string
	legacyOK  bool
}

// Key identifiers come from the keybinding tables and from extensions, a small and stable set, so each distinct identifier is parsed once. The cache is emptied when it reaches keyIDCacheLimit entries.
var (
	keyIDCache      sync.Map // string -> *keyIDInfo
	keyIDCacheCount atomic.Int32
)

// keyIDCacheLimit bounds the identifiers the cache holds: the default and user bindings of a session number in the low hundreds, so 4096 leaves room for extension-registered identifiers; reaching it empties the cache and the identifiers in use are parsed again once.
// pig additive (D112): Pi parses an identifier on every call (keys.ts matchesKey); the bound keeps a stream of distinct identifiers from growing memory.
const keyIDCacheLimit = 4096

func lookupKeyID(keyID string) *keyIDInfo {
	if cached, ok := keyIDCache.Load(keyID); ok {
		return cached.(*keyIDInfo)
	}
	info := &keyIDInfo{}
	info.baseKey, info.modifier, info.parsed = parseKeyID(keyID)
	if info.parsed {
		if info.codepoint, info.dynamic = resolveKeyCodepoint(info.baseKey); !info.dynamic {
			// keys.ts matchesKey: a function key matches only without a modifier, and clear also with exactly shift or ctrl.
			info.legacyKey, info.legacyOK = info.baseKey, true
			switch info.modifier {
			case 0:
			case modShift:
				info.legacyKey = "shift+" + info.baseKey
			case modCtrl:
				info.legacyKey = "ctrl+" + info.baseKey
			default:
				info.legacyOK = false
			}
		}
	}
	if keyIDCacheCount.Load() >= keyIDCacheLimit {
		// A flood of distinct identifiers empties the cache rather than keeping it full of them: the identifiers in use are parsed again once.
		keyIDCache.Clear()
		keyIDCacheCount.Store(0)
	}
	if _, loaded := keyIDCache.LoadOrStore(keyID, info); !loaded {
		keyIDCacheCount.Add(1)
	}
	return info
}

// keystrokeParse is the parse of one terminal input in each CSI form a key binding is compared with. A keystroke is compared with every binding in turn, so the last input is parsed once and the result shared; it is never modified after it is stored.
type keystrokeParse struct {
	data            string
	csiu            *parsedKey
	modifyOtherKeys *parsedKey
	terminal        *parsedKey
}

var lastKeystroke atomic.Pointer[keystrokeParse]

// singleByteKeystrokes holds the parse of every one-byte input (printable ASCII, Enter, Tab, Backspace and the C0 controls), so typing does not
// parse each key again when another key came in between.
var singleByteKeystrokes = sync.OnceValue(func() *[256]keystrokeParse {
	var table [256]keystrokeParse
	for b := range table {
		table[b] = newKeystrokeParse(string([]byte{byte(b)}))
	}
	return &table
})

func parseKeystroke(data string) *keystrokeParse {
	if len(data) == 1 {
		return &singleByteKeystrokes()[data[0]]
	}
	if last := lastKeystroke.Load(); last != nil && last.data == data {
		return last
	}
	parsed := new(newKeystrokeParse(strings.Clone(data)))
	lastKeystroke.Store(parsed)
	return parsed
}

func newKeystrokeParse(data string) keystrokeParse {
	return keystrokeParse{data: data, csiu: parseCSIu(data), modifyOtherKeys: parseModifyOtherKeys(data), terminal: parseTerminalInput(data)}
}

// matchesKeyInfo matches raw terminal input against a parsed key identifier whose base key has a codepoint.
func matchesKeyInfo(data string, info *keyIDInfo) bool {
	baseKey, expectedMod, expectedCP := info.baseKey, info.modifier, info.codepoint
	// keys.ts matchesKey: "escape" with a modifier matches nothing, although parseKey names such input.
	if expectedCP == cpEscape && expectedMod != 0 {
		return false
	}

	keystroke := parseKeystroke(data)
	// Kitty CSI-u, including flag-4 alternate keys. Enter also answers to
	// the keypad Enter codepoint (keys.ts matchesKey "enter").
	if pk := keystroke.csiu; pk != nil {
		if matchesKittySequence(pk, expectedCP, expectedMod) {
			return true
		}
		return expectedCP == cpEnter && matchesKittySequence(pk, cpKPEnter, expectedMod)
	}

	// keys.ts matchesKey: a letter, digit or symbol key matches xterm modifyOtherKeys input by matchesPrintableModifyOtherKeys, with or
	// without a modifier.
	if keystroke.modifyOtherKeys != nil && isPrintableKey(baseKey) {
		return matchesPrintableModifyOtherKeys(keystroke.modifyOtherKeys, expectedCP, expectedMod)
	}
	// keys.ts matchesKey: without a modifier only escape, space and backspace accept modifyOtherKeys; enter and tab match their raw
	// input or a Kitty sequence.
	if expectedMod == 0 && expectedCP != cpEscape && expectedCP != cpSpace && expectedCP != cpBackspace && keystroke.modifyOtherKeys != nil {
		return false
	}

	// Parse the raw terminal input.
	pk := keystroke.terminal
	if pk != nil {
		return pk.codepoint == expectedCP && pk.modifier == expectedMod && !pk.inexactModifier
	}

	// Fallback: legacy single-byte / short-sequence matching.
	return matchesLegacy(data, baseKey, expectedCP, expectedMod)
}

// matchesLegacy handles pre-CSI-u terminal sequences.
func matchesLegacy(data, baseKey string, expectedCP, expectedMod int) bool {
	// Special named keys with legacy sequences.
	switch baseKey {
	case "escape", "esc":
		return expectedMod == 0 && data == "\x1b"

	case "enter", "return":
		if expectedMod == 0 {
			return data == "\r" || !IsKittyProtocolActive() && data == "\n" || data == "\x1bOM"
		}
		if expectedMod == modShift {
			return IsKittyProtocolActive() && (data == "\x1b\r" || data == "\n")
		}
		if expectedMod == modAlt {
			return !IsKittyProtocolActive() && data == "\x1b\r"
		}
		return false

	case "tab":
		if expectedMod == 0 {
			return data == "\t"
		}
		if expectedMod == modShift {
			return data == "\x1b[Z"
		}
		return false

	case "space":
		if expectedMod == 0 {
			return data == " "
		}
		if expectedMod == modCtrl {
			return !IsKittyProtocolActive() && data == "\x00"
		}
		if expectedMod == modAlt {
			return !IsKittyProtocolActive() && data == "\x1b "
		}
		return false

	case "backspace":
		if expectedMod == 0 || expectedMod == modCtrl {
			return matchesRawBackspace(data, expectedMod)
		}
		if expectedMod == modAlt {
			return data == "\x1b\x7f" || data == "\x1b\x08"
		}
		return false

	case "delete":
		if expectedMod == 0 {
			return data == "\x1b[3~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "insert":
		if expectedMod == 0 {
			return data == "\x1b[2~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "home":
		if expectedMod == 0 {
			return data == "\x1b[H" || data == "\x1bOH" || data == "\x1b[1~" || data == "\x1b[7~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "end":
		if expectedMod == 0 {
			return data == "\x1b[F" || data == "\x1bOF" || data == "\x1b[4~" || data == "\x1b[8~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "pageup":
		if expectedMod == 0 {
			return data == "\x1b[5~" || data == "\x1b[[5~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "pagedown":
		if expectedMod == 0 {
			return data == "\x1b[6~" || data == "\x1b[[6~"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "up":
		if expectedMod == 0 {
			return data == "\x1b[A" || data == "\x1bOA"
		}
		if expectedMod == modAlt {
			return data == "\x1bp" || data == "\x1b[1;3A"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "down":
		if expectedMod == 0 {
			return data == "\x1b[B" || data == "\x1bOB"
		}
		if expectedMod == modAlt {
			return data == "\x1bn" || data == "\x1b[1;3B"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "left":
		if expectedMod == 0 {
			return data == "\x1b[D" || data == "\x1bOD"
		}
		if expectedMod == modAlt {
			return data == "\x1bb" || !IsKittyProtocolActive() && data == "\x1bB" || data == "\x1b[1;3D"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)

	case "right":
		if expectedMod == 0 {
			return data == "\x1b[C" || data == "\x1bOC"
		}
		if expectedMod == modAlt {
			return data == "\x1bf" || !IsKittyProtocolActive() && data == "\x1bF" || data == "\x1b[1;3C"
		}
		return matchesLegacyModSeq(data, baseKey, expectedMod)
	}

	// Single character keys (letters, digits, symbols).
	if len(baseKey) == 1 {
		ch := baseKey[0]

		if expectedMod == 0 {
			return len(data) == 1 && data[0] == ch
		}

		// Ctrl+letter → control character
		if expectedMod == modCtrl {
			if rc := rawCtrlChar(ch); rc != 0 {
				return len(data) == 1 && data[0] == rc
			}
		}

		// Alt+letter → ESC followed by letter in legacy mode only.
		if expectedMod == modAlt && !IsKittyProtocolActive() && len(data) == 2 && data[0] == '\x1b' {
			return data[1] == ch
		}

		// Ctrl+Alt+letter → ESC followed by a control character in legacy mode only.
		if expectedMod == modCtrl|modAlt && !IsKittyProtocolActive() {
			if rc := rawCtrlChar(ch); rc != 0 {
				return len(data) == 2 && data[0] == '\x1b' && data[1] == rc
			}
		}

		// Shift+letter → uppercase
		if expectedMod == modShift && ch >= 'a' && ch <= 'z' {
			return len(data) == 1 && data[0] == ch-32
		}
	}

	return false
}

// appendModifiedFinal appends \x1b[1;<mod+1><final>, as fmt.Sprintf("\x1b[1;%d%c", mod+1, final) writes it.
func appendModifiedFinal(buffer []byte, mod int, final byte) []byte {
	buffer = append(buffer, "\x1b[1;"...)
	buffer = strconv.AppendInt(buffer, int64(mod+1), 10)
	return utf8.AppendRune(buffer, rune(final))
}

// matchesLegacyModSeq checks legacy modified sequences for navigation keys.
// Handles both xterm-style (\x1b[1;<mod>D) and rxvt-style (\x1b[d, \x1bOd).
func matchesLegacyModSeq(data, baseKey string, mod int) bool {
	// xterm-style modified arrows: \x1b[1;<mod+1><final>
	if entry, ok := keyNameCodepoints[baseKey]; ok {
		// The expected sequence is built in a stack buffer: every key of every binding is compared on every keystroke.
		var buffer [24]byte
		if entry.isArrow {
			if final, ok := arrowCPtoFinal[entry.cp]; ok && data == string(appendModifiedFinal(buffer[:0], mod, final)) {
				return true
			}
		} else if final, ok := homeCPtoFinal[entry.cp]; ok {
			// home/end: \x1b[1;<mod>H/F
			if data == string(appendModifiedFinal(buffer[:0], mod, final)) {
				return true
			}
		}
		if num, ok := funcCPtoNum[entry.cp]; ok {
			// Functional: \x1b[<num>;<mod>~
			expected := append(buffer[:0], "\x1b["...)
			expected = strconv.AppendInt(expected, int64(num), 10)
			expected = append(expected, ';')
			expected = strconv.AppendInt(expected, int64(mod+1), 10)
			expected = append(expected, '~')
			if data == string(expected) {
				return true
			}
		}
	}

	// rxvt shift sequences
	if mod == modShift {
		switch baseKey {
		case "up":
			return data == "\x1b[a"
		case "down":
			return data == "\x1b[b"
		case "right":
			return data == "\x1b[c"
		case "left":
			return data == "\x1b[d"
		case "insert":
			return data == "\x1b[2$"
		case "delete":
			return data == "\x1b[3$"
		case "pageup":
			return data == "\x1b[5$"
		case "pagedown":
			return data == "\x1b[6$"
		case "home":
			return data == "\x1b[7$"
		case "end":
			return data == "\x1b[8$"
		}
	}
	// rxvt ctrl sequences
	if mod == modCtrl {
		switch baseKey {
		case "up":
			return data == "\x1bOa"
		case "down":
			return data == "\x1bOb"
		case "right":
			return data == "\x1bOc"
		case "left":
			return data == "\x1bOd"
		case "insert":
			return data == "\x1b[2^"
		case "delete":
			return data == "\x1b[3^"
		case "pageup":
			return data == "\x1b[5^"
		case "pagedown":
			return data == "\x1b[6^"
		case "home":
			return data == "\x1b[7^"
		case "end":
			return data == "\x1b[8^"
		}
	}

	return false
}
