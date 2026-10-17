package ai

import (
	"encoding/hex"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// randomDateStrings joins date, time and zone pieces and then inserts, deletes and replaces characters, including the code units
// V8 counts as white space (U+00A0, U+FEFF, U+2028, U+2029, tab) and a non-ASCII letter.
func randomDateStrings() []string {
	random := rand.New(rand.NewSource(11))
	pieces := [][]string{
		{"", "Mon, ", "Tue ", "Monday ", "Fri,", "xyz ", "(c) "},
		{"2024-03-05", "2024-3-5", "2024/03/05", "03/05/2024", "3/5/24", "5 Mar 2024", "March 5, 2024", "Mar 5 2024", "2024-03", "2024", "20240305", "1999-12-31", "0000-01-01", "+002024-03-05", "-000001-01-01", "99", "1/1/1", "12/31/1969", "Feb 30 2024", "Dec 31, 275760", "2024-02-30"},
		{"", "T12:30", "T12:30:45", "T12:30:45.123", "T12:30:45.1234567", " 12:30", " 12:30:45", " 1:2:3", "T24:00", "T24:00:00", " 12:30 PM", " 12:30 am", " 12 PM", "T00:00:00.000", " 23:59:60", "T12", " 12:30:45,5"},
		{"", "Z", "z", "+01:00", "-05:30", "+0100", " GMT", " UTC", " UT", " EST", " PDT", " GMT+0200", " GMT-05:30", "+01", " Z", "+24:00", "+00:60", " (PST)", " America/New_York", " CET", " JST"},
		{"", " ", "  ", ".", ",", "!"},
	}
	var corpus []string
	for range 8000 {
		var b strings.Builder
		for _, choices := range pieces {
			b.WriteString(choices[random.Intn(len(choices))])
		}
		corpus = append(corpus, b.String())
	}
	chars := []rune("0123456789:-+/.,TZ aApPmMJ()\u00a0\t\u2028\u2029\ufeffé")
	for range 12000 {
		base := []rune(corpus[random.Intn(8000)])
		for edits := random.Intn(3) + 1; edits > 0 && len(base) > 0; edits-- {
			at := random.Intn(len(base))
			switch random.Intn(3) {
			case 0:
				base[at] = chars[random.Intn(len(chars))]
			case 1:
				base = append(base[:at], base[at+1:]...)
			default:
				base = append(base[:at], append([]rune{chars[random.Intn(len(chars))]}, base[at:]...)...)
			}
		}
		corpus = append(corpus, string(base))
	}
	return corpus
}

func nodeDateValues(t *testing.T, body string, input any) []*float64 {
	t.Helper()
	var want []*float64
	pioracle.Run(t, body, input, &want, "TZ=America/New_York")
	return want
}

func requireSameDate(t *testing.T, what, input string, got float64, want *float64) bool {
	t.Helper()
	expected := math.NaN()
	if want != nil {
		expected = *want
	}
	if got == expected || math.IsNaN(got) && math.IsNaN(expected) {
		return true
	}
	t.Errorf("%s(%q) = %v, Node %v", what, input, got, expected)
	return false
}

// DateParse is Date.parse of a JavaScript string (session and crash timestamps): UTF-16 code units, so U+00A0 and U+FEFF between tokens are single white space units.
func TestDateParseMatchesNodeOnRandomStrings(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	corpus := randomDateStrings()
	want := nodeDateValues(t, `emit(input.map((s) => { const v = Date.parse(s); return Number.isNaN(v) ? null : v; }));`, corpus)
	if len(want) != len(corpus) {
		t.Fatalf("Node answered %d of %d strings", len(want), len(corpus))
	}
	valid, bad := 0, 0
	for i, text := range corpus {
		if want[i] != nil {
			valid++
		}
		if !requireSameDate(t, "DateParseIn", text, DateParseIn(text, loc), want[i]) {
			if bad++; bad >= 20 {
				t.Fatal("too many differences")
			}
		}
	}
	if valid < 2000 {
		t.Fatalf("only %d of %d strings parse in Node: the corpus is one-sided", valid, len(corpus))
	}
}

// DateParseHeader is Date.parse of a header value, which Node holds as a ByteString: each byte of the value is one code unit.
func TestDateParseHeaderMatchesNodeOnRandomBytes(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	corpus := randomDateStrings()
	encoded := make([]string, len(corpus))
	for i, text := range corpus {
		encoded[i] = hex.EncodeToString([]byte(text))
	}
	want := nodeDateValues(t, `emit(input.map((h) => { const v = Date.parse(Buffer.from(h, "hex").toString("latin1")); return Number.isNaN(v) ? null : v; }));`, encoded)
	if len(want) != len(corpus) {
		t.Fatalf("Node answered %d of %d strings", len(want), len(corpus))
	}
	bad := 0
	for i, text := range corpus {
		if !requireSameDate(t, "jsDateParseIn", text, jsDateParseIn(text, loc), want[i]) {
			if bad++; bad >= 20 {
				t.Fatal("too many differences")
			}
		}
	}
}
