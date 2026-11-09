package sqlite

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi's statements.ts builds the JSON array parameter of a statement from the JSON.stringify text of each value as a string
// and groups those into arrays of at most maxArrayCharacters. The insert differential test builds Pi's inserts that way;
// the builder the reads and deletions use is checked against it.

// jsonText is JSON.stringify of a cell, or of an array of cells.
func jsonText(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	case string:
		encoded, err := jsstring.MarshalJSON(typed)
		if err != nil {
			panic(err)
		}
		return string(encoded)
	case []cell:
		var out strings.Builder
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString(jsonText(item))
		}
		out.WriteByte(']')
		return out.String()
	}
	panic("not a cell")
}

// utf16Length is a string's JavaScript length.
func utf16Length(text string) int {
	units := 0
	for rest := text; rest != ""; {
		r, size := jsstring.DecodeCodePoint(rest)
		units++
		if r >= 0x10000 {
			units++
		}
		//portlint:allow utf16units advances by the byte size of the code point just decoded; the count is UTF-16 units
		rest = rest[size:]
	}
	return units
}

// jsonArrays is the JSON values parts, in order, as JSON arrays of at most maxArrayCharacters each (a longer value alone).
func jsonArrays(parts []string) []string {
	var arrays []string
	var chunk []string
	// The array's text: its brackets, its parts and the commas between them.
	length := 2
	for _, part := range parts {
		units := utf16Length(part)
		if len(chunk) > 0 && length+1+units > maxArrayCharacters {
			arrays = append(arrays, "["+strings.Join(chunk, ",")+"]")
			chunk = nil
			length = 2
		}
		if len(chunk) > 0 {
			length++
		}
		length += units
		chunk = append(chunk, part)
	}
	if len(chunk) > 0 {
		arrays = append(arrays, "["+strings.Join(chunk, ",")+"]")
	}
	return arrays
}

// referenceArrays is Pi's arrays for values.
func referenceArrays(values []cell) []string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = jsonText(value)
	}
	return jsonArrays(parts)
}

var arrayPieces = []string{"a", "Z", "\"", "\\", "\n", "\x00", "<", "é", "€", "😀", "\u2028", "\ufffd", "\xed\xa0\xbd", "\xed\xb8\x80", "\xff", "\xe2\x82", "plain text "}

func randomCellText(rng *rand.Rand, pieces int) string {
	var out strings.Builder
	for range pieces {
		out.WriteString(arrayPieces[rng.IntN(len(arrayPieces))])
	}
	return out.String()
}

func randomArrayCell(rng *rand.Rand, pieces int) cell {
	switch rng.IntN(5) {
	case 0:
		return nil
	case 1:
		return rng.Int64N(1<<62) - 1<<61
	case 2:
		return rng.IntN(1000)
	}
	return randomCellText(rng, rng.IntN(pieces+1))
}

func checkArrays(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d arrays, want %d", label, len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s: array %d differs (%d bytes, want %d)", label, index, len(got[index]), len(want[index]))
		}
	}
}

func TestCellArraysMatchPisArrays(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	// Small sets: one array.
	for n := range 3000 {
		values := make([]cell, rng.IntN(8))
		for index := range values {
			values[index] = randomArrayCell(rng, 12)
		}
		checkArrays(t, "cells "+strconv.Itoa(n), cellArrays(values), referenceArrays(values))
	}
	// Sets over the bound: values of many sizes, ones that end exactly at the bound, one longer than the bound alone, and
	// astral characters, which count two units each.
	for n := range 40 {
		var values []cell
		for total := 0; total < 3*maxArrayCharacters; {
			var value cell
			switch rng.IntN(6) {
			case 0:
				value = strings.Repeat("😀", 1+rng.IntN(maxArrayCharacters/4))
			case 1:
				value = strings.Repeat("x", 1+rng.IntN(maxArrayCharacters/2))
			case 2:
				value = randomCellText(rng, 1+rng.IntN(500))
			case 3:
				value = strings.Repeat("x", maxArrayCharacters-2-2) // quotes and brackets fill the array exactly
			case 4:
				value = strings.Repeat("é", maxArrayCharacters)
			default:
				value = randomArrayCell(rng, 30)
			}
			values = append(values, value)
			total += len(jsonText(value))
		}
		want := referenceArrays(values)
		if len(want) < 2 {
			t.Fatalf("set %d fits one array; the test does not reach the split", n)
		}
		checkArrays(t, "large cells "+strconv.Itoa(n), cellArrays(values), want)
	}
	if got := cellArrays(nil); len(got) != 0 {
		t.Fatalf("no values gave %d arrays", len(got))
	}
}

// The bound is on the whole array, brackets and commas included: values whose array comes to exactly maxArrayCharacters
// share one, and one character more splits them.
func TestCellArraysSplitExactlyAtTheBound(t *testing.T) {
	for _, extra := range []int{-2, -1, 0, 1, 2} {
		for _, first := range []int{1, 1000, maxArrayCharacters / 2} {
			// "[" + "xxx" + "," + "yyy" + "]" with the quotes of each string: 2 + (first+2) + 1 + (second+2).
			second := maxArrayCharacters - 7 - first + extra
			values := []cell{strings.Repeat("x", first), strings.Repeat("y", second), int64(77), "z", int64(7)}
			want := referenceArrays(values)
			checkArrays(t, "cells", cellArrays(values), want)
			builder := newJSONArrayBuilder(0)
			for _, value := range values {
				builder.add(value)
			}
			checkArrays(t, "builder", builder.finish(), want)
		}
	}
}

func TestEncodeIndexedStringIsJSONStringify(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 3000 {
		text := randomCellText(rng, rng.IntN(40))
		if got, want := encodeIndexedString(text), jsonText(text); got != want {
			t.Fatalf("%q: %s, want %s", text, got, want)
		}
	}
}
