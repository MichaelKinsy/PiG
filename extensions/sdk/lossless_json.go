package sdk

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// protectSurrogates carries JSON text that holds an unpaired surrogate through code written over decoded Go values. `JSON.stringify` writes the unpaired surrogate
// of a JavaScript string as an escape ("\ud83d"), and a Go string is UTF-8, so a decoded value has U+FFFD for it. Each unpaired surrogate escape becomes a private-use
// character that occurs nowhere in the text (one unit, as the escape is one unit, and a different character for a different unit); restore writes the escapes back
// into the JSON the code produced, and the set of stand-ins lets a length that counts the JSON.stringify text count each as the six characters of its escape. Text with
// none is returned as it is.
func protectSurrogates(text []byte) ([]byte, map[rune]bool, func([]byte) []byte) {
	type standIn struct {
		char rune
		unit uint16
	}
	var stands []standIn
	var out []byte
	var used map[rune]bool
	next := rune(0xe000)
	allocate := func(unit uint16) rune {
		for _, s := range stands {
			if s.unit == unit {
				return s.char
			}
		}
		if used == nil {
			used = map[rune]bool{}
			for _, r := range string(text) {
				if r >= 0xe000 && r <= 0xf8ff {
					used[r] = true
				}
			}
			// A private-use escape of the text is one its own value keeps; an over-count is harmless.
			for at := 0; ; {
				i := bytes.Index(text[at:], []byte(`\u`))
				if i < 0 {
					break
				}
				at += i + 2
				if at+4 <= len(text) {
					if v, err := strconv.ParseUint(string(text[at:at+4]), 16, 32); err == nil {
						used[rune(v)] = true
					}
				}
			}
		}
		for used[next] {
			next++
		}
		char := next
		next++
		stands = append(stands, standIn{char, unit})
		return char
	}
	for i := 0; i < len(text); {
		if text[i] != '\\' || i+1 >= len(text) {
			out = append(out, text[i])
			i++
			continue
		}
		unit, ok := hexEscapeUnit(text[i:])
		if !ok {
			out = append(out, text[i:i+2]...)
			i += 2
			continue
		}
		low, lowOK := uint16(0), false
		if unit >= 0xd800 && unit < 0xdc00 && i+6 < len(text) {
			low, lowOK = hexEscapeUnit(text[i+6:])
			lowOK = lowOK && low >= 0xdc00 && low < 0xe000
		}
		switch {
		case lowOK:
			out = append(out, text[i:i+12]...)
			i += 12
		case unit >= 0xd800 && unit < 0xe000:
			out = append(out, string(allocate(unit))...)
			i += 6
		default:
			out = append(out, text[i:i+6]...)
			i += 6
		}
	}
	restore := func(encoded []byte) []byte {
		if len(stands) == 0 {
			return encoded
		}
		text := string(encoded)
		for _, s := range stands {
			text = strings.ReplaceAll(text, string(s.char), fmt.Sprintf(`\u%04x`, s.unit))
		}
		return []byte(text)
	}
	if len(stands) == 0 {
		return text, nil, restore
	}
	standIns := map[rune]bool{}
	for _, s := range stands {
		standIns[s.char] = true
	}
	return out, standIns, restore
}

// hexEscapeUnit is the unit of a \uXXXX escape at the start of text.
func hexEscapeUnit(text []byte) (uint16, bool) {
	if len(text) < 6 || text[0] != '\\' || text[1] != 'u' {
		return 0, false
	}
	v, err := strconv.ParseUint(string(text[2:6]), 16, 16)
	return uint16(v), err == nil
}

// PrepareBranchEntriesJSON is [PrepareBranchEntries] over the raw JSON of the entries ([Context.GetEntries] gives it) that answers with JSON,
// {"messages":[...],"fileOps":{"read":[],"written":[],"edited":[]},"totalTokens":N}. A JavaScript string in the entries may hold an unpaired surrogate, which a decoded
// Go string cannot hold; this path keeps every unit, so the messages come back as the entries held them.
func PrepareBranchEntriesJSON(entries []json.RawMessage, tokenBudget int) ([]byte, error) {
	array := append([]byte{'['}, bytes.Join(rawBytes(entries), []byte{','})...)
	array = append(array, ']')
	protected, standIns, restore := protectSurrogates(array)
	var decoded []map[string]any
	if err := json.Unmarshal(protected, &decoded); err != nil {
		return nil, errors.New("the entries are not JSON objects: " + err.Error())
	}
	prepared, err := prepareBranchEntries(decoded, tokenBudget, standIns)
	if err != nil {
		return nil, err
	}
	nonNil := func(values []any) []any {
		if values == nil {
			return []any{}
		}
		return values
	}
	messages := prepared.Messages
	if messages == nil {
		messages = []map[string]any{}
	}
	encoded, err := json.MarshalJS(map[string]any{
		"messages":    messages,
		"fileOps":     map[string]any{"read": nonNil(prepared.FileOps.Read), "written": nonNil(prepared.FileOps.Written), "edited": nonNil(prepared.FileOps.Edited)},
		"totalTokens": prepared.TotalTokens,
	})
	if err != nil {
		return nil, err
	}
	return restore(encoded), nil
}

func rawBytes(entries []json.RawMessage) [][]byte {
	out := make([][]byte, len(entries))
	for i, entry := range entries {
		out[i] = entry
	}
	return out
}
