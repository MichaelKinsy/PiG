package typebox

// Ports the checks of typebox 1.3.27 build/schema/engine that have no walk of their own: minLength, maxLength, pattern, format,
// maximum, minimum, exclusiveMaximum, exclusiveMinimum, multipleOf, const, enum, minItems, maxItems, uniqueItems, minProperties and
// maxProperties (build/guard/guard.mjs, build/guard/string.mjs, build/system/hashing/hash.mjs). The walkers (errors.go, refine.go)
// call checkKeyword for them, so one checker answers Value.Check and Value.Errors and no JSON Schema library is linked for them.
//
// A keyword whose value has the wrong JSON type is not a keyword of the schema: Schema.IsMinLength, IsMaximum, IsMultipleOf, IsMinItems and the other guards of build/schema/types need a finite number (minLength, maxLength,
// minimum, maximum, exclusiveMinimum, exclusiveMaximum, multipleOf, minItems, maxItems, minProperties, maxProperties), a string (pattern, format), an array (enum) or a boolean (uniqueItems), and CheckSchema and ErrorSchema skip the
// keyword otherwise, so it passes every value. Only const has no guard: it applies to any value. A value of the wrong type for the keyword passes as well, as the walkers
// only call a keyword with the value type it belongs to.

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// checkKeyword is the check of the leaf keyword key of n on value: a string for the string keywords, a float64 for the numeric ones, a
// list for the item keywords and an object for the property counts. const and enum take any value. A keyword the schema declares with the wrong type, and a value of the wrong type, pass.
func checkKeyword(n node, key string, value any) bool {
	declared, _ := n.get(key)
	switch key {
	case "minLength", "maxLength":
		text, isText := value.(string)
		limit, hasLimit := finiteNumber(declared)
		if !isText || !hasLimit {
			return true
		}
		// The conversion to UTF-16 units waits until the byte length or an ASCII text cannot decide. A text has at most one unit per byte, so fewer bytes than minLength are too short
		// and at most maxLength bytes are short enough; an ASCII text has one unit and one grapheme cluster per byte.
		if key == "minLength" {
			if limit == 0 {
				return true
			}
			if float64(len(text)) < limit {
				return false
			}
			return jsstring.ASCIIPrefix(text) == len(text) || isMinLength(jsstring.ToUTF16(text), limit)
		}
		if float64(len(text)) <= limit {
			return true
		}
		return jsstring.ASCIIPrefix(text) != len(text) && isMaxLength(jsstring.ToUTF16(text), limit)
	case "pattern":
		text, isText := value.(string)
		pattern, isPattern := declared.(string)
		if !isText || !isPattern {
			return true
		}
		expression, err := CompilePattern(pattern)
		return err == nil && expression.MatchString(text)
	case "format":
		text, isText := value.(string)
		name, isName := declared.(string)
		if !isText || !isName {
			return true
		}
		// Format.Test returns true for a format the registry does not have.
		check, registered := Formats()[name]
		return !registered || check(text)
	case "maximum", "minimum", "exclusiveMaximum", "exclusiveMinimum":
		number, isNumber := value.(float64)
		limit, hasLimit := finiteNumber(declared)
		if !isNumber || !hasLimit {
			return true
		}
		switch key {
		case "maximum":
			return number <= limit
		case "minimum":
			return number >= limit
		case "exclusiveMaximum":
			return number < limit
		}
		return number > limit
	case "multipleOf":
		number, isNumber := value.(float64)
		divisor, hasDivisor := finiteNumber(declared)
		return !isNumber || !hasDivisor || isMultipleOf(number, divisor)
	case "const":
		return equalsAllowed(value, Plain(declared))
	case "enum":
		options, isList := declared.([]any)
		if !isList {
			return true
		}
		for _, option := range options {
			if equalsAllowed(value, Plain(option)) {
				return true
			}
		}
		return false
	case "minItems", "maxItems", "minProperties", "maxProperties":
		var count int
		switch typed := value.(type) {
		case []any:
			count = len(typed)
		case map[string]any:
			count = len(typed)
		default:
			return true
		}
		limit, hasLimit := finiteNumber(declared)
		if !hasLimit {
			return true
		}
		if key == "minItems" || key == "minProperties" {
			return float64(count) >= limit
		}
		return float64(count) <= limit
	case "uniqueItems":
		items, isList := value.([]any)
		unique, isBool := declared.(bool)
		if !isList || !isBool {
			return true
		}
		return !unique || len(uniqueHashes(items)) == len(items)
	}
	return true
}

// finiteNumber is a keyword value that Guard.IsNumber accepts: a JSON number (Infinity and NaN are not JSON) as a float64.
func finiteNumber(declared any) (float64, bool) {
	number, isNumber := Plain(declared).(float64)
	return number, isNumber && !math.IsInf(number, 0) && !math.IsNaN(number)
}

// equalsAllowed is the comparison of const and enum: === with a value-like option, Guard.IsDeepEqual with any other.
func equalsAllowed(value, option any) bool {
	if isValueLike(option) {
		// The option is a scalar, so the comparison cannot panic on an uncomparable value.
		return value == option
	}
	return isDeepEqual(value, option)
}

// isValueLike is Guard.IsValueLike: a JSON scalar that === compares (a number is value-like when finite).
func isValueLike(value any) bool {
	switch typed := value.(type) {
	case nil, bool, string:
		return true
	case float64:
		return !math.IsInf(typed, 0) && !math.IsNaN(typed)
	}
	return false
}

// isDeepEqual is Guard.IsDeepEqual. An array equals only an array. An object equals an object, and also an array (DeepEqualObject accepts any object on the right): the object then needs
// the own property names of the array, its indices and "length", with the items and the length as values.
func isDeepEqual(left, right any) bool {
	switch typed := left.(type) {
	case []any:
		other, ok := right.([]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for index := range typed {
			if !isDeepEqual(typed[index], other[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		switch other := right.(type) {
		case map[string]any:
			if len(typed) != len(other) {
				return false
			}
			for key, item := range typed {
				counterpart, exists := other[key]
				if !exists || !isDeepEqual(item, counterpart) {
					return false
				}
			}
			return true
		case []any:
			if len(typed) != len(other)+1 {
				return false
			}
			for key, item := range typed {
				if key == "length" {
					if !isDeepEqual(item, float64(len(other))) {
						return false
					}
					continue
				}
				index, isIndex := jsstring.ArrayIndex(key)
				if !isIndex || int(index) >= len(other) || !isDeepEqual(item, other[index]) {
					return false
				}
			}
			return true
		}
	case nil, bool, string, float64:
		switch right.(type) {
		case nil, bool, string, float64:
			return left == right
		}
	}
	return false
}

// isMultipleOf is Guard.IsMultipleOf for numbers: a tolerance of 1e-10 on the remainder, and an integer is a multiple of any divisor whose reciprocal is an integer.
func isMultipleOf(dividend, divisor float64) bool {
	if math.IsNaN(dividend) || math.IsInf(dividend, 0) {
		return true
	}
	if dividend == math.Trunc(dividend) && math.Mod(1/divisor, 1) == 0 {
		return true
	}
	remainder := math.Mod(dividend, divisor)
	return math.Min(math.Min(math.Abs(remainder), math.Abs(remainder-divisor)), math.Abs(remainder+divisor)) < 1e-10
}

// uniqueHashes is the set of Hashing.Hash of items.
func uniqueHashes(items []any) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(items))
	for _, item := range items {
		set[hashValue(item)] = struct{}{}
	}
	return set
}

// hashValue is Hashing.Hash (build/system/hashing/hash.mjs) of a JSON value: FNV-1a 64 over a byte marker per type, an
// array's items, an object's members in the UTF-16 order of their names (InstanceKeys leaves out the name "constructor"), a
// number's 8 little-endian bytes and a string's UTF-8 text (a lone surrogate encodes as U+FFFD). 0 and -0 hash apart.
func hashValue(value any) uint64 {
	hash := fnvOffset64
	hash.value(value)
	return uint64(hash)
}

type fnv64 uint64

const (
	fnvOffset64 fnv64 = 14695981039346656037
	fnvPrime64  fnv64 = 1099511628211
)

// The byte markers of hash.mjs.
const (
	markerArray   = 0
	markerBoolean = 2
	markerNull    = 6
	markerNumber  = 7
	markerObject  = 8
	markerString  = 10
)

func (h *fnv64) op(b byte) { *h = (*h ^ fnv64(b)) * fnvPrime64 }

func (h *fnv64) value(value any) {
	switch typed := value.(type) {
	case nil:
		h.op(markerNull)
	case bool:
		h.op(markerBoolean)
		if typed {
			h.op(1)
		} else {
			h.op(0)
		}
	case float64:
		h.op(markerNumber)
		bits := math.Float64bits(typed)
		for range 8 {
			h.op(byte(bits))
			bits >>= 8
		}
	case string:
		h.text(typed)
	case []any:
		h.op(markerArray)
		for _, item := range typed {
			h.value(item)
		}
	case map[string]any:
		h.op(markerObject)
		names := make([]string, 0, len(typed))
		for name := range typed {
			if name != "constructor" {
				names = append(names, name)
			}
		}
		slices.SortFunc(names, compareUTF16)
		for _, name := range names {
			h.text(name)
			h.value(typed[name])
		}
	}
}

// text is the hash of a string: its marker and its UTF-8 bytes.
func (h *fnv64) text(text string) {
	h.op(markerString)
	if utf8.ValidString(text) {
		// The UTF-8 text of a string with no lone surrogate is the text itself.
		for index := range len(text) {
			h.op(text[index])
		}
		return
	}
	for _, b := range jsstring.ToUTF8(text) {
		h.op(b)
	}
}

// compareUTF16 orders two strings as Array.prototype.sort does by default: by UTF-16 code units. UTF-8 bytes sort in code point order, which is the UTF-16 order unless a character is U+D000 or above
// (a lead byte of 0xED or more: surrogate halves, U+E000 to U+FFFF and the astral planes, whose units start at 0xD800); only a pair with such a character converts the strings.
func compareUTF16(a, b string) int {
	if hasLeadAbove(a) || hasLeadAbove(b) {
		return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b))
	}
	return strings.Compare(a, b)
}

// hasLeadAbove reports whether text has a byte of 0xED or more, which only a character of U+D000 or above starts with.
func hasLeadAbove(text string) bool {
	for index := range len(text) {
		if text[index] >= 0xED {
			return true
		}
	}
	return false
}

// The grapheme length of a string, as build/guard/string.mjs counts it: a code point, the combining marks and variation selectors
// after it, zero-width-joiner sequences and a pair of regional indicators are one. The text is its UTF-16 units.

func inRange(unit rune, low, high rune) bool { return unit >= low && unit <= high }

func isHighSurrogate(unit rune) bool { return inRange(unit, 0xD800, 0xDBFF) }

func isCombiningMark(unit rune) bool {
	return inRange(unit, 0x0300, 0x036F) || inRange(unit, 0x1AB0, 0x1AFF) || inRange(unit, 0x1DC0, 0x1DFF) || inRange(unit, 0xFE20, 0xFE2F)
}

func isVariationSelector(unit rune) bool { return inRange(unit, 0xFE00, 0xFE0F) }

func isRegionalIndicator(point rune) bool { return inRange(point, 0x1F1E6, 0x1F1FF) }

// isGraphemeUnit is IsGraphemeCodePoint of a UTF-16 unit: one above U+02FF that is a high surrogate, a combining mark, a variation selector or a joiner.
func isGraphemeUnit(unit rune) bool {
	return unit >= 0x0300 && (isHighSurrogate(unit) || isCombiningMark(unit) || isVariationSelector(unit) || unit == 0x200D)
}

// codePointAt is String.prototype.codePointAt: a surrogate pair is one code point; a lone unit is itself.
func codePointAt(units []uint16, index int) rune {
	unit := rune(units[index])
	if isHighSurrogate(unit) && index+1 < len(units) {
		if low := rune(units[index+1]); inRange(low, 0xDC00, 0xDFFF) {
			return (unit-0xD800)<<10 + (low - 0xDC00) + 0x10000
		}
	}
	return unit
}

func codePointLength(point rune) int {
	if point > 0xFFFF {
		return 2
	}
	return 1
}

func consumeModifiers(units []uint16, index int) int {
	for index < len(units) {
		point := codePointAt(units, index)
		if !isCombiningMark(point) && !isVariationSelector(point) {
			break
		}
		index += codePointLength(point)
	}
	return index
}

func nextGraphemeClusterIndex(units []uint16, start int) int {
	startPoint := codePointAt(units, start)
	end := consumeModifiers(units, start+codePointLength(startPoint))
	for end < len(units)-1 && codePointAt(units, end) == 0x200D {
		next := codePointAt(units, end+1)
		end += 1 + codePointLength(next)
		end = consumeModifiers(units, end)
	}
	if isRegionalIndicator(startPoint) && end < len(units) && isRegionalIndicator(codePointAt(units, end)) {
		end += codePointLength(codePointAt(units, end))
	}
	return end
}

// isMinLength is Guard.IsMinLength: at least limit grapheme clusters. The length in units bounds the count from above.
func isMinLength(units []uint16, limit float64) bool {
	if limit == 0 {
		return true
	}
	if math.IsNaN(limit) {
		return false
	}
	if float64(len(units)) < limit {
		return false
	}
	for index := 0; ; {
		if index < len(units) && isGraphemeUnit(rune(units[index])) {
			count := 0
			for position := 0; position < len(units); {
				position = nextGraphemeClusterIndex(units, position)
				count++
				if float64(count) >= limit {
					return true
				}
			}
			return false
		}
		index++
		if float64(index) >= limit {
			return true
		}
	}
}

// isMaxLength is Guard.IsMaxLength: at most limit grapheme clusters.
func isMaxLength(units []uint16, limit float64) bool {
	if math.IsNaN(limit) {
		return false
	}
	if float64(len(units)) <= limit {
		return true
	}
	for index := 0; ; {
		if index < len(units) && isGraphemeUnit(rune(units[index])) {
			count := 0
			for position := 0; position < len(units); {
				position = nextGraphemeClusterIndex(units, position)
				count++
				if float64(count) > limit {
					return false
				}
			}
			return true
		}
		index++
		if float64(index) > limit {
			return false
		}
	}
}
