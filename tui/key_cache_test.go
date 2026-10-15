package tui

import (
	"fmt"
	"slices"
	"sync"
	"testing"
)

// referenceMatchesKeyID is matchesKeyID as it was before key identifiers and keystrokes were parsed once: every call parses the identifier and the input itself.
func referenceMatchesKeyID(data, keyID string) bool {
	baseKey, modifier, parsed := parseKeyID(keyID)
	if !parsed {
		return false
	}
	if expectedCP, dynamic := resolveKeyCodepoint(baseKey); dynamic {
		expectedMod := modifier
		if expectedCP == cpEscape && expectedMod != 0 {
			return false
		}
		if pk := parseCSIu(data); pk != nil {
			if matchesKittySequence(pk, expectedCP, expectedMod) {
				return true
			}
			return expectedCP == cpEnter && matchesKittySequence(pk, cpKPEnter, expectedMod)
		}
		if mok := parseModifyOtherKeys(data); mok != nil && isPrintableKey(baseKey) {
			return matchesPrintableModifyOtherKeys(mok, expectedCP, expectedMod)
		}
		if expectedMod == 0 && expectedCP != cpEscape && expectedCP != cpSpace && expectedCP != cpBackspace && parseModifyOtherKeys(data) != nil {
			return false
		}
		if pk := parseTerminalInput(data); pk != nil {
			return pk.codepoint == expectedCP && pk.modifier == expectedMod && !pk.inexactModifier
		}
		return matchesLegacy(data, baseKey, expectedCP, expectedMod)
	}
	switch modifier {
	case 0:
	case modShift:
		baseKey = "shift+" + baseKey
	case modCtrl:
		baseKey = "ctrl+" + baseKey
	default:
		return false
	}
	return slices.Contains(tuiKeyIDInputs()[baseKey], data)
}

func keyCacheKeyIDs() []string {
	ids := []string{"", "+", "ctrl+", "ctrl+x+y", "bogus+a", "CTRL+C", "Ctrl+Shift+Left", "escape", "ctrl+escape", "esc", "enter", "return", "shift+enter", "alt+enter", "tab", "shift+tab", "space", "backspace", "delete", "insert", "home", "end", "pageUp", "pageDown", "f1", "f12", "clear", "ctrl+alt+super+shift+a"}
	for _, base := range []string{"a", "z", "0", "9", "up", "down", "left", "right", "[", "]", "/", "\\", "-", "=", "`", "'", ";", ","} {
		for _, prefix := range []string{"", "ctrl+", "shift+", "alt+", "super+", "ctrl+shift+", "ctrl+alt+", "ctrl+shift+alt+super+"} {
			ids = append(ids, prefix+base)
		}
	}
	return ids
}

func keyCacheInputs() []string {
	inputs := []string{"", "a", "A", "z", "0", "\x1b", "\r", "\n", "\t", " ", "\x7f", "\x08", "\x1bOM", "\x1b\r", "\x1b[Z", "\x01", "\x03", "\x1b[A", "\x1b[a", "\x1bOa", "\x1b[2$", "\x1b[3~", "\x1b[5~", "\x1b[H", "\x1bOP", "\x1b[11~", "\x1b[24~", "é", "日本"}
	for _, mod := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 129, 193} {
		for _, final := range "ABCDHF" {
			inputs = append(inputs, fmt.Sprintf("\x1b[1;%d%c", mod, final))
		}
		for _, num := range []int{2, 3, 5, 6, 11, 24} {
			inputs = append(inputs, fmt.Sprintf("\x1b[%d;%d~", num, mod))
		}
		for _, cp := range []int{97, 65, 99, 13, 9, 27, 32, 127, 57414, 1089, 91, 47} {
			inputs = append(inputs, fmt.Sprintf("\x1b[%d;%du", cp, mod), fmt.Sprintf("\x1b[%d;%d:3u", cp, mod), fmt.Sprintf("\x1b[27;%d;%d~", mod, cp))
		}
	}
	inputs = append(inputs, "\x1b[1089::99;5u", "\x1b[97:65;2u", "\x1b[u", "\x1b[97", "\x1b[97;u", "\x1b[1;5:2A", "\x1b[27;5;99~x")
	return inputs
}

// Every identifier against every input answers what the uncached matcher answers, with the Kitty protocol off and on.
func TestKeyIDMatchingIsUnchangedByCaching(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		previous := IsKittyProtocolActive()
		SetKittyProtocolActive(kitty)
		restore := func() { SetKittyProtocolActive(previous) }
		for _, id := range keyCacheKeyIDs() {
			for _, data := range keyCacheInputs() {
				if got, want := matchesKeyID(data, id), referenceMatchesKeyID(data, id); got != want {
					restore()
					t.Fatalf("kitty=%v matchesKeyID(%q, %q) = %v, want %v", kitty, data, id, got, want)
				}
			}
		}
		restore()
	}
}

// matchesLegacyModSeq built its expected sequences with fmt.Sprintf; the stack-buffer form writes the same bytes.
func TestLegacyModifiedSequencesMatchTheirFormattedForm(t *testing.T) {
	for baseKey, entry := range keyNameCodepoints {
		for mod := range 260 {
			var expected []string
			if entry.isArrow {
				if final, ok := arrowCPtoFinal[entry.cp]; ok {
					expected = append(expected, fmt.Sprintf("\x1b[1;%d%c", mod+1, final))
				}
			} else if final, ok := homeCPtoFinal[entry.cp]; ok {
				expected = append(expected, fmt.Sprintf("\x1b[1;%d%c", mod+1, final))
			}
			if num, ok := funcCPtoNum[entry.cp]; ok {
				expected = append(expected, fmt.Sprintf("\x1b[%d;%d~", num, mod+1))
			}
			for _, data := range expected {
				if !matchesLegacyModSeq(data, baseKey, mod) {
					t.Fatalf("matchesLegacyModSeq(%q, %q, %d) = false", data, baseKey, mod)
				}
				if matchesLegacyModSeq(data+"x", baseKey, mod) || matchesLegacyModSeq(data[:len(data)-1], baseKey, mod) {
					t.Fatalf("matchesLegacyModSeq accepts a %q with a changed length (%q, %d)", data, baseKey, mod)
				}
			}
		}
	}
}

// Many goroutines matching different keystrokes at once each get the answer for their own input, and a flood of distinct identifiers neither grows the cache past its limit nor leaves it unable to hold the identifiers in use.
func TestKeystrokeParseIsSafeForConcurrentMatching(t *testing.T) {
	inputs, ids := keyCacheInputs(), keyCacheKeyIDs()
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for round := range 300 {
				data, id := inputs[(worker*37+round)%len(inputs)], ids[(worker*11+round)%len(ids)]
				if got, want := matchesKeyID(data, id), referenceMatchesKeyID(data, id); got != want {
					t.Errorf("matchesKeyID(%q, %q) = %v, want %v", data, id, got, want)
					return
				}
			}
		})
	}
	wg.Wait()
	for i := range keyIDCacheLimit + 50 {
		matchesKeyID("a", fmt.Sprintf("ctrl+shift+unknown%d", i))
	}
	if keyIDCacheCount.Load() > keyIDCacheLimit {
		t.Fatalf("%d cached identifiers, limit %d", keyIDCacheCount.Load(), keyIDCacheLimit)
	}
	matchesKeyID("a", "ctrl+c")
	if _, cached := keyIDCache.Load("ctrl+c"); !cached {
		t.Fatal("an identifier looked up after a flood was not cached")
	}
}

// D112: every identifier answers the same before, across and after the cache is emptied at keyIDCacheLimit.
func TestKeyIDCacheEvictionDoesNotChangeMatching(t *testing.T) {
	ids, inputs := keyCacheIDsForEviction(), keyCacheInputs()
	answers := func() []bool {
		var out []bool
		for _, id := range ids {
			for _, data := range inputs {
				out = append(out, matchesKeyID(data, id))
			}
		}
		return out
	}
	before := answers()
	for i := range keyIDCacheLimit + 10 {
		matchesKeyID("a", fmt.Sprintf("alt+flood%d", i))
	}
	if got := answers(); !slices.Equal(got, before) {
		t.Fatal("matching changed after the identifier cache was emptied")
	}
}

func keyCacheIDsForEviction() []string { return keyCacheKeyIDs()[:40] }
