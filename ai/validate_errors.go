package ai

import (
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// argumentValidationErrors follows TypeBox 1.3.27 schema/engine/schema.mjs and
// system/locale/en_US.mjs. Unlike jsonschema.ValidationError it reports siblings
// after a type failure and includes failing union arms before the union error.
func (s *argumentSchema) argumentValidationErrors(value any, path string) []string {
	var lines []string
	emit := func(message string) {
		location := path
		if location == "" {
			location = "root"
		}
		lines = append(lines, "  - "+location+": "+message)
	}
	nested := func(child *argumentSchema, item any, nextPath string) bool {
		failures := child.argumentValidationErrors(item, nextPath)
		lines = append(lines, failures...)
		return len(failures) == 0
	}
	if boolean, ok := s.doc.(bool); ok {
		if !boolean {
			emit("schema is false")
		}
		return lines
	}
	types := s.types()
	if len(types) > 0 && !slices.ContainsFunc(types, func(typ string) bool { return matchesArgumentType(value, typ) }) {
		if _, union := s.value("type").([]any); union {
			emit("must be either " + strings.Join(types, " or "))
		} else {
			emit("must be " + types[0])
		}
	}
	if object, ok := value.(map[string]any); ok {
		var missing []string
		for _, required := range anySlice(s.value("required")) {
			key, _ := required.(string)
			if _, exists := object[key]; !exists {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			location := argumentPath(path, missing[0])
			lines = append(lines, "  - "+location+": must have required properties "+strings.Join(missing, ", "))
		}
		properties := s.child("properties")
		patterns := s.child("patternProperties")
		if additional := s.child("additionalProperties"); additional != nil {
			failed := false
			for _, key := range slices.Sorted(maps.Keys(object)) {
				known := properties != nil && properties.child(key) != nil
				if patterns != nil {
					for _, pattern := range patterns.keys {
						if argumentPatternMatches(pattern, key) {
							known = true
						}
					}
				}
				if !known && !nested(additional, object[key], argumentPath(path, escapeArgumentPointer(key))) {
					failed = true
				}
			}
			if failed {
				emit("must not have additional properties")
			}
		}
		for _, keyword := range []string{"dependencies", "dependentRequired", "dependentSchemas"} {
			if dependencies := s.child(keyword); dependencies != nil {
				for _, key := range dependencies.keys {
					if _, exists := object[key]; !exists {
						continue
					}
					child := dependencies.child(key)
					if required, ok := child.doc.([]any); ok {
						var missing []string
						for _, name := range required {
							key, _ := name.(string)
							if _, exists := object[key]; !exists {
								missing = append(missing, key)
							}
						}
						if len(missing) > 0 {
							emit(fmt.Sprintf("must have properties %s when property %s is present", strings.Join(missing, ", "), key))
						}
					} else {
						nested(child, value, path)
					}
				}
			}
		}
		if patterns != nil {
			for _, pattern := range patterns.keys {
				for _, key := range slices.Sorted(maps.Keys(object)) {
					if argumentPatternMatches(pattern, key) {
						nested(patterns.child(pattern), object[key], argumentPath(path, escapeArgumentPointer(key)))
					}
				}
			}
		}
		if properties != nil {
			for _, key := range properties.keys {
				if item, exists := object[key]; exists {
					nested(properties.child(key), item, argumentPath(path, escapeArgumentPointer(key)))
				}
			}
		}
		if names := s.child("propertyNames"); names != nil {
			var invalid []string
			for _, key := range slices.Sorted(maps.Keys(object)) {
				if !nested(names, key, path) {
					invalid = append(invalid, key)
				}
			}
			if len(invalid) > 0 {
				emit("property names " + strings.Join(invalid, ", ") + " are invalid")
			}
		}
		s.argumentLimitErrors(float64(len(object)), "minProperties", "maxProperties", "properties", emit)
	}
	if items, ok := value.([]any); ok {
		if tuple := s.list("items"); tuple != nil {
			if additional := s.child("additionalItems"); additional != nil {
				for i := len(tuple); i < len(items); i++ {
					nested(additional, items[i], argumentPath(path, fmt.Sprint(i)))
				}
			}
		}
		if contains := s.child("contains"); contains != nil {
			var failures []string
			count := 0
			for i, item := range items {
				current := contains.argumentValidationErrors(item, argumentPath(path, fmt.Sprint(i)))
				if len(current) == 0 {
					count++
				}
				failures = append(failures, current...)
			}
			if count == 0 {
				lines = append(lines, failures...)
				emit("must contain at least 1 valid item")
			}
		}
		if tuple := s.list("items"); tuple != nil {
			for i := range min(len(tuple), len(items)) {
				nested(tuple[i], items[i], argumentPath(path, fmt.Sprint(i)))
			}
		} else if child := s.child("items"); child != nil {
			for i, item := range items {
				nested(child, item, argumentPath(path, fmt.Sprint(i)))
			}
		}
		s.argumentLimitErrors(float64(len(items)), "minItems", "maxItems", "items", emit)
		if prefix := s.list("prefixItems"); prefix != nil {
			for i := range min(len(prefix), len(items)) {
				nested(prefix[i], items[i], argumentPath(path, fmt.Sprint(i)))
			}
		}
		if s.value("uniqueItems") == true && !s.checkKeyword("uniqueItems", value) {
			emit("must not have duplicate items")
		}
	}
	if text, ok := value.(string); ok {
		// TypeBox uses code points for JSON Schema string lengths, not UTF-16 units.
		s.argumentLimitErrors(float64(len([]rune(text))), "minLength", "maxLength", "characters", emit)
		if s.text("format") != "" && !s.checkKeyword("format", value) {
			emit(fmt.Sprintf("must match format \"%s\"", s.text("format")))
		}
		if s.text("pattern") != "" && !s.checkKeyword("pattern", value) {
			emit(fmt.Sprintf("must match pattern \"%s\"", s.text("pattern")))
		}
	}
	if number, ok := value.(float64); ok {
		for _, rule := range []struct {
			key, comparison string
			failed          func(float64) bool
		}{
			{"exclusiveMaximum", "<", func(limit float64) bool { return number >= limit }},
			{"exclusiveMinimum", ">", func(limit float64) bool { return number <= limit }},
			{"maximum", "<=", func(limit float64) bool { return number > limit }},
			{"minimum", ">=", func(limit float64) bool { return number < limit }},
		} {
			if limit, ok := s.value(rule.key).(float64); ok && rule.failed(limit) {
				emit("must be " + rule.comparison + " " + argumentNumberString(limit))
			}
		}
		if multiple, ok := s.value("multipleOf").(float64); ok && math.Mod(number, multiple) != 0 {
			emit("must be multiple of " + argumentNumberString(multiple))
		}
	}
	if ref := s.text("$ref"); ref != "" {
		if target := s.resolveArgumentRef(ref); target != nil {
			nested(target, value, path)
		}
	}
	if _, exists := s.fields["const"]; exists && !s.checkKeyword("const", value) {
		emit("must be equal to constant")
	}
	if _, exists := s.fields["enum"]; exists && !s.checkKeyword("enum", value) {
		emit("must be equal to one of the allowed values")
	}
	if condition := s.child("if"); condition != nil {
		branch := "else"
		if condition.check(value) {
			branch = "then"
		}
		if child := s.child(branch); child != nil && !nested(child, value, path) {
			emit("must match \"" + branch + "\" schema")
		}
	}
	if child := s.child("not"); child != nil && child.check(value) {
		emit("must not be valid")
	}
	for _, child := range s.list("allOf") {
		nested(child, value, path)
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		children := s.list(keyword)
		if children == nil {
			continue
		}
		count := 0
		var failures []string
		for _, child := range children {
			current := child.argumentValidationErrors(value, path)
			if len(current) == 0 {
				count++
			}
			failures = append(failures, current...)
		}
		if count == 0 || (keyword == "oneOf" && count != 1) {
			if count == 0 {
				lines = append(lines, failures...)
			}
			if keyword == "anyOf" {
				emit("must match a schema in anyOf")
			} else {
				emit("must match exactly one schema in oneOf")
			}
		}
	}
	return lines
}

func (s *argumentSchema) resolveArgumentRef(ref string) *argumentSchema {
	fragment, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	fragment, err := url.PathUnescape(fragment)
	if err != nil {
		return nil
	}
	current := s.root
	for part := range strings.SplitSeq(fragment, "/") {
		if current == nil {
			return nil
		}
		current = current.child(strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~"))
	}
	return current
}

func (s *argumentSchema) checkKeyword(keyword string, value any) bool {
	schema := &argumentSchema{doc: map[string]any{keyword: s.value(keyword)}}
	return schema.check(value)
}

func (s *argumentSchema) argumentLimitErrors(size float64, minimum, maximum, unit string, emit func(string)) {
	// Array and string upper bounds precede lower bounds in TypeBox's error traversal.
	order := []string{maximum, minimum}
	if unit == "properties" {
		order = []string{minimum, maximum}
	}
	for _, keyword := range order {
		limit, ok := s.value(keyword).(float64)
		if !ok {
			continue
		}
		if keyword == minimum && size < limit {
			emit("must not have fewer than " + argumentNumberString(limit) + " " + unit)
		}
		if keyword == maximum && size > limit {
			emit("must not have more than " + argumentNumberString(limit) + " " + unit)
		}
	}
}

func argumentPatternMatches(pattern, value string) bool {
	expression, err := regexp.Compile(pattern)
	return err == nil && expression.MatchString(value)
}
func argumentPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
func escapeArgumentPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
