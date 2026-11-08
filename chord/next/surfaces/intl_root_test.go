package surfaces

import (
	"testing"
	"time"
)

// platform sets the three variables ICU reads for the default locale (set to a value, or unset).
func platform(t *testing.T, all, messages, lang *string) {
	t.Helper()
	for name, value := range map[string]*string{"LC_ALL": all, "LC_MESSAGES": messages, "LANG": lang} {
		t.Setenv(name, "")
		if value == nil {
			unsetenv(t, name)
		} else {
			t.Setenv(name, *value)
		}
	}
}

// With no data for the tag or the platform's locale ICU formats in its root locale: numbers as en-US, times by the hour cycle of the
// platform locale's region; a variable set to the empty string is the locale `und`. (RevChordIntl P3-3, measured against Node 24.22.1:
// LANG=xx_YY prints 14:05, LANG=xx_US 2:05 PM, LC_ALL= prints 02:05 PM.)
func TestRootLocaleIsWhatIcuFormatsInWhenNothingHasData(t *testing.T) {
	at := time.Date(2026, time.November, 3, 14, 5, 7, 0, time.Local)
	cases := []struct {
		name            string
		all, msgs, lang *string
		time, medium    string
	}{
		{name: "an unknown language and region", lang: new("xx_YY.UTF-8"), time: "14:05", medium: "14:05:07"},
		{name: "an unknown language", lang: new("xx"), time: "14:05", medium: "14:05:07"},
		{name: "an unknown language in a region that counts to 12", lang: new("xx_US"), time: "2:05 PM", medium: "2:05:07 PM"},
		{name: "und with that region", lang: new("und_US"), time: "2:05 PM", medium: "2:05:07 PM"},
		{name: "a variable set to the empty string", all: new(""), lang: new("de_DE.UTF-8"), time: "02:05 PM", medium: "02:05:07 PM"},
		{name: "LANG set to the empty string", lang: new(""), time: "02:05 PM", medium: "02:05:07 PM"},
		{name: "LC_MESSAGES set to the empty string", msgs: new(""), lang: new("de_DE"), time: "02:05 PM", medium: "02:05:07 PM"},
		{name: "C is en-US, not root", all: new("C.UTF-8"), lang: new("de_DE"), time: "2:05 PM", medium: "2:05:07 PM"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			platform(t, c.all, c.msgs, c.lang)
			if got, err := FormatTime(nil, at, nil); err != nil || got != c.time {
				t.Errorf("FormatTime(nil) = %q, %v; want %q", got, err, c.time)
			}
			if got, err := FormatTime(nil, at, &TimeOptions{TimeStyle: TimeStyleMedium}); err != nil || got != c.medium {
				t.Errorf("FormatTime(nil, medium) = %q, %v; want %q", got, err, c.medium)
			}
			// an unavailable tag falls back to the same default
			if got, err := FormatTime(&Language{Locale: "qaa"}, at, nil); err != nil || got != c.time {
				t.Errorf("FormatTime(qaa) = %q, %v; want %q", got, err, c.time)
			}
			if got, err := FormatNumber(nil, -1234567.5, nil); err != nil || got != "-1,234,567.5" {
				t.Errorf("FormatNumber(nil) = %q, %v; want -1,234,567.5", got, err)
			}
		})
	}
}

// A tag with no data takes the platform's locale when that has data (Intl's default locale), not the root or en-US.
func TestAnUnavailableTagTakesThePlatformLocaleWhenItHasData(t *testing.T) {
	platform(t, nil, nil, new("de_DE.UTF-8"))
	at := time.Date(2026, time.November, 3, 14, 5, 7, 0, time.Local)
	if got, _ := FormatTime(&Language{Locale: "qaa"}, at, nil); got != "14:05" {
		t.Errorf("FormatTime(qaa) under de_DE = %q, want 14:05", got)
	}
	if got, _ := FormatNumber(&Language{Locale: "qaa"}, 1234.5, nil); got != "1.234,5" {
		t.Errorf("FormatNumber(qaa) under de_DE = %q, want 1.234,5", got)
	}
	// no platform locale at all: en-US
	platform(t, nil, nil, nil)
	if got, _ := FormatTime(&Language{Locale: "qaa"}, at, nil); got != "2:05 PM" {
		t.Errorf("FormatTime(qaa) with no platform locale = %q, want 2:05 PM", got)
	}
}

// RevChordIntl P3-4: CLDR's compound language aliases. Intl canonicalizes `hy-arevmda` to `hyw` (no data: the default locale
// formats), `zh-hakka` to `hak` and `zh-xiang` to `hsn`; truncating to the base language would format in Armenian or Chinese.
func TestCompoundLegacyAliasesAreCanonicalizedBeforeTheLookup(t *testing.T) {
	platform(t, nil, nil, new("de_DE.UTF-8"))
	at := time.Date(2026, time.November, 3, 14, 5, 7, 0, time.Local)
	for _, tag := range []string{"hy-arevmda", "hy-AM-arevmda", "zh-hakka", "zh-xiang"} {
		if got, err := FormatNumber(&Language{Locale: tag}, 1234.5, nil); err != nil || got != "1.234,5" {
			t.Errorf("FormatNumber(%s) = %q, %v; want the platform's 1.234,5", tag, got, err)
		}
		if got, err := FormatTime(&Language{Locale: tag}, at, nil); err != nil || got != "14:05" {
			t.Errorf("FormatTime(%s) = %q, %v; want 14:05", tag, got, err)
		}
	}
	// a variant that no alias names is kept: `de-1996` is German
	if got, _ := FormatNumber(&Language{Locale: "de-1996"}, 1234.5, nil); got != "1.234,5" {
		t.Errorf("FormatNumber(de-1996) = %q", got)
	}
}

// RevChordIntl P3-7 (mutant M13): a region alias moves the tag to another locale. `en-UK` is `en-GB` (24-hour), not `en` (en-US).
func TestRegionAliasesAreAppliedBeforeTheLookup(t *testing.T) {
	platform(t, nil, nil, nil)
	at := time.Date(2026, time.November, 3, 14, 5, 7, 0, time.Local)
	if got, err := FormatTime(&Language{Locale: "en-UK"}, at, nil); err != nil || got != "14:05" {
		t.Errorf("FormatTime(en-UK) = %q, %v; want en-GB's 14:05", got, err)
	}
}

// RevChordIntl P3-7 (mutant M8): the zone is the process's whatever location the instant carries, as a Date's is.
func TestFormatTimeShowsTheInstantInTheProcessZone(t *testing.T) {
	platform(t, nil, nil, nil)
	saved := time.Local
	time.Local = time.FixedZone("EST", -5*3600)
	t.Cleanup(func() { time.Local = saved })
	at := time.Date(2026, time.January, 1, 19, 5, 7, 0, time.UTC)
	if got, err := FormatTime(&Language{Locale: "en-US"}, at, nil); err != nil || got != "2:05 PM" {
		t.Errorf("FormatTime(UTC 19:05) in EST = %q, %v; want 2:05 PM", got, err)
	}
	other := at.In(time.FixedZone("far", 9*3600))
	if got, _ := FormatTime(&Language{Locale: "en-US"}, other, nil); got != "2:05 PM" {
		t.Errorf("an instant carrying another zone = %q, want 2:05 PM (the process zone)", got)
	}
}

// RevChordIntl P3-6: D466's claim. A canonical tag allocates the result string only; a non-canonical tag or the platform default
// allocates a few times to build the key (bounded here so a change that makes it worse fails).
func TestFormatsAllocateOnlyTheResultForCanonicalTags(t *testing.T) {
	platform(t, nil, nil, nil)
	at := time.Date(2026, time.November, 3, 14, 5, 7, 0, time.Local)
	for _, locale := range []string{"de-CH", "zh-Hant-TW", "en"} {
		lang := &Language{Locale: locale}
		if n := testing.AllocsPerRun(100, func() { _, _ = FormatNumber(lang, 1234567.891, nil) }); n > 1 {
			t.Errorf("FormatNumber(%s) allocates %v times, want 1", locale, n)
		}
		if n := testing.AllocsPerRun(100, func() { _, _ = FormatTime(lang, at, nil) }); n > 1 {
			t.Errorf("FormatTime(%s) allocates %v times, want 1", locale, n)
		}
	}
	for _, locale := range []string{"EN-us", "sh-RS", "de-DE-1996", "iw"} {
		lang := &Language{Locale: locale}
		if n := testing.AllocsPerRun(100, func() { _, _ = FormatNumber(lang, 1234567.891, nil) }); n > 6 {
			t.Errorf("FormatNumber(%s) allocates %v times, want at most 6", locale, n)
		}
	}
	platform(t, nil, nil, new("de_DE.UTF-8"))
	if n := testing.AllocsPerRun(100, func() { _, _ = FormatNumber(nil, 1234567.891, nil) }); n > 6 {
		t.Errorf("FormatNumber(nil) allocates %v times, want at most 6", n)
	}
}
