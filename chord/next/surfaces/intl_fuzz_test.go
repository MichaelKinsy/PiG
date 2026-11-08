package surfaces

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// referenceNumber is an independent en-US rendering of Intl's algorithm for the value→string path: the shortest
// decimal of the double (strconv's 'f' form), rounded half away from zero on its decimal digits, grouped by three.
func referenceNumber(value float64, minFD, maxFD int) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	sign := ""
	if value < 0 || value == 0 && math.Signbit(value) {
		sign = "-"
	}
	if math.IsInf(value, 0) {
		return sign + "∞"
	}
	text := strconv.FormatFloat(math.Abs(value), 'f', -1, 64)
	integer, fraction, _ := strings.Cut(text, ".")
	if len(fraction) > maxFD {
		roundUp := fraction[maxFD] >= '5'
		fraction = fraction[:maxFD]
		if roundUp {
			digits := []byte(integer + fraction)
			i := len(digits) - 1
			for i >= 0 && digits[i] == '9' {
				digits[i] = '0'
				i--
			}
			if i < 0 {
				digits = append([]byte{'1'}, digits...)
			} else {
				digits[i]++
			}
			integer, fraction = string(digits[:len(digits)-len(fraction)]), string(digits[len(digits)-len(fraction):])
		}
	}
	fraction = strings.TrimRight(fraction, "0")
	for len(fraction) < minFD {
		fraction += "0"
	}
	var grouped strings.Builder
	for i, d := range integer {
		if i > 0 && (len(integer)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(d)
	}
	if fraction != "" {
		return sign + grouped.String() + "." + fraction
	}
	return sign + grouped.String()
}

func FuzzFormatNumber(f *testing.F) {
	for _, v := range []float64{0, math.Copysign(0, -1), 1.0625, 1234.5, -2.5, 999.9995, 1e21, 5e-324, math.MaxFloat64, math.NaN(), math.Inf(1), 0.0005, 4503599627370497.5} {
		f.Add(v, uint8(0), uint8(3), uint16(0))
	}
	f.Fuzz(func(t *testing.T, value float64, minFD, maxFD uint8, localeIndex uint16) {
		lo, hi := int(minFD%12), int(maxFD%12)
		if lo > hi {
			lo, hi = hi, lo
		}
		opts := &NumberOptions{MinimumFractionDigits: &lo, MaximumFractionDigits: &hi}
		got, err := FormatNumber(&Language{Locale: "en-US"}, value, opts)
		if err != nil {
			t.Fatal(err)
		}
		if want := referenceNumber(value, lo, hi); got != want {
			t.Fatalf("en-US %v (%d..%d): %q, reference %q", value, lo, hi, got, want)
		}
		// any locale: no panic, valid UTF-8, the same digits as en-US (a locale only changes symbols and digit forms)
		locale := intlNumberLocales[int(localeIndex)%len(intlNumberLocales)]
		other, err := FormatNumber(&Language{Locale: locale}, value, opts)
		if err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		if !utf8.ValidString(other) {
			t.Fatalf("%s: %q is not UTF-8", locale, other)
		}
		format, _ := numberFormatOf(locale)
		digits := 0
		for _, r := range other {
			if r >= format.zero && r < format.zero+10 {
				digits++
			}
		}
		wantDigits := 0
		for _, r := range got {
			if r >= '0' && r <= '9' {
				wantDigits++
			}
		}
		if !math.IsNaN(value) && !math.IsInf(value, 0) && digits != wantDigits {
			t.Fatalf("%s %v: %d digits in %q, en-US has %d in %q", locale, value, digits, other, wantDigits, got)
		}
	})
}

func FuzzFormatTime(f *testing.F) {
	f.Add(int64(0), uint8(0), uint16(0))
	f.Add(int64(1_700_000_000), uint8(9), uint16(5))
	f.Fuzz(func(t *testing.T, seconds int64, shape uint8, localeIndex uint16) {
		styles := []TimeStyle{"", TimeStyleShort, TimeStyleMedium}
		fields := []FieldStyle{"", FieldNumeric, FieldTwoDigit}
		yes := shape%2 == 0
		o := TimeOptions{TimeStyle: styles[int(shape)%3], Hour: fields[int(shape/3)%3], Minute: fields[int(shape/9)%3], Second: fields[int(shape/27)%3]}
		if shape%5 < 2 {
			o.Hour12 = &yes
		}
		locale := intlTimeLocales[int(localeIndex)%len(intlTimeLocales)]
		got, err := FormatTime(&Language{Locale: locale}, time.Unix(seconds%4_000_000_000, 0), &o)
		if err != nil {
			if _, slotErr := o.slot(); slotErr == nil {
				t.Fatalf("%s %+v: %v", locale, o, err)
			}
			return
		}
		if got == "" || !utf8.ValidString(got) {
			t.Fatalf("%s %+v: %q", locale, o, got)
		}
	})
}

func benchmarkLanguage(b *testing.B, locale string) *Language {
	b.Helper()
	return newLanguage(b, locale)
}

func BenchmarkFormatNumber(b *testing.B) {
	for _, locale := range []string{"en-US", "de", "hi", "ar-EG"} {
		lang := benchmarkLanguage(b, locale)
		b.Run(locale, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := FormatNumber(lang, 1234567.891, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFormatTime(b *testing.B) {
	at := time.Date(2026, 11, 3, 14, 5, 0, 0, time.Local)
	for _, locale := range []string{"en-US", "de", "ar-EG"} {
		lang := benchmarkLanguage(b, locale)
		b.Run(locale, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := FormatTime(lang, at, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
