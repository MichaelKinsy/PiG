package ai

// Ports V8's DateParser (src/date/dateparser.cc, dateparser-inl.h) and the Date.parse wrapper (src/date/date.cc ParseDateTimeString), which Node runs for every `Date.parse(string)`.

import (
	"math"
	"time"
)

const (
	dateNone = math.MinInt32
	// dateSignificantDigits is the number of digits V8's DateParser reads from a number token (DateParser::kMaxSignificantDigits); later digits are skipped.
	dateSignificantDigits = 9
	// timeClipMs is ECMAScript's TimeClip range (ECMA-262 21.4.1.31, 100,000,000 days in milliseconds) that V8's MakeDate applies. Both constants belong to the JavaScript Date.parse that Node runs, not to PiG or Pi.
	timeClipMs          = 8.64e15
	timeClipBeforeUTCMs = timeClipMs + 864e5
)

type dateTokenKind int

const (
	dateInvalid dateTokenKind = iota
	dateUnknown
	dateNumber
	dateSymbol
	dateWhiteSpace
	dateKeyword
	dateEnd
)

type dateKeywordType int

const (
	keywordInvalid dateKeywordType = iota
	keywordMonthName
	keywordAMPM
	keywordTimeZoneName
	keywordTimeSeparator
)

type dateToken struct {
	kind   dateTokenKind
	value  int
	length int
	kw     dateKeywordType
}

func (t dateToken) isNumber() bool           { return t.kind == dateNumber }
func (t dateToken) isFixedLength(n int) bool { return t.kind == dateNumber && t.length == n }
func (t dateToken) isSymbol(c rune) bool     { return t.kind == dateSymbol && t.value == int(c) }
func (t dateToken) isAsciiSign() bool {
	return t.kind == dateSymbol && (t.value == '+' || t.value == '-')
}
func (t dateToken) asciiSign() int { return 44 - t.value }
func (t dateToken) isKeywordType(k dateKeywordType) bool {
	return t.kind == dateKeyword && t.kw == k
}
func (t dateToken) isKeywordZ() bool {
	return t.isKeywordType(keywordTimeZoneName) && t.length == 1
}

var dateKeywords = []struct {
	prefix string
	kind   dateKeywordType
	value  int
}{
	{"jan", keywordMonthName, 1}, {"feb", keywordMonthName, 2}, {"mar", keywordMonthName, 3},
	{"apr", keywordMonthName, 4}, {"may", keywordMonthName, 5}, {"jun", keywordMonthName, 6},
	{"jul", keywordMonthName, 7}, {"aug", keywordMonthName, 8}, {"sep", keywordMonthName, 9},
	{"oct", keywordMonthName, 10}, {"nov", keywordMonthName, 11}, {"dec", keywordMonthName, 12},
	{"am", keywordAMPM, 0}, {"pm", keywordAMPM, 12},
	{"ut", keywordTimeZoneName, 0}, {"utc", keywordTimeZoneName, 0}, {"z", keywordTimeZoneName, 0}, {"gmt", keywordTimeZoneName, 0},
	{"cdt", keywordTimeZoneName, -5}, {"cst", keywordTimeZoneName, -6},
	{"edt", keywordTimeZoneName, -4}, {"est", keywordTimeZoneName, -5},
	{"mdt", keywordTimeZoneName, -6}, {"mst", keywordTimeZoneName, -7},
	{"pdt", keywordTimeZoneName, -7}, {"pst", keywordTimeZoneName, -8},
	{"t", keywordTimeSeparator, 0},
}

// dateScanner is V8's InputReader plus DateStringTokenizer over the string's UTF-16 code units. A header value reaches Node as a ByteString, so each byte of the Go string is one code unit.
type dateScanner struct {
	in   []uint16
	pos  int
	peek dateToken
}

func newDateScanner(s string) *dateScanner {
	units := make([]uint16, len(s))
	for i := 0; i < len(s); i++ {
		units[i] = uint16(s[i])
	}
	sc := &dateScanner{in: units}
	sc.peek = sc.scan()
	return sc
}

func (s *dateScanner) ch() int {
	if s.pos >= len(s.in) {
		return -1
	}
	return int(s.in[s.pos])
}

// isJSWhiteSpace is V8's IsWhiteSpaceOrLineTerminator.
func isJSWhiteSpace(c int) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}

func (s *dateScanner) next() dateToken {
	t := s.peek
	s.peek = s.scan()
	return t
}

func (s *dateScanner) skipSymbol(c rune) bool {
	if s.peek.isSymbol(c) {
		s.next()
		return true
	}
	return false
}

func (s *dateScanner) scan() dateToken {
	start := s.pos
	c := s.ch()
	switch {
	case c < 0:
		return dateToken{kind: dateEnd}
	case c >= '0' && c <= '9':
		n, digits := 0, 0
		for s.ch() == '0' {
			s.pos++
		}
		for c := s.ch(); c >= '0' && c <= '9'; c = s.ch() {
			if digits < dateSignificantDigits {
				n = n*10 + c - '0'
			}
			digits++
			s.pos++
		}
		return dateToken{kind: dateNumber, value: n, length: s.pos - start}
	case c == ':' || c == '-' || c == '+' || c == '.' || c == ')':
		s.pos++
		return dateToken{kind: dateSymbol, value: c}
	case c >= 'A' && !isJSWhiteSpace(c):
		var buf [3]uint16
		length := 0
		for c := s.ch(); c >= 'A' && !isJSWhiteSpace(c); c = s.ch() {
			if length < 3 {
				u := uint16(c)
				if u >= 'A' && u <= 'Z' {
					u += 'a' - 'A'
				}
				buf[length] = u
			}
			length++
			s.pos++
		}
		for _, k := range dateKeywords {
			matched := true
			for j := range 3 {
				var want uint16
				if j < len(k.prefix) {
					want = uint16(k.prefix[j])
				}
				if buf[j] != want {
					matched = false
					break
				}
			}
			if matched && (length <= 3 || k.kind == keywordMonthName) {
				return dateToken{kind: dateKeyword, kw: k.kind, value: k.value, length: length}
			}
		}
		return dateToken{kind: dateKeyword, kw: keywordInvalid, length: length}
	case isJSWhiteSpace(c):
		for isJSWhiteSpace(s.ch()) {
			s.pos++
		}
		return dateToken{kind: dateWhiteSpace, length: s.pos - start}
	case c == '(':
		depth := 0
		for s.pos < len(s.in) {
			switch s.in[s.pos] {
			case '(':
				depth++
			case ')':
				depth--
			}
			s.pos++
			if depth == 0 {
				break
			}
		}
		return dateToken{kind: dateUnknown}
	}
	s.pos++
	return dateToken{kind: dateUnknown}
}

func readMilliseconds(t dateToken) int {
	number, length := t.value, t.length
	if length < 3 {
		switch length {
		case 1:
			number *= 100
		case 2:
			number *= 10
		}
	} else if length > 3 {
		length = min(length, dateSignificantDigits)
		factor := 1
		for {
			factor *= 10
			length--
			if length <= 3 {
				break
			}
		}
		number /= factor
	}
	return number
}

func between(x, lo, hi int) bool { return x >= lo && x <= hi }

type dayComposer struct {
	comp       [3]int
	index      int
	namedMonth int
	isISO      bool
}

func (d *dayComposer) add(n int) bool {
	if d.index < 3 {
		d.comp[d.index] = n
		d.index++
		return true
	}
	return false
}

func isMonth(x int) bool { return between(x, 1, 12) }
func isDay(x int) bool   { return between(x, 1, 31) }

func (d *dayComposer) write() (year, month, day int, ok bool) {
	if d.index < 1 {
		return 0, 0, 0, false
	}
	for d.index < 3 {
		d.comp[d.index] = 1
		d.index++
	}
	if d.namedMonth == dateNone {
		if d.isISO || (d.index == 3 && !isDay(d.comp[0])) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day = d.comp[0], d.comp[1]
			if d.index == 3 {
				year = d.comp[2]
			}
		}
	} else {
		month = d.namedMonth
		switch {
		case d.index == 1:
			day = d.comp[0]
		case !isDay(d.comp[0]):
			year, day = d.comp[0], d.comp[1]
		default:
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.isISO {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	if year < -(1<<30) || year >= 1<<30 || !isMonth(month) || !isDay(day) {
		return 0, 0, 0, false
	}
	return year, month - 1, day, true
}

type timeComposer struct {
	comp       [4]int
	index      int
	hourOffset int
}

func (t *timeComposer) add(n int) bool {
	if t.index < 4 {
		t.comp[t.index] = n
		t.index++
		return true
	}
	return false
}

func (t *timeComposer) addFinal(n int) bool {
	if !t.add(n) {
		return false
	}
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	return true
}

func isHour(x int) bool        { return between(x, 0, 23) }
func isMinute(x int) bool      { return between(x, 0, 59) }
func isSecond(x int) bool      { return between(x, 0, 59) }
func isMillisecond(x int) bool { return between(x, 0, 999) }

func (t *timeComposer) isExpecting(n int) bool {
	return t.index == 1 && isMinute(n) || t.index == 2 && isSecond(n) || t.index == 3 && isMillisecond(n)
}

func (t *timeComposer) write() (hour, minute, second, ms int, ok bool) {
	for t.index < 4 {
		t.comp[t.index] = 0
		t.index++
	}
	hour, minute, second, ms = t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dateNone {
		if !between(hour, 0, 12) {
			return 0, 0, 0, 0, false
		}
		hour %= 12
		hour += t.hourOffset
	}
	if !isHour(hour) || !isMinute(minute) || !isSecond(second) || !isMillisecond(ms) {
		if hour != 24 || minute != 0 || second != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return hour, minute, second, ms, true
}

type zoneComposer struct{ sign, hour, minute int }

func (z *zoneComposer) set(offsetHours int) {
	z.sign = 1
	if offsetHours < 0 {
		z.sign = -1
	}
	z.hour = offsetHours * z.sign
	z.minute = 0
}

func (z *zoneComposer) setSign(sign int) {
	z.sign = 1
	if sign < 0 {
		z.sign = -1
	}
}

func (z *zoneComposer) isExpecting(n int) bool {
	return z.hour != dateNone && z.minute == dateNone && isMinute(n)
}
func (z *zoneComposer) isUTC() bool   { return z.hour == 0 && z.minute == 0 }
func (z *zoneComposer) isEmpty() bool { return z.hour == dateNone }

// parseES5DateTime is DateParser::ParseES5DateTime. It returns the first token it did not consume, which the legacy loop continues from.
func parseES5DateTime(sc *dateScanner, day *dayComposer, tm *timeComposer, tz *zoneComposer) dateToken {
	invalid := dateToken{kind: dateInvalid}
	switch {
	case sc.peek.isAsciiSign():
		sign := sc.next()
		if !sc.peek.isFixedLength(6) {
			return sign
		}
		year := sc.next().value
		if sign.asciiSign() < 0 && year == 0 {
			return sign
		}
		day.add(sign.asciiSign() * year)
	case sc.peek.isFixedLength(4):
		day.add(sc.next().value)
	default:
		return sc.next()
	}
	if sc.skipSymbol('-') {
		if !sc.peek.isFixedLength(2) || !isMonth(sc.peek.value) {
			return sc.next()
		}
		day.add(sc.next().value)
		if sc.skipSymbol('-') {
			if !sc.peek.isFixedLength(2) || !isDay(sc.peek.value) {
				return sc.next()
			}
			day.add(sc.next().value)
		}
	}
	if !sc.peek.isKeywordType(keywordTimeSeparator) {
		if sc.peek.kind != dateEnd {
			return sc.next()
		}
	} else {
		sc.next()
		if !sc.peek.isFixedLength(2) || !between(sc.peek.value, 0, 24) {
			return invalid
		}
		hourIs24 := sc.peek.value == 24
		tm.add(sc.next().value)
		if !sc.skipSymbol(':') {
			return invalid
		}
		if !sc.peek.isFixedLength(2) || !isMinute(sc.peek.value) || hourIs24 && sc.peek.value > 0 {
			return invalid
		}
		tm.add(sc.next().value)
		if sc.skipSymbol(':') {
			if !sc.peek.isFixedLength(2) || !isSecond(sc.peek.value) || hourIs24 && sc.peek.value > 0 {
				return invalid
			}
			tm.add(sc.next().value)
			if sc.skipSymbol('.') {
				if !sc.peek.isNumber() || hourIs24 && sc.peek.value > 0 {
					return invalid
				}
				tm.add(readMilliseconds(sc.next()))
			}
		}
		if sc.peek.isKeywordZ() {
			sc.next()
			tz.set(0)
		} else if sc.peek.isSymbol('+') || sc.peek.isSymbol('-') {
			sign := 1
			if sc.next().value == '-' {
				sign = -1
			}
			tz.setSign(sign)
			if sc.peek.isFixedLength(4) {
				hourMin := sc.next().value
				hour, minute := hourMin/100, hourMin%100
				if !isHour(hour) || !isMinute(minute) {
					return invalid
				}
				tz.hour, tz.minute = hour, minute
			} else {
				if !sc.peek.isFixedLength(2) || !isHour(sc.peek.value) {
					return invalid
				}
				tz.hour = sc.next().value
				if !sc.skipSymbol(':') {
					return invalid
				}
				if !sc.peek.isFixedLength(2) || !isMinute(sc.peek.value) {
					return invalid
				}
				tz.minute = sc.next().value
			}
		}
		if sc.peek.kind != dateEnd {
			return invalid
		}
	}
	if tz.isEmpty() && tm.index == 0 {
		tz.set(0)
	}
	day.isISO = true
	return dateToken{kind: dateEnd}
}

// jsDateParse is Date.parse for the process's local time zone: milliseconds since the epoch, or NaN when V8 does not recognise the string.
func jsDateParse(s string) float64 { return jsDateParseIn(s, time.Local) }

// DateParse is JavaScript's Date.parse: milliseconds since the epoch, or NaN when V8 does not recognise the string. A string that names no zone is local time.
func DateParse(s string) float64 { return jsDateParse(s) }

// jsDateParseIn is jsDateParse with the zone V8 applies to a string that names none.
func jsDateParseIn(s string, local *time.Location) float64 {
	sc := newDateScanner(s)
	day := dayComposer{namedMonth: dateNone}
	tm := timeComposer{hourOffset: dateNone}
	tz := zoneComposer{sign: dateNone, hour: dateNone, minute: dateNone}

	next := parseES5DateTime(sc, &day, &tm, &tz)
	if next.kind == dateInvalid {
		return math.NaN()
	}
	hasReadNumber := day.index != 0
	for token := next; token.kind != dateEnd; token = sc.next() {
		switch {
		case token.isNumber():
			hasReadNumber = true
			n := token.value
			switch {
			case sc.skipSymbol(':'):
				if sc.skipSymbol(':') {
					if tm.index != 0 {
						return math.NaN()
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return math.NaN()
					}
					if sc.peek.isSymbol('.') {
						sc.next()
					}
				}
			case sc.skipSymbol('.') && tm.isExpecting(n):
				tm.add(n)
				if !sc.peek.isNumber() {
					return math.NaN()
				}
				ms := readMilliseconds(sc.next())
				if ms < 0 {
					return math.NaN()
				}
				tm.addFinal(ms)
			case tz.isExpecting(n):
				tz.minute = n
			case tm.isExpecting(n):
				tm.addFinal(n)
				if p := sc.peek; p.kind != dateEnd && p.kind != dateWhiteSpace && !p.isKeywordZ() && !p.isAsciiSign() {
					return math.NaN()
				}
			default:
				if !day.add(n) {
					return math.NaN()
				}
				sc.skipSymbol('-')
			}
		case token.kind == dateKeyword:
			switch {
			case token.kw == keywordAMPM && tm.index != 0:
				tm.hourOffset = token.value
			case token.kw == keywordMonthName:
				day.namedMonth = token.value
				sc.skipSymbol('-')
			case token.kw == keywordTimeZoneName && hasReadNumber:
				tz.set(token.value)
			default:
				if hasReadNumber {
					return math.NaN()
				}
				if sc.peek.isNumber() {
					return math.NaN()
				}
			}
		case token.isAsciiSign() && (tz.isUTC() || tm.index != 0):
			tz.setSign(token.asciiSign())
			n, length := 0, 0
			if sc.peek.isNumber() {
				t := sc.next()
				length, n = t.length, t.value
			}
			hasReadNumber = true
			switch {
			case sc.peek.isSymbol(':'):
				tz.hour = n
				tz.minute = dateNone
			case length == 2 || length == 1:
				tz.hour, tz.minute = n, 0
			case length == 4 || length == 3:
				tz.hour, tz.minute = n/100, n%100
			default:
				return math.NaN()
			}
		case (token.isAsciiSign() || token.isSymbol(')')) && hasReadNumber:
			return math.NaN()
		}
	}

	year, month, dayOfMonth, ok := day.write()
	if !ok {
		return math.NaN()
	}
	hour, minute, second, ms, ok := tm.write()
	if !ok {
		return math.NaN()
	}
	var offsetSeconds int64
	hasOffset := tz.sign != dateNone
	if hasOffset {
		if tz.hour == dateNone {
			tz.hour = 0
		}
		if tz.minute == dateNone {
			tz.minute = 0
		}
		// TimeZoneComposer::Write sums in unsigned 32-bit arithmetic and rejects a total above Smi::kMaxValue, which is 2^31-1 in Node's V8 (built without pointer compression, so Smis are 32-bit).
		total := uint32(tz.hour)*3600 + uint32(tz.minute)*60
		if total > math.MaxInt32 {
			return math.NaN()
		}
		offsetSeconds = int64(total) * int64(tz.sign)
	}

	// MakeDay, MakeTime and MakeDate (ES MakeDay over a proleptic Gregorian calendar; a day past the month's end rolls over).
	days := daysFromCivil(int64(year), int64(month)) + int64(dayOfMonth-1)
	date := float64(days)*86400000 + float64(hour)*3600000 + float64(minute)*60000 + float64(second)*1000 + float64(ms)
	if !hasOffset {
		if date < -timeClipBeforeUTCMs || date > timeClipBeforeUTCMs {
			return math.NaN()
		}
		date = localToUTC(date, local)
	} else {
		date -= float64(offsetSeconds) * 1000
	}
	if math.Abs(date) > timeClipMs {
		return math.NaN()
	}
	return date
}

// daysFromCivil is the number of days from 1970-01-01 to the first-of-month (year, month0) in the proleptic Gregorian calendar; month0 is zero-based.
func daysFromCivil(year, month0 int64) int64 {
	y := year
	m := month0 + 1
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp + 2) / 5
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// localToUTC is DateCache::ToUTC: the instant whose wall clock in loc reads t (milliseconds, as if UTC). A wall time that occurs twice resolves to its first occurrence and one that never occurs resolves with the offset in effect before the transition, as ICU's former-offset rule gives V8.
func localToUTC(t float64, loc *time.Location) float64 {
	offsetAt := func(instantMs float64) float64 {
		_, offset := time.UnixMilli(int64(instantMs)).In(loc).Zone()
		return float64(offset) * 1000
	}
	before, after := offsetAt(t-864e5), offsetAt(t+864e5)
	if before == after {
		return t - before
	}
	first, second := t-before, t-after
	firstValid, secondValid := offsetAt(first) == before, offsetAt(second) == after
	switch {
	case firstValid && secondValid:
		return min(first, second)
	case secondValid:
		return second
	}
	return first
}
