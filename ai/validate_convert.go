package ai

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// convert follows TypeBox 1.3.27 value/convert. Only TypeBox's non-enumerable
// ~kind metadata selects this pass; plain JSON schemas use coerce instead.
func (s *argumentSchema) convert(value any) any {
	switch s.text("~kind") {
	case "Number", "Integer", "Boolean", "String", "Null":
		return coerceArgumentPrimitive(value, strings.ToLower(s.text("~kind")), true)
	case "Literal":
		candidate := coerceArgumentPrimitive(value, s.text("type"), true)
		if sameArgumentValue(candidate, s.value("const")) {
			return candidate
		}
	case "Object":
		s.eachProperty(value, true)
	case "Array":
		items, ok := value.([]any)
		if !ok {
			items = []any{value}
		}
		// FromArray returns a new array; replacing a root array is not a mutation.
		result := slices.Clone(items)
		if child := s.child("items"); child != nil {
			for i := range result {
				result[i] = child.convert(result[i])
			}
		}
		return result
	case "Tuple":
		if items, ok := value.([]any); ok {
			s.eachItem(items, func(child *argumentSchema, item any) any { return child.convert(item) })
		}
	case "Union":
		return coerceArgumentUnion(value, s.list("anyOf"), true)
	case "Enum", "Intersect", "TemplateLiteral":
		if evaluated := s.child("~convert"); evaluated != nil {
			return evaluated.convert(value)
		}
	case "Record":
		if object, ok := value.(map[string]any); ok {
			patterns := s.child("patternProperties")
			if patterns != nil {
				for _, pattern := range patterns.keys {
					expression, err := regexp.Compile(pattern)
					if err != nil {
						continue
					}
					child := patterns.child(pattern)
					for key, current := range object {
						if expression.MatchString(key) {
							object[key] = child.convert(current)
						}
					}
				}
			}
			if additional := s.child("additionalProperties"); additional != nil && patterns != nil {
				if _, ok := additional.doc.(map[string]any); ok {
					for _, pattern := range patterns.keys {
						for key, current := range object {
							if !argumentPatternMatches(pattern, key) {
								object[key] = additional.convert(current)
							}
						}
					}
				}
			}
		}
	}
	return value
}

func matchesArgumentType(value any, typ string) bool {
	switch typ {
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}

func coerceArgumentPrimitive(value any, typ string, native bool) any {
	switch typ {
	case "number", "integer":
		number, ok := argumentNumber(value, native)
		if !ok {
			return value
		}
		if typ == "integer" {
			if native {
				return math.Trunc(number)
			}
			if math.Trunc(number) != number {
				return value
			}
		}
		return number
	case "boolean":
		switch current := value.(type) {
		case nil:
			return false
		case bool:
			return current
		case float64:
			if current == 0 {
				return false
			}
			if current == 1 {
				return true
			}
		case string:
			if native {
				current = strings.ToLower(current)
			}
			if current == "true" || (native && current == "1") {
				return true
			}
			if current == "false" || (native && current == "0") {
				return false
			}
		}
	case "string":
		switch current := value.(type) {
		case nil:
			if native {
				return "null"
			}
			return ""
		case bool:
			return strconv.FormatBool(current)
		case float64:
			return argumentNumberString(current)
		}
	case "null":
		switch current := value.(type) {
		case bool:
			if !current {
				return nil
			}
		case float64:
			if current == 0 {
				return nil
			}
		case string:
			lower := strings.ToLower(current)
			if current == "" || (native && (lower == "null" || lower == "undefined" || current == "0")) {
				return nil
			}
		}
	}
	return value
}

var argumentDecimal = lazyregexp.New(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var argumentBigInt = lazyregexp.New(`^-?(0|[1-9][0-9]*)n$`)

func argumentNumber(value any, native bool) (float64, bool) {
	switch current := value.(type) {
	case nil:
		return 0, true
	case bool:
		if current {
			return 1, true
		}
		return 0, true
	case float64:
		return current, true
	case string:
		trimmed := strings.Trim(current, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
		if trimmed == "" {
			return 0, native
		}
		if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") || strings.HasPrefix(trimmed, "0b") || strings.HasPrefix(trimmed, "0B") || strings.HasPrefix(trimmed, "0o") || strings.HasPrefix(trimmed, "0O") {
			base := 16
			switch trimmed[1] {
			case 'b', 'B':
				base = 2
			case 'o', 'O':
				base = 8
			}
			if len(trimmed) == 2 || trimmed[2] == '-' || trimmed[2] == '+' {
				return 0, false
			}
			n, ok := new(big.Int).SetString(trimmed[2:], base)
			if ok {
				f, _ := new(big.Float).SetInt(n).Float64()
				return f, !math.IsInf(f, 0)
			}
		}
		if argumentDecimal.MatchString(trimmed) {
			n, err := strconv.ParseFloat(trimmed, 64)
			if err == nil && !math.IsInf(n, 0) {
				return n, true
			}
		}
		if native {
			switch strings.ToLower(current) {
			case "true":
				return 1, true
			case "false":
				return 0, true
			}
			if argumentBigInt.MatchString(current) {
				n, ok := new(big.Int).SetString(strings.TrimSuffix(current, "n"), 10)
				if ok && n.IsInt64() && n.Int64() >= -(1<<53-1) && n.Int64() <= 1<<53-1 {
					return float64(n.Int64()), true
				}
			}
		}
	}
	return 0, false
}

func argumentNumberString(number float64) string {
	if number == 0 {
		return "0"
	}
	// encoding/json uses ECMAScript's decimal/exponent thresholds and shortest representation.
	encoded, err := json.Marshal(number)
	if err != nil {
		return strconv.FormatFloat(number, 'g', -1, 64)
	}
	return string(encoded)
}
