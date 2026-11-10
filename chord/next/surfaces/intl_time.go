package surfaces

import (
	"fmt"
	"slices"
	"time"
	"unicode/utf8"
)

// timePattern is one locale's pattern for one option shape, as the pinned Node's `Intl.DateTimeFormat` prints it
// (generated: intl_gen.go). tmpl's bytes 1..7 are fields, everything else is literal text: 1/2 the hour (bare/padded to
// two digits), 3/4 the minute, 5/6 the second, 7 the day period. The day period is a function of the minute of the day:
// am and pm, or a locale's flexible periods; periods are sorted and start at minute 0.
type timePattern struct {
	cycle   uint8 // hourCycleNone, h11, h12, h23, h24
	tmpl    string
	periods []dayPeriod
}

// dayPeriod is the text printed from minute `start` of the day until the next period's start.
type dayPeriod struct {
	start uint16
	text  string
}

func (p *timePattern) periodAt(minuteOfDay int) string {
	text := ""
	for _, d := range p.periods {
		if int(d.start) > minuteOfDay {
			break
		}
		text = d.text
	}
	return text
}

const (
	hourCycleNone uint8 = iota
	hourCycleH11
	hourCycleH12
	hourCycleH23
	hourCycleH24
)

// timeLocale is a locale's row of patterns (one per valid option shape) and the numbering system's digit zero.
// regionRow is the root time row of a region (generated).
type regionRow struct {
	region string
	row    uint16
}

type timeLocale struct {
	row  uint16
	zero rune
}

// TimeStyle is `Intl.DateTimeFormatOptions.timeStyle`, the two that need no time zone name.
type TimeStyle string

const (
	TimeStyleShort  TimeStyle = "short"  // `14:05`
	TimeStyleMedium TimeStyle = "medium" // `14:05:07`
)

// FieldStyle is how a time field prints in a TimeOptions: `numeric` or `2-digit`.
type FieldStyle string

const (
	FieldNumeric  FieldStyle = "numeric"
	FieldTwoDigit FieldStyle = "2-digit"
)

// TimeOptions are the `Intl.DateTimeFormatOptions` FormatTime honors: the ones Pi's call sites pass (none, which is
// `{timeStyle: "short"}`, and `{hour: "numeric"}`) and their siblings over the time fields. Exactly one of TimeStyle
// and at least one field must be set, as Intl has it (a style with fields is a TypeError there, neither prints a date).
// Everything else (time zone, zone names, calendars, eras, fractional seconds, day periods, hourCycle) is outside the
// type: it would need ICU's pattern generator and zone data, not a table (D in rows/chord-ui.md).
type TimeOptions struct {
	TimeStyle            TimeStyle
	Hour, Minute, Second FieldStyle
	Hour12               *bool
}

func fieldDigit(f FieldStyle) (int, error) {
	switch f {
	case "":
		return 0, nil
	case FieldNumeric:
		return 1, nil
	case FieldTwoDigit:
		return 2, nil
	}
	return 0, fmt.Errorf("%w: %q is not a field style", ErrOption, string(f))
}

// slot is the option shape's number, a mixed radix over style, hour, minute, second and hour12 (the generator's order).
func (o TimeOptions) slot() (int, error) {
	var style int
	switch o.TimeStyle {
	case "":
	case TimeStyleShort:
		style = 1
	case TimeStyleMedium:
		style = 2
	default:
		return 0, fmt.Errorf("%w: timeStyle %q is outside the supported set (short, medium)", ErrOption, string(o.TimeStyle))
	}
	var fields [3]int
	for i, f := range [...]FieldStyle{o.Hour, o.Minute, o.Second} {
		d, err := fieldDigit(f)
		if err != nil {
			return 0, err
		}
		fields[i] = d
	}
	any := fields[0]+fields[1]+fields[2] > 0
	switch {
	case style > 0 && any:
		return 0, fmt.Errorf("%w: timeStyle cannot be combined with hour, minute or second", ErrOption)
	case style == 0 && !any:
		return 0, fmt.Errorf("%w: a time format needs a timeStyle or one of hour, minute, second", ErrOption)
	}
	h12 := 0
	if o.Hour12 != nil {
		h12 = 1
		if !*o.Hour12 {
			h12 = 2
		}
	}
	return (((style*3+fields[0])*3+fields[1])*3+fields[2])*3 + h12, nil
}

// FormatTime is at's local time in lang's locale: `14:05` in German, `2:05 PM` in en-US with the default options (Pi's
// `{timeStyle: "short"}`). nil options is that default; lang nil is the platform's default locale. The zone is the
// process's (time.Local), as a `Date`'s is in Node.
func FormatTime(lang *Language, at time.Time, options *TimeOptions) (string, error) {
	return formatClock(lang, at.Local(), options)
}

// formatClock is FormatTime with the zone already chosen: the clock it prints is at's own in at's location. FormatTime
// passes the process's; a test that compares against Node's fixed zone passes UTC without rewriting the process-global time.Local.
func formatClock(lang *Language, at time.Time, options *TimeOptions) (string, error) {
	// pig divergence (D464): a closed option type over generated tables, not Intl's open options.
	// pig divergence (D465): a time.Time for Pi's `Date | number | string`.
	opts := TimeOptions{TimeStyle: TimeStyleShort}
	if options != nil {
		opts = *options
	}
	slot, err := opts.slot()
	if err != nil {
		return "", err
	}
	var tag string
	if lang != nil {
		tag = lang.Locale
	} else {
		tag = platformLocale()
	}
	li, err := resolveLocale(tag, intlTimeLocales[:], intlTimeExceptions[:])
	if err != nil {
		return "", err
	}
	column, found := slices.BinarySearch(intlTimeShapeSlots[:], uint8(slot))
	if !found {
		return "", fmt.Errorf("%w: no table for these options", ErrOption)
	}
	// ICU's root locale (no data for the tag or the platform's locale): the row of the platform locale's region, digits 0-9.
	locale := timeLocale{row: rootTimeRow(), zero: '0'}
	if li >= 0 {
		locale = intlTimeLocaleData[li]
	}
	pattern := &intlTimePatterns[intlTimeRows[locale.row][column]]
	hour, minute, second := at.Clock()
	var buf [64]byte
	return string(pattern.appendTime(buf[:0], locale.zero, hour, minute, second)), nil
}

func (p *timePattern) appendTime(dst []byte, zero rune, hour, minute, second int) []byte {
	shown := hour
	switch p.cycle {
	case hourCycleH12:
		if shown = hour % 12; shown == 0 {
			shown = 12
		}
	case hourCycleH11:
		shown = hour % 12
	case hourCycleH24:
		if hour == 0 {
			shown = 24
		}
	}
	number := func(dst []byte, n int, padded bool) []byte {
		if n >= 10 || padded {
			dst = utf8.AppendRune(dst, zero+rune(n/10))
		}
		return utf8.AppendRune(dst, zero+rune(n%10))
	}
	for i := 0; i < len(p.tmpl); i++ {
		switch c := p.tmpl[i]; c {
		case 1, 2:
			dst = number(dst, shown, c == 2)
		case 3, 4:
			dst = number(dst, minute, c == 4)
		case 5, 6:
			dst = number(dst, second, c == 6)
		case 7:
			dst = append(dst, p.periodAt(hour*60+minute)...)
		default:
			dst = append(dst, c)
		}
	}
	return dst
}
