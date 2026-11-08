package surfaces

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// pi: packages/chord/src/surfaces/translations.ts (canonicalLocale) and language.ts (directionOf), against the Intl of the pinned Node.

type intlAnswer struct {
	Canon *string `json:"canon"`
	Dir   *string `json:"dir"`
}

// askIntl is what Node's Intl says of each tag.
func askIntl(t *testing.T, tags []string) []intlAnswer {
	t.Helper()
	input, err := json.Marshal(tags)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/locale_oracle.mjs")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Intl oracle: %v\n%s", err, &stderr)
	}
	var answers []intlAnswer
	if err := json.Unmarshal(output, &answers); err != nil {
		t.Fatal(err)
	}
	if len(answers) != len(tags) {
		t.Fatalf("Intl oracle answered %d of %d tags", len(answers), len(tags))
	}
	return answers
}

func localeCorpus() []string {
	tags := []string{
		"pt-br", "iw", "ac", "aa-AB", "en-XX", "eng", "alb", "zh-hant-tw", "en-u-ca-gregory", "de-DE-u-co-phonebk", "i-klingon", "en-US-POSIX",
		"und", "EN", "es-419", "sr-Latn-RS", "in", "mo", "art-lojban", "cmn", "zh-cmn-Hans", "en-GB-oed", "ji", "sh", "no", "tl", "zh-min-nan",
		"de-1996", "en-x-foo", "x-private", "", "en_US", "fil", "qaa", "en-Latn-US", "ar-SA-u-nu-latn", "ja-JP-u-ca-japanese",
		"ar", "he", "fa", "ur", "ps", "sd", "ckb", "yi", "dv", "ug", "az-Arab", "az-IR", "pa-PK", "ks", "syr", "ku-Arab", "mzn", "uz-Arab",
		"ms-Arab", "ha-Arab", "rhg", "nqo", "ff-Adlm", "sd-Deva", "pa-Arab", "ku", "lrc", "glk", "bal", "arc", "sam", "ar-EG", "ru-SU", "uz-SU",
		"en-US-u-kn-true", "en-u-kn-true-ca-gregory", "en-t-fr-ca", "en-t-m0-name", "en-a-bbb-u-ca-x", "en-a", "en-u", "en-x", "en-x-", "en-",
		"-en", "en--US", "EN-latn-us", "de-de-1996", "de-1996-1996", "abcd", "abcde", "abcdefgh", "abcdefghi", "en-u-ca", "en-U-CA-GREGORY",
		"en-u-attr-ca-gregory", "en-u-ca-gregory-ca-buddhist", "sr-Cyrl-CS", "hy-arevela", "ja-Latn-hepburn-heploc", "zh-guoyu", "cel-gaulish",
		"en-US-u-va-posix", "en-polytoni", "el-polytoni", "ru-SU-u-co-x", "en-ABCDE", "en-1234", "en-123", "en-12", "en-a1b2c", "und-Latn",
		"und-US", "x-i-foo", "i-ami", "sgn-BE-FR", "en-GB-x-twain", "en-US-x-a", "en-x-a-b", "en-x-a-bcdefghij", "en-t-en-latn-us-t0-abc",
	}
	for _, a := range "abcdefghijklmnopqrstuvwxyz" {
		for _, b := range "abcdefghijklmnopqrstuvwxyz" {
			tags = append(tags, string(a)+string(b))
			for _, c := range "abcdefghijklmnopqrstuvwxyz" {
				tags = append(tags, string(a)+string(b)+string(c))
			}
		}
	}
	for _, language := range []string{"en", "ru", "uz", "sr", "zh", "de", "fr", "sh"} {
		for _, a := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			for _, b := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
				tags = append(tags, language+"-"+string(a)+string(b))
			}
		}
		for i := range 1000 {
			tags = append(tags, fmt.Sprintf("%s-%03d", language, i))
		}
	}
	// Random well-formed and malformed tags, seeded.
	random := rand.New(rand.NewSource(7))
	// Languages first, then subtags; two quirks of Node's ICU stay out of the alphabet: `TW` in capitals as a language (`ak`; `tw` in
	// small letters stays `tw`) and `true` or a variant of an alias in a `-t-` or private-use extension (D458).
	languages := []string{"en", "de", "zh", "sr", "ar", "ja", "fr", "abc", "ab", "und", "nu", "uz", "ru"}
	pieces := []string{"en", "de", "Latn", "Hant", "Arab", "US", "DE", "GB", "419", "1996", "rozaj", "posix", "u", "t", "x", "a", "ca", "co", "nu",
		"gregory", "phonebk", "latn", "fr", "abc", "ab", "k0", "m0", "und", "hepburn", "heploc", "aaland", "polytoni", "xyzzy12345"}
	for range 3000 {
		n := 1 + random.Intn(6)
		parts := make([]string, n)
		parts[0] = languages[random.Intn(len(languages))]
		for i := 1; i < n; i++ {
			parts[i] = pieces[random.Intn(len(pieces))]
		}
		tags = append(tags, strings.Join(parts, "-"))
	}
	return tags
}

func TestLocaleTablesMatchIntl(t *testing.T) {
	tags := localeCorpus()
	answers := askIntl(t, tags)
	failures := 0
	bad := func(format string, args ...any) {
		t.Helper()
		t.Errorf(format, args...)
		if failures++; failures >= 60 {
			t.Fatal("too many differences")
		}
	}
	for i, tag := range tags {
		canon, ok := CanonicalLocale(tag)
		want := answers[i]
		switch {
		case want.Canon == nil && ok:
			bad("CanonicalLocale(%q) = %q, Intl refuses it", tag, canon)
		case want.Canon != nil && !ok:
			bad("CanonicalLocale(%q) refuses it, Intl says %q", tag, *want.Canon)
		case want.Canon != nil && canon != *want.Canon:
			bad("CanonicalLocale(%q) = %q, Intl says %q", tag, canon, *want.Canon)
		}
		if want.Dir != nil {
			dir, err := directionOf(tag)
			if err != nil || dir != *want.Dir {
				bad("directionOf(%q) = %q (%v), Intl says %q", tag, dir, err, *want.Dir)
			}
		}
	}
}
