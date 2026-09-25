package tui

// theme_json.go ports upstream modes/interactive/theme/theme-json.ts: the
// schema check for user-authored theme files. Pi validates with a compiled
// TypeBox schema; this walks the same schema in the same order and reports the
// same errors, including TypeBox's eight-error cap.

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
)

// themeJSONMaxErrors is TypeBox's default maxErrors setting.
const themeJSONMaxErrors = 8

// themeColorTokens lists ThemeJsonSchema.colors in schema order; optional
// tokens may be omitted.
var themeColorTokens = []struct {
	name     string
	optional bool
}{
	{"accent", false}, {"border", false}, {"borderAccent", false}, {"borderMuted", false},
	{"success", false}, {"error", false}, {"warning", false}, {"muted", false}, {"dim", false},
	{"text", false}, {"thinkingText", false},
	{"scrollbarTrack", true}, {"scrollbarThumb", true},
	{"selectedBg", false}, {"searchMatchBg", true}, {"searchMatchText", true},
	{"userMessageBg", false}, {"userMessageText", false}, {"customMessageBg", false},
	{"customMessageText", false}, {"customMessageLabel", false}, {"toolPendingBg", false},
	{"toolSuccessBg", false}, {"toolErrorBg", false}, {"toolTitle", false}, {"toolOutput", false},
	{"mdHeading", false}, {"mdLink", false}, {"mdLinkUrl", false}, {"mdCode", false},
	{"mdCodeBlock", false}, {"mdCodeBlockBorder", false}, {"mdQuote", false},
	{"mdQuoteBorder", false}, {"mdHr", false}, {"mdListBullet", false},
	{"toolDiffAdded", false}, {"toolDiffRemoved", false}, {"toolDiffContext", false},
	{"syntaxComment", false}, {"syntaxKeyword", false}, {"syntaxFunction", false},
	{"syntaxVariable", false}, {"syntaxString", false}, {"syntaxNumber", false},
	{"syntaxType", false}, {"syntaxOperator", false}, {"syntaxPunctuation", false},
	{"thinkingOff", false}, {"thinkingMinimal", false}, {"thinkingLow", false},
	{"thinkingMedium", false}, {"thinkingHigh", false}, {"thinkingXhigh", false},
	{"thinkingMax", true},
	{"bashMode", false},
}

// themeExportTokens lists ThemeJsonSchema.export in schema order.
var themeExportTokens = []string{"pageBg", "cardBg", "infoBg"}

type themeSchemaError struct {
	path, keyword, message string
	missing                []string
}

type themeSchemaErrors struct{ list []themeSchemaError }

func (e *themeSchemaErrors) add(err themeSchemaError) {
	if len(e.list) < themeJSONMaxErrors {
		e.list = append(e.list, err)
	}
}

// ValidateThemeJSON validates one theme document (JSON text without a BOM)
// and returns an error naming the offending tokens, or nil. Mirrors upstream
// validateThemeJson.
func ValidateThemeJSON(label string, data []byte) error {
	var root any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return err
	}
	var errs themeSchemaErrors
	checkThemeDocument(&errs, root, data)
	if len(errs.list) > 0 {
		return errors.New(formatThemeSchemaErrors(label, errs.list))
	}
	name, _ := root.(map[string]any)["name"].(string)
	if strings.Contains(name, "/") {
		return errors.New("Invalid theme name \"" + name + "\": theme names cannot contain \"/\" because it is reserved for automatic light/dark theme settings.")
	}
	return nil
}

func checkThemeDocument(errs *themeSchemaErrors, root any, data []byte) {
	object, ok := root.(map[string]any)
	if !ok {
		errs.add(themeSchemaError{path: "", message: "must be object"})
		return
	}
	checkRequired(errs, "", object, []string{"name", "colors"})
	if value, present := object["$schema"]; present {
		checkString(errs, "/$schema", value)
	}
	if value, present := object["name"]; present {
		checkString(errs, "/name", value)
	}
	if value, present := object["vars"]; present {
		if vars, ok := value.(map[string]any); ok {
			for _, key := range jsPropertyOrder(themeObjectKeys(data, "vars")) {
				checkThemeColorValue(errs, "/vars/"+key, vars[key])
			}
		} else {
			errs.add(themeSchemaError{path: "/vars", message: "must be object"})
		}
	}
	if value, present := object["colors"]; present {
		checkThemeColors(errs, value)
	}
	if value, present := object["export"]; present {
		exports, ok := value.(map[string]any)
		if !ok {
			errs.add(themeSchemaError{path: "/export", message: "must be object"})
			return
		}
		for _, token := range themeExportTokens {
			if value, present := exports[token]; present {
				checkThemeColorValue(errs, "/export/"+token, value)
			}
		}
	}
}

func checkThemeColors(errs *themeSchemaErrors, value any) {
	colors, ok := value.(map[string]any)
	if !ok {
		errs.add(themeSchemaError{path: "/colors", message: "must be object"})
		return
	}
	var required []string
	for _, token := range themeColorTokens {
		if !token.optional {
			required = append(required, token.name)
		}
	}
	checkRequired(errs, "/colors", colors, required)
	for _, token := range themeColorTokens {
		if value, present := colors[token.name]; present {
			checkThemeColorValue(errs, "/colors/"+token.name, value)
		}
	}
}

func checkRequired(errs *themeSchemaErrors, path string, object map[string]any, required []string) {
	var missing []string
	for _, name := range required {
		if _, present := object[name]; !present {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		errs.add(themeSchemaError{path: path, keyword: "required", message: "must have required properties " + strings.Join(missing, ", "), missing: missing})
	}
}

func checkString(errs *themeSchemaErrors, path string, value any) {
	if _, ok := value.(string); !ok {
		errs.add(themeSchemaError{path: path, message: "must be string"})
	}
}

// checkThemeColorValue checks Union([String, Integer{0..255}]) the way TypeBox
// reports it: the string branch, the integer branch's type and bound
// failures, then the union failure.
func checkThemeColorValue(errs *themeSchemaErrors, path string, value any) {
	if _, ok := value.(string); ok {
		return
	}
	number, finite := themeFiniteNumber(value)
	integer := finite && number == math.Trunc(number)
	if integer && number >= 0 && number <= 255 {
		return
	}
	errs.add(themeSchemaError{path: path, message: "must be string"})
	if !integer {
		errs.add(themeSchemaError{path: path, message: "must be integer"})
	}
	if finite && number < 0 {
		errs.add(themeSchemaError{path: path, message: "must be >= 0"})
	}
	if finite && number > 255 {
		errs.add(themeSchemaError{path: path, message: "must be <= 255"})
	}
	errs.add(themeSchemaError{path: path, message: "must match a schema in anyOf"})
}

func themeFiniteNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(string(number), 64)
	if err != nil || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func formatThemeSchemaErrors(label string, list []themeSchemaError) string {
	missing := map[string]bool{}
	var other []string
	for _, err := range list {
		if err.keyword == "required" && err.path == "/colors" {
			for _, name := range err.missing {
				missing[name] = true
			}
			continue
		}
		path := err.path
		if path == "" {
			path = "/"
		}
		other = append(other, "  - "+path+": "+err.message)
	}
	var b strings.Builder
	b.WriteString("Invalid theme \"" + label + "\":\n")
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, "  - "+name)
		}
		slices.Sort(names)
		b.WriteString("\nMissing required color tokens:\n")
		b.WriteString(strings.Join(names, "\n"))
		b.WriteString("\n\nPlease add these colors to your theme's \"colors\" object.")
		b.WriteString("\nSee the built-in themes (dark.json, light.json) for reference values.")
	}
	if len(other) > 0 {
		b.WriteString("\n\nOther errors:\n")
		b.WriteString(strings.Join(other, "\n"))
	}
	return b.String()
}

// themeObjectKeys returns the keys of the top-level object member named field
// in document order, keeping the first position of a duplicated key as a
// JavaScript object does.
func themeObjectKeys(data []byte, field string) []string {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(root[field]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return keys
		}
		key, _ := token.(string)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
		var skip json.RawMessage
		if decoder.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}

// jsPropertyOrder orders keys as JavaScript enumerates an object's own
// properties: array-index keys ascending, then the rest in insertion order.
func jsPropertyOrder(keys []string) []string {
	var indexes []uint64
	var names []string
	for _, key := range keys {
		if n, err := strconv.ParseUint(key, 10, 32); err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == key {
			indexes = append(indexes, n)
			continue
		}
		names = append(names, key)
	}
	slices.Sort(indexes)
	ordered := make([]string, 0, len(keys))
	for _, n := range indexes {
		ordered = append(ordered, strconv.FormatUint(n, 10))
	}
	return append(ordered, names...)
}
