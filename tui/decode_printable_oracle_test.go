package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// decodePrintableKey (keys.ts decodeKittyPrintable and decodeModifyOtherKeysPrintable) runs against the vendored Pi module over CSI-u and modifyOtherKeys sequences with plain, shifted, locked and oversized numbers.
// A modifier that does not fit 32 bits wraps through ToInt32 in the bit tests, a codepoint above 2^53 is a rounded double, and Shift with no shifted key leaves the base codepoint alone in Pi; PiG uppercases a base letter a-z there (D185), which pigUppercasesBaseOnlyShiftLetter allows and nothing else.
func TestDecodePrintableKeyMatchesPi(t *testing.T) {
	var corpus []string
	add := func(format string, args ...any) { corpus = append(corpus, fmt.Sprintf(format, args...)) }
	for _, cp := range []string{"97", "65", "122", "32", "31", "0", "233", "955", "128512", "1114111", "1114112", "55357", "57344", "57399", "57414", "9007199254740993", "99999999999999999999", "00097"} {
		for _, mod := range []string{"", ";1", ";2", ";3", ";5", ";9", ";65", ";129", ";193", ";4294967297", ";4294967298", ";4294967299", ";2147483649", ";9223372036854775809", ";99999999999999999999", ";2:3", ";1:2", ";0"} {
			add("\x1b[%s%su", cp, mod)
		}
	}
	for _, shifted := range []string{"65", "", "32", "31", "1114112", "99999999999999999999", "9007199254740993"} {
		for _, mod := range []string{";2", ";1", ";3", ";4294967298"} {
			add("\x1b[97:%s%su", shifted, mod)
			add("\x1b[97:%s:97%su", shifted, mod)
		}
	}
	for _, mod := range []string{"1", "2", "3", "5", "65", "129", "4294967297", "4294967298", "99999999999999999999", "0"} {
		for _, cp := range []string{"97", "65", "31", "955", "1114111", "1114112", "99999999999999999999", "9007199254740993", "0"} {
			add("\x1b[27;%s;%s~", mod, cp)
		}
	}
	corpus = append(corpus, "", "a", "\x1b[u", "\x1b[97u\n", "\x1b[97;2u ", "\x1b[٣u", "\x1b[97;u", "\x1b[97::u", "\x1b[27;2;97", "\x1b[57;2;97~x")
	type result struct {
		S  string
		OK bool
	}
	var want []result
	pioracle.Run(t, `
const keys = await load("pi-tui/keys.js");
const esc = (s) => Array.from({ length: s.length }, (_, i) => s.charCodeAt(i)).map((u) => u < 0x20 || (u >= 0xd800 && u < 0xe000) ? "<U+" + u.toString(16).padStart(4, "0") + ">" : String.fromCharCode(u)).join("");
emit(input.map((data) => { const r = keys.decodePrintableKey(data); return { S: r === undefined ? "" : esc(r), OK: r !== undefined }; }));`, corpus, &want)
	bad := 0
	for i, data := range corpus {
		s, ok := DecodePrintableKey(data)
		got := result{unitEscape(s), ok}
		if pigUppercasesBaseOnlyShiftLetter(data, got.S, want[i].S) {
			continue
		}
		if got != want[i] {
			if bad++; bad <= 12 {
				t.Errorf("%q: Go %+v, Pi %+v", data, got, want[i])
			}
		}
	}
	if bad > 12 {
		t.Errorf("%d of %d differ", bad, len(corpus))
	}
}

// pigUppercasesBaseOnlyShiftLetter reports the one sequence class D185 allows to differ from Pi: a Kitty CSI-u sequence with Shift, no Alt or Ctrl and no shifted key, whose base letter a-z Pi types as it is and PiG types as the capital.
func pigUppercasesBaseOnlyShiftLetter(data, got, want string) bool {
	m := kittyCSIURegex.FindStringSubmatch(data)
	if m == nil || m[2] != "" || m[4] == "" || len(want) != 1 || want[0] < 'a' || want[0] > 'z' {
		return false
	}
	mod, err := strconv.Atoi(m[4])
	if err != nil {
		return false
	}
	mod--
	return mod&kittyModifierShift != 0 && mod&(kittyModifierAlt|kittyModifierCtrl) == 0 && got != want && strings.EqualFold(got, want)
}

func unitEscape(s string) string {
	var b strings.Builder
	for _, u := range jsstring.ToUTF16(s) {
		if u < 0x20 || u >= 0xd800 && u < 0xe000 {
			fmt.Fprintf(&b, "<U+%04x>", u)
		} else {
			b.WriteRune(rune(u))
		}
	}
	return b.String()
}
