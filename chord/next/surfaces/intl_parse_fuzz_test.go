package surfaces

import (
	"errors"
	"testing"
)

// FuzzParseTag feeds parseTag arbitrary text: it never panics, refuses with ErrLocale only, and the key of an accepted tag is a tag
// that parses to the same key (the form the tables are looked up by). TestLocaleTablesMatchIntl checks the accepted set against
// Node's Intl.getCanonicalLocales over a corpus.
func FuzzParseTag(f *testing.F) {
	for _, seed := range []string{
		"", "en", "en-US", "EN-us", "en_US", "zh-Hant-TW", "sr-Latn-RS", "de-1996", "de-DE-1996", "hy-arevmda", "zh-hakka",
		"i-klingon", "zh-min-nan", "en-GB-oed", "ac", "aa-AB", "und", "und-US", "es-419", "en-u-ca-gregory", "x-private",
		"en-a-bbb-x-a-ccc", "a", "abcdefghi", "en-", "-en", "en--US", "tw", "TW", "cmn", "mo", "iw", "sam",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, tag string) {
		parts, err := parseTag(tag)
		if err != nil {
			if !errors.Is(err, ErrLocale) {
				t.Fatalf("parseTag(%q) = %v, want ErrLocale", tag, err)
			}
			return
		}
		key := parts.aliased().key()
		again, err := parseTag(key)
		if err != nil {
			t.Fatalf("parseTag(%q).key() = %q does not parse: %v", tag, key, err)
		}
		if again.aliased().key() != key {
			t.Fatalf("key of %q is not stable: %q then %q", tag, key, again.aliased().key())
		}
	})
}
