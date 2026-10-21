//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A pig_strip_word_dictionaries build compiles cjk.go and sea.go out, so the package embeds no dictionary; stock embeds all five.
func TestStripWordDictionariesBuildOmitsDictionaryEmbeds(t *testing.T) {
	if testing.Short() {
		t.Skip("go list in -short mode")
	}
	embeds := func(tags ...string) []string {
		args := append([]string{"list"}, tags...)
		args = append(args, "-f", `{{join .EmbedFiles " "}}`, "github.com/MichaelKinsy/PiG/internal/wordsegmenter")
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}
	want := []string{"burmese_dictionary.bin", "cjk_dictionary.bin", "khmer_dictionary.bin", "lao_dictionary.bin", "thai_dictionary.bin"}
	if got := embeds(); !slices.Equal(got, want) {
		t.Fatalf("stock embeds %v, want %v", got, want)
	}
	if got := embeds("-tags", pigstrip.Tag(pigstrip.WordDictionaries)); len(got) != 0 {
		t.Fatalf("a %s build embeds %v", pigstrip.Tag(pigstrip.WordDictionaries), got)
	}
}

// A Piglet that strips word-dictionaries segments each CJK or Southeast Asian run as one word, as a Binary built without the
// dictionaries does; stock splits the run with the dictionary.
func TestRuntimeStripWordDictionariesKeepsRunsWhole(t *testing.T) {
	segments := func(text string) []string {
		var out []string
		for segment := range Segments(text) {
			out = append(out, segment.Segment)
		}
		return out
	}
	cases := []struct {
		text  string
		stock []string
	}{
		{"日本語文章", []string{"日本語", "文章"}},
		{"สวัสดีครับ", []string{"สวัสดี", "ครับ"}},
	}
	for _, c := range cases {
		if got := segments(c.text); !slices.Equal(got, c.stock) {
			t.Fatalf("stock %q segments %q, want %q", c.text, got, c.stock)
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.WordDictionaries))
	for _, c := range cases {
		if got := segments(c.text); !slices.Equal(got, []string{c.text}) {
			t.Fatalf("stripped %q segments %q, want one word", c.text, got)
		}
	}
}
