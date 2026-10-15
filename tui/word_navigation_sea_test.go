//go:build !pig_strip_word_dictionaries

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The fixture calls Pi 0.87.1's actual word-navigation functions at every UTF-16 cursor, including inside combining sequences and surrogate pairs.
func TestICUSoutheastAsianWordNavigation(t *testing.T) {
	data, err := os.ReadFile("../internal/wordsegmenter/testdata/sea-icu78.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Script, Text      string
			Backward, Forward []int
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("empty ICU corpus")
	}
	probes := 0
	for i, tc := range fixture.Cases {
		if len(tc.Backward) != jsstring.Length(tc.Text)+1 || len(tc.Forward) != jsstring.Length(tc.Text)+1 {
			t.Fatalf("case %d: fixture must probe every UTF-16 cursor in both directions", i)
		}
		probes += len(tc.Backward) + len(tc.Forward)
		for cursor, want := range tc.Backward {
			if got := FindWordBackward(tc.Text, cursor); got != want {
				t.Errorf("case %d (%s) %q backward(%d)=%d want %d", i, tc.Script, tc.Text, cursor, got, want)
				break
			}
		}
		for cursor, want := range tc.Forward {
			if got := FindWordForward(tc.Text, cursor); got != want {
				t.Errorf("case %d (%s) %q forward(%d)=%d want %d", i, tc.Script, tc.Text, cursor, got, want)
				break
			}
		}
	}
	t.Logf("%d corpus texts, %d directional probes", len(fixture.Cases), probes)
}

func BenchmarkSoutheastAsianWordNavigation(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"Thai", "ภาษาไทยภาษาไทย"},
		{"Lao", "ພາສາລາວສະບາຍດີ"},
		{"Khmer", "សួស្តីពិភពលោកភាសាខ្មែរ"},
		{"Burmese", "မြန်မာဘာသာစကားမင်္ဂလာပါ"},
		{"Mixed", "abcภาษาไทยພາສາລາວမြန်မာဘာသာစကား你好"},
	} {
		for _, repeat := range []int{1, 128, 4096} {
			b.Run(fmt.Sprintf("%s/%d", tc.name, repeat), func(b *testing.B) {
				text := strings.Repeat(tc.text, repeat)
				b.ReportAllocs()
				for b.Loop() {
					FindWordForward(text, 0)
				}
			})
		}
	}
}
