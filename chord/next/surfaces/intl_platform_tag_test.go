package surfaces

import (
	"strings"
	"testing"
)

// windowsLocaleTag maps the name Windows gives for the user's locale to the default locale ICU makes of it. Every expectation is
// READ from ICU's code (icu4c/source/common/putil.cpp, release 78.1, identical in Node v24.22.2's deps/icu-small, the Windows
// branch of `uprv_getDefaultLocaleID`, lines 1796-1869); none is a run against Node (that is the Windows CI test, which can
// only run there).
func TestWindowsLocaleTagIsWhatICUMakesOfTheUsersLocaleName(t *testing.T) {
	for _, c := range []struct {
		name, want, source string
	}{
		{"de-DE", "de-DE", "the name copied as it is (:1816-1829), canonicalize keeps it (:1850)"},
		{"en-US", "en-US", "the same"},
		{"zh-Hans-CN", "zh-Hans-CN", "a script is a subtag canonicalize keeps"},
		{"sr-Latn-RS", "sr-Latn-RS", "the same"},
		{"de-DE_phoneb", "de-DE-phoneb", "`_` becomes `-` (:1816-1829): the sort name is a variant"},
		{"de_DE", "de-DE", "`_` becomes `-` (:1816-1829)"},
		{"", "en-US", "length 0: GetLocaleInfoEx failed (:1808), no locale found: `en_US` (:1861-1863)"},
		{"!!", "en-US", "a name that cannot be made a locale: `en_US` (:1861-1863)"},
		{strings.Repeat("a", 90), "en-US", "cut to 84 characters (:1833-1838), no language of that length: `en_US` (:1861-1863)"},
	} {
		if got := windowsLocaleTag(c.name); got != c.want {
			t.Errorf("windowsLocaleTag(%q) = %q, want %q (%s)", c.name, got, c.want, c.source)
		}
	}
	// A sort name still resolves to its language's data.
	if _, err := numberFormatOf(windowsLocaleTag("de-DE_phoneb")); err != nil {
		t.Errorf("the tag of de-DE_phoneb does not resolve: %v", err)
	}
}
