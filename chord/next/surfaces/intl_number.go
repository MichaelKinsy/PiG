package surfaces

import (
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// numberFormat is what the pinned Node's `Intl.NumberFormat` prints for one locale (generated: intl_gen.go).
type numberFormat struct {
	zero                                       rune // the numbering system's digit zero
	decimal, group, nan, inf                   string
	posPrefix, posSuffix, negPrefix, negSuffix string
	primary, secondary, minGroup               uint8 // group sizes (0: no grouping); digits needed above the primary group
}

// numberFormatOf is the locale tag's format.
func numberFormatOf(tag string) (*numberFormat, error) {
	i, err := resolveLocale(tag, intlNumberLocales[:], intlNumberExceptions[:])
	if err != nil {
		return nil, err
	}
	if i < 0 {
		// ICU's root locale: its numbers are en-US's (the generator checks it against Node's default locale under LANG=xx_YY and the empty
		// platform locale).
		i, _ = slices.BinarySearch(intlNumberLocales[:], "en-US")
	}
	return &intlNumberFormats[intlNumberLocaleFormat[i]], nil
}

// maxFractionDigitsLimit is `Intl`'s limit for fraction digits.
const maxFractionDigitsLimit = 100

// appendNumber appends value as Intl.NumberFormat prints it with minFD to maxFD fraction digits: the shortest decimal
// of the double rounded half away from zero (ICU's halfExpand), digit-grouped, in the locale's digits and affixes. A
// negative value (including -0 and one that rounds to 0) takes the negative affixes.
func (f *numberFormat) appendNumber(dst []byte, value float64, minFD, maxFD int) []byte {
	negative := value < 0 || value == 0 && 1/value < 0
	if value != value {
		return append(dst, f.nan...)
	}
	prefix, suffix := f.posPrefix, f.posSuffix
	if negative {
		prefix, suffix = f.negPrefix, f.negSuffix
	}
	dst = append(dst, prefix...)
	if value > 1.7976931348623157e308 || value < -1.7976931348623157e308 {
		return append(append(dst, f.inf...), suffix...)
	}
	var scratch [48]byte
	var work [1 + 330]byte
	digits, point := shortestDigits(scratch[:0], value)
	// work[1:] holds the integer digits then the fraction digits; work[0] is room for a carry.
	w := work[:1]
	intLen := 1
	switch {
	case len(digits) == 0:
		w = append(w, '0')
	case point >= len(digits):
		w = append(w, digits...)
		for range point - len(digits) {
			w = append(w, '0')
		}
		intLen = point
	case point > 0:
		w = append(w, digits[:point]...)
		intLen = point
	default:
		w = append(w, '0')
	}
	// fraction digits, at most maxFD+1 of them (the digit after the last kept one decides the rounding)
	fracStart := len(w)
	if len(digits) > 0 && point < len(digits) {
		for i := 0; i <= maxFD; i++ {
			idx := i + point
			switch {
			case idx >= len(digits):
				i = maxFD
			case idx < 0:
				w = append(w, '0')
			default:
				w = append(w, digits[idx])
			}
		}
	}
	start := 1
	if fl := len(w) - fracStart; fl > maxFD {
		up := w[fracStart+maxFD] >= '5'
		w = w[:fracStart+maxFD]
		if up {
			i := len(w) - 1
			for i >= 1 && w[i] == '9' {
				w[i] = '0'
				i--
			}
			if i < 1 {
				w[0] = '1'
				start = 0
				intLen++
			} else {
				w[i]++
			}
		}
	}
	frac := w[fracStart:]
	for len(frac) > minFD && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	integer := w[start : start+intLen]
	if f.primary > 0 && intLen >= int(f.primary)+int(f.minGroup) {
		for i, d := range integer {
			if i > 0 {
				if rest := intLen - i; rest == int(f.primary) || rest > int(f.primary) && (rest-int(f.primary))%int(f.secondary) == 0 {
					dst = append(dst, f.group...)
				}
			}
			dst = f.appendDigit(dst, d)
		}
	} else {
		for _, d := range integer {
			dst = f.appendDigit(dst, d)
		}
	}
	if len(frac) > 0 || minFD > 0 {
		dst = append(dst, f.decimal...)
		for _, d := range frac {
			dst = f.appendDigit(dst, d)
		}
		for i := len(frac); i < minFD; i++ {
			dst = f.appendDigit(dst, '0')
		}
	}
	return append(dst, suffix...)
}

func (f *numberFormat) appendDigit(dst []byte, d byte) []byte {
	if f.zero == '0' {
		return append(dst, d)
	}
	return utf8.AppendRune(dst, f.zero+rune(d-'0'))
}

// shortestDigits is the shortest decimal digits that round-trip |value| (none for 0) and the decimal point's position:
// |value| = 0.DIGITS × 10^point.
func shortestDigits(scratch []byte, value float64) ([]byte, int) {
	if value == 0 {
		return nil, 0
	}
	if value < 0 {
		value = -value
	}
	e := strconv.AppendFloat(scratch, value, 'e', -1, 64) // d.ddde±xx
	var digits [24]byte
	n := 0
	exp := 0
	for i := 0; i < len(e); i++ {
		switch c := e[i]; c {
		case '.':
		case 'e':
			exp, _ = strconv.Atoi(string(e[i+1:]))
			i = len(e)
		default:
			digits[n] = c
			n++
		}
	}
	for n > 0 && digits[n-1] == '0' {
		n--
	}
	return append(scratch[:0], digits[:n]...), exp + 1
}

// NumberOptions are the `Intl.NumberFormatOptions` FormatNumber honors: the fraction digits (0 to 100). Nil fields keep
// Intl's defaults: at least 0, at most 3 (or the minimum when that is larger). The type is closed on purpose: Pi's call
// sites pass no other option, and an option the generated tables do not cover cannot be expressed (D in
// rows/chord-ui.md).
type NumberOptions struct {
	MinimumFractionDigits *int
	MaximumFractionDigits *int
}

// ErrOption is wrapped by the errors of options `Intl` throws a RangeError for or that the closed option types do not
// express.
var ErrOption = errOption("surfaces: invalid option")

type errOption string

func (e errOption) Error() string { return string(e) }

func (o *NumberOptions) fractionDigits() (minFD, maxFD int, err error) {
	minFD, maxFD = 0, 3
	if o == nil {
		return minFD, maxFD, nil
	}
	checked := func(name string, p *int) (int, bool, error) {
		if p == nil {
			return 0, false, nil
		}
		if *p < 0 || *p > maxFractionDigitsLimit {
			return 0, false, fmt.Errorf("%w: %s must be between 0 and %d, got %d", ErrOption, name, maxFractionDigitsLimit, *p)
		}
		return *p, true, nil
	}
	lo, hasMin, err := checked("minimumFractionDigits", o.MinimumFractionDigits)
	if err != nil {
		return 0, 0, err
	}
	hi, hasMax, err := checked("maximumFractionDigits", o.MaximumFractionDigits)
	if err != nil {
		return 0, 0, err
	}
	switch {
	case hasMin && hasMax:
		if lo > hi {
			return 0, 0, fmt.Errorf("%w: minimumFractionDigits %d is greater than maximumFractionDigits %d", ErrOption, lo, hi)
		}
		minFD, maxFD = lo, hi
	case hasMin:
		minFD, maxFD = lo, max(lo, 3)
	case hasMax:
		minFD, maxFD = 0, hi
	}
	return minFD, maxFD, nil
}
