package tui

// pi: packages/tui/src/keys.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type keysCase struct {
	Data  string `json:"data"`
	Kitty bool   `json:"kitty"`
}

type keysAnswer struct {
	Parse   *string `json:"parse"`
	Matches []int   `json:"matches"`
}

func keyIDs() []string {
	bases := []string{
		"a", "b", "c", "z", "A", "0", "5", "9", "escape", "esc", "enter", "return", "tab", "space", "backspace", "delete", "insert", "clear", "home", "end", "pageUp", "pageDown",
		"up", "down", "left", "right", "f1", "f4", "f5", "f12", "`", "-", "=", "[", "]", "\\", ";", "'", ",", ".", "/", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "_", "+", "|", "~", "{", "}", ":", "<", ">", "?",
	}
	mods := []string{"ctrl", "shift", "alt", "super"}
	var ids []string
	for _, mask := range []int{0, 1, 2, 4, 8, 5, 6, 3, 7, 12, 15} {
		prefix := ""
		for i, m := range mods {
			if mask&(1<<i) != 0 {
				prefix += m + "+"
			}
		}
		for _, base := range bases {
			ids = append(ids, prefix+base)
		}
	}
	// keys.ts parseKeyId ignores a part that names no modifier, lowercases as JavaScript does (İ becomes two characters, the Kelvin sign k),
	// and rejects an empty base key.
	return append(ids, "meta+a", "cmd+k", "hyper+up", "ctrl+meta+c", "meta+f1", "meta+clear", "ctrl+shift+clear", "shift+f1", "Ctrl+A", "İ", "ctrl+İ",
		"\u212a", "ctrl+\u212a", "", "ctrl+", "+", "ctrl++", "shift", "ctrl+ctrl+a", "alt+meta+enter", "super+meta+tab")
}

// keysCases cover legacy single bytes and ESC-prefixed bytes, SS3 and CSI cursor, editing and function sequences with and without modifiers,
// xterm modifyOtherKeys, and Kitty CSI-u sequences with shifted and base-layout alternates, modifier masks (lock bits included), event types and text
// fields, each run with the Kitty protocol off and on, plus truncated and byte-flipped variants.
func keysCases() []keysCase {
	random := rand.New(rand.NewSource(20260932))
	var data []string
	for b := range 0x80 {
		data = append(data, string(rune(b)), "\x1b"+string(rune(b)))
	}
	// Numbers keys.ts's \d+ patterns reject (a sign, an empty event) or read as JavaScript numbers (modifiers past 2^31 wrap in bitwise
	// operators, codepoints and modifiers past 2^53 round, digit runs past an int64 still parse), and unmodified enter and tab in
	// modifyOtherKeys form.
	data = append(data, "\x1b[27;1;9~", "\x1b[27;1;13~", "\x1b[27;1;27~", "\x1b[27;1;32~", "\x1b[27;1;127~", "\x1b[27;01;13~", "\x1b[27;1;-1~",
		"\x1b[27;+1;13~", "\x1b[27;5;+97~", "\x1b[1;5:A", "\x1b[1;+5A", "\x1b[1;5:xA", "\x1b[1;5:H", "\x1b[3;+5~", "\x1b[+3;5~", "\x1b[3;5:~",
		"\x1b[1;4294967301A", "\x1b[1;4294967302H", "\x1b[3;4294967298~", "\x1b[99;4294967301u", "\x1b[99;2147483654u", "\x1b[99;99999999999999999999u",
		"\x1b[99;18446744073709551621u", "\x1b[27;4294967301;97~", "\x1b[27;6;4294967393~", "\x1b[4294967393;5u", "\x1b[9007199254740993;5u",
		"\x1b[27;9007199254740995;99~", "\x1b[9007199254740991;5u", "\x1b[9007199254741039;5u", "\x1b[9007199254741038;5u", "\x1b[27;5;9007199254741039~", "\x1b[18446744073709551663u", "\x1b[1"+strings.Repeat("0", 400)+"u",
		"\x1b[1089::"+strings.Repeat("9", 30)+";5u", "\x1b[1089::4294967395;5u", "\x1b[1;"+strings.Repeat("9", 320)+"A")
	data = append(data, "é", "\x1bé", "😀", "\x1b😀", "ab", "\x1b\x1b", "\x1b\x1b[A", "\x1b\x1bOA", "\x1b[200~", "\x1b[I", "\x1b[O", "\x1b[M !!", "\x1b]52;c;x\x07")
	for _, final := range "ABCDEFHPQRSZabcdefghijklmnopqrstuvwxyz" {
		data = append(data, "\x1b["+string(final), "\x1bO"+string(final), "\x1b[1;"+"5"+string(final), "\x1b[1;2"+string(final), "\x1b[1;3"+string(final), "\x1b[1;9"+string(final), "\x1b[1;"+"7:3"+string(final), "\x1b[1;1:2"+string(final), "\x1b\x1b["+string(final))
	}
	for n := range 36 {
		for _, mod := range []string{"", ";1", ";2", ";3", ";4", ";5", ";6", ";7", ";8", ";9", ";16", ";17", ";65", ";129", ";5:3", ";2:2"} {
			data = append(data, fmt.Sprintf("\x1b[%d%s~", n, mod))
		}
	}
	codepoints := []int{97, 65, 122, 90, 48, 57, 13, 9, 27, 32, 127, 8, 0, 1, 31, 33, 47, 96, 126, 1040, 1072, 0x1f600, 57344, 57399, 57414, 57441, 57442, 57450, 57454, 57358, 57359, 57360, 57361, 57362, 57363, 1114111, 1114112, 99999999, 106, 108, 45, 61, 91, 92, 93, 59, 39, 44, 46}
	mods := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13, 16, 17, 33, 65, 66, 69, 129, 193, 256}
	pick := func(list []int) int { return list[random.Intn(len(list))] }
	for range 1300 {
		cp := fmt.Sprint(pick(codepoints))
		switch random.Intn(4) {
		case 0:
			cp += ":" + fmt.Sprint(pick(codepoints))
		case 1:
			cp += ":" + fmt.Sprint(pick(codepoints)) + ":" + fmt.Sprint(pick(codepoints))
		case 2:
			cp += "::" + fmt.Sprint(pick(codepoints))
		}
		field := ""
		if random.Intn(5) != 0 {
			field = ";" + fmt.Sprint(pick(mods))
			if random.Intn(3) == 0 {
				field += ":" + fmt.Sprint(1+random.Intn(4))
			}
			if random.Intn(6) == 0 {
				field += ";" + fmt.Sprint(pick(codepoints))
			}
		}
		if random.Intn(8) == 0 {
			data = append(data, fmt.Sprintf("\x1b[27;%d;%d~", pick(mods), pick(codepoints)))
		}
		data = append(data, "\x1b["+cp+field+"u")
	}
	var cases []keysCase
	for _, d := range data {
		variants := []string{d}
		if len(d) > 2 && random.Intn(6) == 0 {
			variants = append(variants, d[:len(d)-1], d+"x")
		}
		if len(d) > 1 && random.Intn(10) == 0 {
			bytes := []byte(d)
			bytes[random.Intn(len(bytes))] ^= byte(1 << random.Intn(7))
			variants = append(variants, string(bytes))
		}
		for _, v := range variants {
			if !utf8.ValidString(v) {
				// JSON would hand Pi U+FFFD in place of the invalid bytes, so the two sides would read different input.
				continue
			}
			cases = append(cases, keysCase{Data: v, Kitty: false}, keysCase{Data: v, Kitty: true})
		}
	}
	return cases
}

// keys.ts runs in Node from the pinned source against the same inputs: parseKey's identifier and the set of key ids matchesKey accepts, for every
// input with the Kitty protocol off and on, must match Pi's.
func TestParseAndMatchKeyMatchPiOnSeededInput(t *testing.T) {
	ids, cases := keyIDs(), keysCases()
	payload, err := json.Marshal(map[string]any{"ids": ids, "cases": cases})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "tui", "src", "keys.ts")
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keys.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "WT_SESSION=") || strings.HasPrefix(kv, "SSH_")
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []keysAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(cases) {
		t.Fatalf("Pi answered %d of %d inputs", len(expected), len(cases))
	}
	t.Setenv("WT_SESSION", "")
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	parsed, matched, differing := 0, 0, 0
	for i, c := range cases {
		SetKittyProtocolActive(c.Kitty)
		var got keysAnswer
		if key, ok := ParseKey(c.Data); ok {
			got.Parse = &key
		}
		for index, id := range ids {
			if MatchesKeyID(c.Data, id) {
				got.Matches = append(got.Matches, index)
			}
		}
		want := expected[i]
		if want.Parse != nil {
			parsed++
		}
		if len(want.Matches) > 0 {
			matched++
		}
		gotParse, wantParse := "<none>", "<none>"
		if got.Parse != nil {
			gotParse = *got.Parse
		}
		if want.Parse != nil {
			wantParse = *want.Parse
		}
		if gotParse != wantParse || !slices.Equal(got.Matches, want.Matches) {
			if differing++; differing <= 12 {
				name := func(indexes []int) []string {
					var out []string
					for _, index := range indexes {
						out = append(out, ids[index])
					}
					return out
				}
				t.Errorf("%q (kitty %v) differs from Pi:\n  Pig parse %s, matches %v\n  Pi  parse %s, matches %v", c.Data, c.Kitty, gotParse, name(got.Matches), wantParse, name(want.Matches))
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d inputs differ from Pi", differing, len(cases))
	}
	if parsed < len(cases)/6 || matched < len(cases)/6 {
		t.Errorf("inputs are one-sided: Pi parsed %d and matched %d of %d", parsed, matched, len(cases))
	}
}
