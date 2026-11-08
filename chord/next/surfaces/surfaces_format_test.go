package surfaces

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// pi: packages/chord/test/surfaces/format.test.ts
// "are collected with the language" (a WeakMap of formatters; Go formats from tables and keeps no per-language state)
// is recorded in rows/chord-ui.md. The expected strings below are what the pinned Node prints (the review's probe,
// rv-chordui/node-numbers.jsonl, and the oracle's formatToParts); intl_drift_test.go checks the same against a live Node.

func must(t testing.TB, s string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func number(t testing.TB, locale string, value float64, options *NumberOptions) string {
	t.Helper()
	s, err := FormatNumber(newLanguage(t, locale), value, options)
	return must(t, s, err)
}

func clock(t testing.TB, locale string, at time.Time, options *TimeOptions) string {
	t.Helper()
	s, err := FormatTime(newLanguage(t, locale), at, options)
	return must(t, s, err)
}

func local(hour, minute int) time.Time {
	return time.Date(2026, time.November, 3, hour, minute, 0, 0, time.Local)
}

func TestFormatsNumbersInTheRendersLocale(t *testing.T) {
	if got := number(t, "de", 1234.5, nil); got != "1.234,5" {
		t.Errorf("de = %q", got)
	}
	if got := number(t, "en-US", 1234.5, nil); got != "1,234.5" {
		t.Errorf("en-US = %q", got)
	}
}

func TestFormatsTimesInTheRendersLocale(t *testing.T) {
	numeric := &TimeOptions{Hour: FieldNumeric}
	for _, c := range []struct {
		locale string
		at     time.Time
		opts   *TimeOptions
		want   string
	}{
		{"de", local(14, 5), nil, "14:05"},
		{"en-US", local(14, 5), nil, "2:05 PM"},
		{"de", local(14, 5), numeric, "14 Uhr"},
		{"de", local(9, 5), numeric, "09 Uhr"},
		{"fr-CA", local(14, 5), nil, "14 h 05"},
		{"fi", local(14, 5), nil, "14.05"},
		{"ko", local(14, 5), nil, "오후 2:05"},
		{"ar", local(14, 5), nil, "2:05 م"},
		{"ar-EG", local(14, 5), nil, "٢:٠٥ م"},
		{"fa", local(14, 5), nil, "۱۴:۰۵"},
		{"bn", local(14, 5), nil, "২:০৫ PM"},
		{"my", local(14, 5), nil, "၁၄:၀၅"},
		{"en-US", local(0, 5), nil, "12:05 AM"},
	} {
		if got := clock(t, c.locale, c.at, c.opts); got != c.want {
			t.Errorf("%s %v %+v = %q, want %q", c.locale, c.at.Format("15:04"), c.opts, got, c.want)
		}
	}
}

// The review's P1-3 table: where x/text (CLDR 32, half-even) differed from the pinned Node with default options.
func TestFormatsNumbersLikeTheOracle(t *testing.T) {
	max0 := 0
	for _, c := range []struct {
		locale string
		value  float64
		opts   *NumberOptions
		want   string
	}{
		{"es", 1234.5, nil, "1234,5"},
		{"es", 1000, nil, "1000"},
		{"it", 1234.5, nil, "1234,5"},
		{"pl", 1234.5, nil, "1234,5"},
		{"fr", 1234.5, nil, "1\u202f234,5"},
		{"fr-CH", 1234.5, nil, "1'234,5"},
		{"de-CH", 1234.5, nil, "1'234.5"},
		{"ar", 1234.5, nil, "1,234.5"},
		{"ar-EG", 1234.5, nil, "١٬٢٣٤٫٥"},
		{"en", 1.0625, nil, "1.063"},
		{"en", 1234.5, &NumberOptions{MaximumFractionDigits: &max0}, "1,235"},
		{"en", -0.0004, nil, "-0"},
		{"en", math.Copysign(0, -1), nil, "-0"},
		{"en", math.NaN(), nil, "NaN"},
		{"en", math.Inf(-1), nil, "-∞"},
		{"hi", 1234567.891, nil, "12,34,567.891"},
		{"en", 1e21, nil, "1,000,000,000,000,000,000,000"},
	} {
		if got := number(t, c.locale, c.value, c.opts); got != c.want {
			t.Errorf("%s %v = %q, want %q", c.locale, c.value, got, c.want)
		}
	}
}

func TestFormatNumberFractionDigits(t *testing.T) {
	two, six := 2, 6
	if got := number(t, "en", 1.5, &NumberOptions{MinimumFractionDigits: &two}); got != "1.50" {
		t.Errorf("min 2 = %q", got)
	}
	// only a minimum above the default maximum raises the maximum
	if got := number(t, "en", 1.23456789, &NumberOptions{MinimumFractionDigits: &six}); got != "1.234568" {
		t.Errorf("min 6 = %q", got)
	}
	if _, err := FormatNumber(nil, 1, &NumberOptions{MinimumFractionDigits: &six, MaximumFractionDigits: &two}); !errors.Is(err, ErrOption) {
		t.Errorf("min > max = %v, want ErrOption (Intl throws a RangeError)", err)
	}
	over := 101
	if _, err := FormatNumber(nil, 1, &NumberOptions{MaximumFractionDigits: &over}); !errors.Is(err, ErrOption) {
		t.Errorf("max 101 = %v, want ErrOption", err)
	}
}

func TestFormatTimeRefusesOptionsOutsideTheClosedType(t *testing.T) {
	yes := true
	for name, opts := range map[string]TimeOptions{
		"empty (Intl would print a date)": {},
		"style with fields":               {TimeStyle: TimeStyleShort, Hour: FieldNumeric},
		"unsupported style":               {TimeStyle: "long"},
		"unknown field style":             {Hour: "narrow"},
		"hour12 alone":                    {Hour12: &yes},
	} {
		if _, err := FormatTime(nil, local(1, 1), &opts); !errors.Is(err, ErrOption) {
			t.Errorf("%s = %v, want ErrOption", name, err)
		}
	}
}

func TestFormatsRefuseLocalesIntlRefuses(t *testing.T) {
	for _, locale := range []string{"", "en_US", "i-klingon", "x-private", "en-GB-oed", "en-US-", "en--US", "zh-min-nan"} {
		lang := &Language{Locale: locale}
		if _, err := FormatNumber(lang, 1, nil); !errors.Is(err, ErrLocale) {
			t.Errorf("%q: FormatNumber err = %v, want ErrLocale", locale, err)
		}
		if _, err := FormatTime(lang, local(1, 1), nil); !errors.Is(err, ErrLocale) {
			t.Errorf("%q: FormatTime err = %v, want ErrLocale", locale, err)
		}
	}
	if _, err := FormatNumber(&Language{Locale: "ar-u-nu-latn"}, 1, nil); !errors.Is(err, ErrLocale) {
		t.Errorf("unicode extension = %v, want ErrLocale", err)
	}
}

func TestFormatsNumbersInThePlatformsDefaultLocaleOutsideARender(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	if got := mustNumber(FormatNumber(nil, 1234.5, nil)); got != "1.234,5" {
		t.Errorf("default = %q", got)
	}
	if got, err := FormatTime(nil, local(14, 5), nil); err != nil || got != "14:05" {
		t.Errorf("default time = %q, %v", got, err)
	}
}

func mustNumber(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}

func TestUnavailableLocaleFallsBackToThePlatformDefault(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	if got := mustNumber(FormatNumber(&Language{Locale: "xx"}, 1234.5, nil)); got != "1.234,5" {
		t.Errorf("xx = %q, want the default locale's", got)
	}
}

// The review's differential probe (P1-3): 26 locales x 13 values x 4 option sets recorded from the pinned Node (v24.22.1,
// ICU 78.3), where x/text differed in 312 outputs. testdata/review-probe-node-numbers.jsonl is Node's side verbatim:
// [locale, option set, value, output].
func TestFormatsNumbersLikeTheReviewsProbe(t *testing.T) {
	data, err := os.ReadFile("testdata/review-probe-node-numbers.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	set := func(v int) *int { return &v }
	options := map[string]*NumberOptions{
		"none":     nil,
		"max0":     {MaximumFractionDigits: set(0)},
		"min2":     {MinimumFractionDigits: set(2)},
		"min1max1": {MinimumFractionDigits: set(1), MaximumFractionDigits: set(1)},
	}
	rows, diffs := 0, 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var row [4]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		locale, name, want := row[0].(string), row[1].(string), row[3].(string)
		var value float64
		switch v := row[2].(type) {
		case float64:
			value = v
		case string:
			value, _ = strconv.ParseFloat(v, 64)
			if v == "-0" {
				value = math.Copysign(0, -1)
			}
		}
		lang := &Language{Locale: locale}
		if name == "special" {
			if want == "-0" {
				value = math.Copysign(0, -1) // the probe wrote String(-0), which is "0"
			}
			locale = "en"
			lang = &Language{Locale: "en"}
			name = "none"
		}
		got, err := FormatNumber(lang, value, options[name])
		rows++
		if err != nil || got != want {
			diffs++
			t.Errorf("%s %s %v: %q (%v), node %q", locale, name, value, got, err, want)
		}
	}
	t.Logf("%d rows, %d diffs", rows, diffs)
}
