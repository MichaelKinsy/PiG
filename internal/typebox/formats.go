package typebox

// Ports typebox 1.3.27 build/format/*.mjs: the format registry that Value.Check asserts for the "format" keyword. The expressions are the sources of the JavaScript literals, which RE2 accepts. A JavaScript expression with the
// i flag matches ASCII letters only, so a non-ASCII value fails before the matcher can fold U+017F or U+212A into a letter. hostname, idn-hostname, idn-email and regex keep the JSON Schema library's checks.

import (
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/third_party/jsonschema"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// The expressions of the format checks. Formats creates them with the format table on first use, so package initialization allocates
// nothing for them; a check runs only from the table.
var formatDateTimeSeparator, formatEmailRE, formatUUIDRE, formatIPv4RE, formatIPv6RE, formatDurationRE, formatJSONPointerRE,
	formatJSONPointerURIFragmentRE, formatRelativeJSONPointerRE, formatURIRE, formatURIReferenceRE, formatURITemplateRE, formatDateRE,
	formatTimeRE, formatIRIInvalidChars, formatIRIReferenceChars, formatMalformedScheme, formatIPvFuture *lazyregexp.Regexp

// formats holds the table Formats returns.
var formats struct {
	once  sync.Once
	table map[string]func(string) bool
}

// Formats returns the formats of typebox's registry that Pig ports, by name. Every call returns the same map.
func Formats() map[string]func(string) bool {
	formats.once.Do(func() {
		formatDateTimeSeparator = lazyregexp.New(`[Tt]`) // /T/i in value.split(/T/i)
		formatEmailRE = lazyregexp.New("^(?:[a-zA-Z0-9!#$%&'*+/=?^_`{|}~-]+(?:\\.[a-zA-Z0-9!#$%&'*+/=?^_`{|}~-]+)*|\"(?:[^\"\\\\]|\\\\[\\x20-\\x7e])*\")@(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*|\\[(?:[iI][pP][vV]6:[a-fA-F0-9:]+|(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3})\\])$")
		//portlint:allow regexfold every caller checks isASCII first, and on ASCII JS toUpperCase and Go simple folding agree
		formatUUIDRE = lazyregexp.New("(?i)^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$")
		formatIPv4RE = lazyregexp.New("^(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)$")
		//portlint:allow regexfold every caller checks isASCII first, and on ASCII JS toUpperCase and Go simple folding agree
		formatIPv6RE = lazyregexp.New("(?i)^(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:)?[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)$")
		formatDurationRE = lazyregexp.New("^P((\\d+Y(\\d+M(\\d+D)?)?|\\d+M(\\d+D)?|\\d+D)(T(\\d+H(\\d+M(\\d+S)?)?|\\d+M(\\d+S)?|\\d+S))?|T(\\d+H(\\d+M(\\d+S)?)?|\\d+M(\\d+S)?|\\d+S)|\\d+W)$")
		formatJSONPointerRE = lazyregexp.New("^(?:\\/(?:[^~/]|~0|~1)*)*$")
		//portlint:allow regexfold every caller checks isASCII first, and on ASCII JS toUpperCase and Go simple folding agree
		formatJSONPointerURIFragmentRE = lazyregexp.New("(?i)^#(?:\\/(?:[a-z0-9_\\-.!$&'()*+,;:=@]|%[0-9a-f]{2}|~0|~1)*)*$")
		formatRelativeJSONPointerRE = lazyregexp.New("^(?:0|[1-9][0-9]*)(?:#|(?:\\/(?:[^~/]|~0|~1)*)*)$")
		//portlint:allow regexfold every caller checks isASCII first, and on ASCII JS toUpperCase and Go simple folding agree
		formatURIRE = lazyregexp.New("(?i)^[a-z][a-z0-9+\\-.]*:(?:\\/\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\\[(?:(?:(?:[\\da-f]{1,4}:){6}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|::(?:[\\da-f]{1,4}:){5}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){4}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,1}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){3}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,2}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){2}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,3}[\\da-f]{1,4})?::[\\da-f]{1,4}:(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,4}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,5}[\\da-f]{1,4})?::[\\da-f]{1,4}|(?:(?:[\\da-f]{1,4}:){0,6}[\\da-f]{1,4})?::)|v[0-9a-f]+\\.[-a-z0-9._~!$&'()*+,;=:]+)\\]|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\\d*)?(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\\?(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$")
		//portlint:allow regexfold every caller checks isASCII first, and on ASCII JS toUpperCase and Go simple folding agree
		formatURIReferenceRE = lazyregexp.New("(?i)^(?:[a-z][a-z0-9+\\-.]*:(?:\\/\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\\[(?:(?:(?:[\\da-f]{1,4}:){6}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|::(?:[\\da-f]{1,4}:){5}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){4}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,1}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){3}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,2}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){2}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,3}[\\da-f]{1,4})?::[\\da-f]{1,4}:(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,4}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,5}[\\da-f]{1,4})?::[\\da-f]{1,4}|(?:(?:[\\da-f]{1,4}:){0,6}[\\da-f]{1,4})?::)|v[0-9a-f]+\\.[-a-z0-9._~!$&'()*+,;=:]+)\\]|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\\d*)?(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:\\/\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\\[(?:(?:(?:[\\da-f]{1,4}:){6}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|::(?:[\\da-f]{1,4}:){5}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){4}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,1}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){3}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,2}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:){2}(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,3}[\\da-f]{1,4})?::[\\da-f]{1,4}:(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,4}[\\da-f]{1,4})?::(?:[\\da-f]{1,4}:[\\da-f]{1,4}|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d))|(?:(?:[\\da-f]{1,4}:){0,5}[\\da-f]{1,4})?::[\\da-f]{1,4}|(?:(?:[\\da-f]{1,4}:){0,6}[\\da-f]{1,4})?::)|v[0-9a-f]+\\.[-a-z0-9._~!$&'()*+,;=:]+)\\]|(?:(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)\\.){3}(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)|(?:[-a-z0-9._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\\d*)?(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\\/(?:(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[-a-z0-9._~!$&'()*+,;=@]|%[0-9a-f]{2})+(?:\\/(?:[-a-z0-9._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?)(?:\\?(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[-a-z0-9._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$")
		formatURITemplateRE = lazyregexp.New("^(?:(?:[^\\x00-\\x20\"<>%\\\\^`{|}\\x7f]|%[0-9a-fA-F]{2})|\\{[+#./;?&=,!@|]?(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+(?:\\.(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+)*(?::[1-9]\\d{0,3}|\\*)?(?:,(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+(?:\\.(?:[a-zA-Z0-9_]|%[0-9a-fA-F]{2})+)*(?::[1-9]\\d{0,3}|\\*)?)*\\})*$")
		formatDateRE = lazyregexp.New("^(\\d\\d\\d\\d)-(\\d\\d)-(\\d\\d)$")
		formatTimeRE = lazyregexp.New("^(\\d\\d):(\\d\\d):(\\d\\d)(?:\\.\\d+)?(?:([Zz])|([+-])(\\d\\d):(\\d\\d))?$")
		formatIRIInvalidChars = lazyregexp.New("[\\x00-\\x20<>\\^`{|}\\\\]")
		formatIRIReferenceChars = lazyregexp.New("[\\x00-\\x20\\x7F\\\\]")
		formatMalformedScheme = lazyregexp.New(`^[a-zA-Z][a-zA-Z0-9+\-.]*//`)
		formatIPvFuture = lazyregexp.New(`\[[vV][0-9a-fA-F]+\.[^\]]+\]`)
		formats.table = map[string]func(string) bool{
			"date-time":                 formatIsDateTime,
			"date":                      formatIsDate,
			"time":                      formatIsTime,
			"duration":                  formatDurationRE.MatchString,
			"email":                     formatEmailRE.MatchString,
			"ipv4":                      formatIPv4RE.MatchString,
			"ipv6":                      func(v string) bool { return isASCII(v) && formatIPv6RE.MatchString(v) },
			"iri":                       formatIsIRI,
			"iri-reference":             formatIsIRIReference,
			"json-pointer":              formatJSONPointerRE.MatchString,
			"json-pointer-uri-fragment": func(v string) bool { return isASCII(v) && formatJSONPointerURIFragmentRE.MatchString(v) },
			"relative-json-pointer":     formatRelativeJSONPointerRE.MatchString,
			"uri":                       func(v string) bool { return isASCII(v) && formatURIRE.MatchString(v) },
			"uri-reference":             func(v string) bool { return isASCII(v) && formatURIReferenceRE.MatchString(v) },
			"uri-template":              formatURITemplateRE.MatchString,
			"url":                       func(v string) bool { _, err := nodeurl.ParseURL(v); return err == nil },
			"uuid":                      func(v string) bool { return isASCII(v) && formatUUIDRE.MatchString(v) },
		}
	})
	return formats.table
}

func isASCII(value string) bool {
	for i := range len(value) {
		if value[i] >= 0x80 {
			return false
		}
	}
	return true
}

// hasMalformedPercent reports a "%" not followed by two hex digits (the lookahead /%(?![0-9a-fA-F]{2})/ of the iri formats).
func hasMalformedPercent(value string) bool {
	for i := range len(value) {
		if value[i] == '%' && (i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2])) {
			return true
		}
	}
	return false
}

func isHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

func formatIsDate(value string) bool {
	m := formatDateRE.FindStringSubmatch(value)
	if m == nil {
		return false
	}
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	day, _ := strconv.Atoi(m[3])
	days := [...]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	limit := days[month]
	if month == 2 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
		limit = days[month] + 1
	}
	return day <= limit
}

func formatIsTime(value string) bool {
	m := formatTimeRE.FindStringSubmatch(value)
	if m == nil || (m[4] == "" && m[5] == "") {
		return false
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	second, _ := strconv.Atoi(m[3])
	if hour > 23 || minute > 59 || second > 60 {
		return false
	}
	if m[5] != "" {
		zoneHour, _ := strconv.Atoi(m[6])
		zoneMinute, _ := strconv.Atoi(m[7])
		if zoneHour > 23 || zoneMinute > 59 {
			return false
		}
	}
	if second < 60 {
		return true
	}
	sign := 1
	if m[5] == "-" {
		sign = -1
	}
	zoneHour, _ := strconv.Atoi(m[6])
	zoneMinute, _ := strconv.Atoi(m[7])
	total := hour*60 + minute - sign*(zoneHour*60+zoneMinute)
	return (total%1440+1440)%1440 == 1439
}

// formatIsDateTime is value.split(/T/i) with exactly two parts, a date and a time.
func formatIsDateTime(value string) bool {
	parts := formatDateTimeSeparator.Split(value, -1)
	return len(parts) == 2 && formatIsDate(parts[0]) && formatIsTime(parts[1])
}

func formatIsIRI(value string) bool {
	if formatIRIInvalidChars.MatchString(value) || hasMalformedPercent(value) {
		return false
	}
	if jsstring.Length(value) < 2048 {
		if loc := formatIPvFuture.FindStringIndex(value); loc != nil {
			value = value[:loc[0]] + "[::1]" + value[loc[1]:]
		}
	}
	_, err := nodeurl.ParseURL(value)
	return err == nil
}

func formatIsIRIReference(value string) bool {
	if formatIRIReferenceChars.MatchString(value) || hasMalformedPercent(value) || formatMalformedScheme.MatchString(value) {
		return false
	}
	_, err := nodeurl.ResolveURL(value, "http://example.com")
	return err == nil
}

// unregisteredTypeboxFormats are the JSON Schema library's built-in formats that typebox's registry lacks. Format.Test returns true for a format it does not have (format/_registry.mjs Test), so asserting the format turns these
// into no-ops instead of the library's checks.
var unregisteredTypeboxFormats = []string{"period", "semver"}

// RegisterFormats makes the compiler assert the typebox formats.
func RegisterFormats(compiler *jsonschema.Compiler) {
	compiler.AssertFormat()
	for _, name := range unregisteredTypeboxFormats {
		compiler.RegisterFormat(&jsonschema.Format{Name: name, Validate: func(any) error { return nil }})
	}
	for name, check := range Formats() {
		compiler.RegisterFormat(&jsonschema.Format{Name: name, Validate: func(value any) error {
			if text, ok := value.(string); ok && !check(text) {
				return &formatError{name}
			}
			return nil
		}})
	}
}

type formatError struct{ name string }

func (e *formatError) Error() string { return "invalid " + strings.TrimSpace(e.name) }
