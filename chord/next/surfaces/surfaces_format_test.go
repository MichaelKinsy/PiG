package surfaces

import (
	"testing"
)

// pi: packages/chord/test/surfaces/format.test.ts
// `formatTime`'s cases and "are collected with the language" (a WeakMap of formatters; Go makes a printer per call) are
// recorded in rows/chord-ui.md: formatTime is pending, the formatter cache designed out.

func TestFormatsNumbersInTheRendersLocale(t *testing.T) {
	de, enUS := newLanguage(t, "de"), newLanguage(t, "en-US")
	if got := FormatNumber(de, 1234.5, nil); got != "1.234,5" {
		t.Errorf("de = %q", got)
	}
	if got := FormatNumber(enUS, 1234.5, nil); got != "1,234.5" {
		t.Errorf("en-US = %q", got)
	}
}

func TestFormatsNumbersInThePlatformsDefaultLocaleOutsideARender(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	if got := FormatNumber(nil, 1234.5, nil); got != "1.234,5" {
		t.Errorf("default = %q", got)
	}
}
