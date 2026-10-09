package yaml12

// The tags the core schema recognises only when a document names them (yaml 2.9.0 coreKnownTags: binary, merge, omap, pairs, set, timestamp), and the JavaScript values they build.

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

const (
	binaryTag    = "tag:yaml.org,2002:binary"
	mergeTag     = "tag:yaml.org,2002:merge"
	omapTag      = "tag:yaml.org,2002:omap"
	pairsTag     = "tag:yaml.org,2002:pairs"
	setTag       = "tag:yaml.org,2002:set"
	timestampTag = "tag:yaml.org,2002:timestamp"
)

// knownTagCollectionKind reports a known tag and the collection kind it expects ("" for a scalar tag).
func knownTagCollectionKind(tagName string) (string, bool) {
	switch tagName {
	case binaryTag, mergeTag, timestampTag:
		return "", true
	case setTag:
		return "map", true
	case omapTag, pairsTag:
		return "seq", true
	}
	return "", false
}

// knownCollectionTag returns the class a known collection tag gives a collection of the expected type, or "".
func knownCollectionTag(tagName, expType string) string {
	if kind, ok := knownTagCollectionKind(tagName); ok && kind == expType {
		switch tagName {
		case setTag:
			return "set"
		case omapTag:
			return "omap"
		case pairsTag:
			return "pairs"
		}
	}
	return ""
}

// knownTagFor is the tag of the value's own type when it is not a default tag.
func knownTagFor(n *node) string {
	if n.kind == kindScalar {
		switch n.value.(type) {
		case Date:
			return timestampTag
		case []byte:
			return binaryTag
		case Symbol:
			return mergeTag
		}
		return ""
	}
	switch n.class {
	case "set":
		return setTag
	case "omap":
		return omapTag
	case "pairs":
		return pairsTag
	}
	return ""
}

var timestampRE = lazyregexp.New(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})(?:(?:t|T|[ \t]+)([0-9]{1,2}):([0-9]{1,2}):([0-9]{1,2}(\.[0-9]+)?)(?:[ \t]*(Z|[-+][012]?[0-9](?::[0-9]{2})?))?)?$`)

func resolveKnownScalarTag(ctx *composeCtx, value, tagName string, tagToken *Token, onError errorFn) (scalarOutcome, bool) {
	switch tagName {
	case binaryTag:
		ctx.pushed[tagName] = true
		return scalarOutcome{value: nodeBase64(value)}, true
	case mergeTag:
		ctx.pushed[tagName] = true
		return scalarOutcome{value: Symbol("<<"), mergeKey: true}, true
	case timestampTag:
		ctx.pushed[tagName] = true
		m := timestampRE.FindStringSubmatch(value)
		if m == nil {
			onError(posOfToken(tagToken), "TAG_RESOLVE_FAILED", "!!timestamp expects a date, starting with yyyy-mm-dd")
			return scalarOutcome{value: value}, true
		}
		num := func(s string) float64 {
			if s == "" {
				return 0
			}
			f, _ := strconv.ParseFloat(s, 64)
			return f
		}
		year, month, day := num(m[1]), num(m[2]), num(m[3])
		hour, minute, second := num(m[4]), num(m[5]), num(m[6])
		millis := 0.0
		if m[7] != "" {
			frac := (m[7] + "00")[1:4]
			millis = num(frac)
		}
		date := dateUTC(year, month-1, day, hour, minute, second, millis)
		if tz := m[8]; tz != "" && tz != "Z" {
			d := parseSexagesimal(tz)
			if math.Abs(d) < 30 {
				d *= 60
			}
			date -= 60000 * d
		}
		return scalarOutcome{value: Date{MS: date}}, true
	}
	return scalarOutcome{}, false
}

func parseSexagesimal(s string) float64 {
	sign := s[0]
	parts := s
	if sign == '-' || sign == '+' {
		parts = s[1:]
	}
	res := 0.0
	for p := range strings.SplitSeq(strings.ReplaceAll(parts, "_", ""), ":") {
		f, _ := strconv.ParseFloat(p, 64)
		res = res*60 + f
	}
	if sign == '-' {
		return -res
	}
	return res
}

// dateUTC is Date.UTC: each argument is truncated to an integer and years 0 to 99 mean 1900 to 1999.
func dateUTC(year, month, day, hour, minute, second, ms float64) float64 {
	t := math.Trunc
	y := t(year)
	if y >= 0 && y <= 99 {
		y += 1900
	}
	// Days from the civil date, with month overflow carried into the year.
	m := t(month)
	y += math.Floor(m / 12)
	m -= 12 * math.Floor(m/12)
	days := daysFromCivil(int64(y), int64(m)+1, 1) + int64(t(day)) - 1
	return (float64(days)*24+t(hour))*3600000 + t(minute)*60000 + t(second)*1000 + t(ms)
}

// daysFromCivil counts days since 1970-01-01 of a proleptic Gregorian date.
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// nodeBase64 is Buffer.from(text, "base64"): both alphabets, illegal characters skipped, decoding ends at the first "=".
func nodeBase64(s string) []byte {
	var out []byte
	var acc uint32
	bits := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var v int
		switch {
		case c >= 'A' && c <= 'Z':
			v = int(c - 'A')
		case c >= 'a' && c <= 'z':
			v = int(c-'a') + 26
		case c >= '0' && c <= '9':
			v = int(c-'0') + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			return finishBase64(out)
		default:
			continue
		}
		acc = acc<<6 | uint32(v)
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= (1 << bits) - 1
		}
	}
	return finishBase64(out)
}

func finishBase64(out []byte) []byte {
	if out == nil {
		return []byte{}
	}
	return out
}

// resolveKnownCollection is the resolve step of the set, omap and pairs tags.
func resolveKnownCollection(coll *node, class string, onError func(string)) {
	switch class {
	case "set":
		if coll.kind == kindMap && hasAllNullValues(coll, true) {
			coll.class = "set"
		} else {
			onError("Set items must all have null values")
		}
	case "omap", "pairs":
		resolvePairs(coll, onError)
		coll.class = class
		if class == "omap" {
			var seen []any
			for _, p := range coll.pairs {
				if p.key.kind == kindScalar {
					// seenKeys.includes(key.value) (yaml-1.1/omap.ts): SameValueZero.
					if slices.ContainsFunc(seen, func(v any) bool { return jsStrictEquals(v, p.key.value, true) }) {
						onError("Ordered maps must not include duplicate keys: " + jsStringValueOf(p.key.value))
					}
					seen = append(seen, p.key.value)
				}
			}
		}
	}
}

// resolvePairs turns the items of a sequence into pairs.
func resolvePairs(seq *node, onError func(string)) {
	if seq.kind != kindSeq {
		onError("Expected a sequence for this tag")
		return
	}
	for _, item := range seq.items {
		var p *pair
		if item.kind == kindMap && item.class == "map" {
			if len(item.pairs) > 1 {
				onError("Each pair must have its own sequence indicator")
			}
			if len(item.pairs) > 0 {
				p = item.pairs[0]
			} else {
				p = &pair{key: &node{kind: kindScalar}}
			}
			if item.commentBefore != "" {
				prependComment(&p.key.commentBefore, item.commentBefore)
			}
			if item.comment != "" {
				cn := p.value
				if cn == nil {
					cn = p.key
				}
				prependComment(&cn.comment, item.comment)
			}
		} else {
			p = &pair{key: item}
		}
		seq.pairs = append(seq.pairs, p)
	}
	seq.items = nil
}

func jsStringValueOf(v any) string {
	// A template literal is String(value): JavaScript's number text (NaN, 0 for -0), not YAML's.
	return propertyKey(v)
}

// binaryString is the stringify function of the binary tag: base64, folded into lines of the line width.
func binaryString(n *node, value []byte, ctx *strCtx) string {
	str := base64Std(value)
	typ := n.typ
	if typ == "" {
		typ = "BLOCK_LITERAL"
	}
	if typ != "QUOTE_DOUBLE" {
		width := max(lineWidth-len(ctx.indent), minContentWidth)
		var lines []string
		for o := 0; o < len(str); o += width {
			lines = append(lines, str[o:min(o+width, len(str))])
		}
		sep := " "
		if typ == "BLOCK_LITERAL" {
			sep = "\n"
		}
		str = strings.Join(lines, sep)
	}
	return stringifyString(&node{typ: typ}, str, ctx)
}

func base64Std(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		v := uint32(chunk[0])<<16 | uint32(chunk[1])<<8 | uint32(chunk[2])
		sb.WriteByte(alphabet[v>>18&63])
		sb.WriteByte(alphabet[v>>12&63])
		if n > 1 {
			sb.WriteByte(alphabet[v>>6&63])
		} else {
			sb.WriteByte('=')
		}
		if n > 2 {
			sb.WriteByte(alphabet[v&63])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}
