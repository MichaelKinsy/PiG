package surfaces

import (
	"strings"
	"testing"
	"time"
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
		{"de-CH", "de-CH", "the same"},
		{"en-US", "en-US", "the same"},
		{"zh-Hans-CN", "zh-Hans-CN", "a script is a subtag canonicalize keeps"},
		{"zh-Hant-TW", "zh-Hant-TW", "the same"},
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
	// Exercise nil-language FormatNumber/formatClock through the platformLocale seam with Windows LOCALE_SNAME values on every OS.
	saved := platformLocale
	t.Cleanup(func() { platformLocale = saved })
	at := time.Date(2026, time.June, 15, 14, 5, 7, 0, time.UTC)
	for _, c := range []struct {
		sname, wantNum, wantClock string
	}{
		{"de-DE", "1.234.567,891", "14:05"},
		{"de-CH", "1\x27234\x27567.891", "14:05"},
		{"zh-Hant-TW", "1,234,567.891", "下午2:05"},
		{"de-DE_phoneb", "1.234.567,891", "14:05"},
		{"", "1,234,567.891", "2:05 PM"},
	} {
		platformLocale = func() string { return windowsLocaleTag(c.sname) }
		gotNum, err := FormatNumber(nil, 1234567.891, nil)
		if err != nil || gotNum != c.wantNum {
			t.Errorf("LOCALE_SNAME %q FormatNumber(nil) = %q, %v; want %q", c.sname, gotNum, err, c.wantNum)
		}
		gotClock, err := formatClock(nil, at, nil)
		if err != nil || gotClock != c.wantClock {
			t.Errorf("LOCALE_SNAME %q formatClock(nil) = %q, %v; want %q", c.sname, gotClock, err, c.wantClock)
		}
	}
}
