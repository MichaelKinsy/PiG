package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

type modelDataObject struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseModelDataObject(data []byte) *modelDataObject {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	object := &modelDataObject{values: map[string]json.RawMessage{}}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil
		}
		key, ok := token.(string)
		if !ok {
			return nil
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil
		}
		if _, exists := object.values[key]; !exists {
			object.keys = append(object.keys, key)
		}
		object.values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	orderModelIntegerKeys(object.keys)
	return object
}

func (o *modelDataObject) get(key string) json.RawMessage {
	if o == nil {
		return nil
	}
	return o.values[key]
}

func (o *modelDataObject) string(key string) string {
	value, _ := modelDataString(o.get(key))
	return value
}

func modelDataString(raw json.RawMessage) (string, bool) {
	var value string
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func sortModelStrings(values []string) {
	slices.SortFunc(values, func(a, b string) int { return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b))) })
}

func sortedModelObjectKeys[V any](values map[string]V) []string {
	keys := slices.Collect(maps.Keys(values))
	sortModelStrings(keys)
	orderModelIntegerKeys(keys)
	return keys
}

// Object.fromEntries and JSON.parse enumerate canonical array-index keys before other properties.
func orderModelIntegerKeys(keys []string) {
	index := func(key string) (uint64, bool) {
		n, err := strconv.ParseUint(key, 10, 32)
		return n, err == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == key
	}
	slices.SortStableFunc(keys, func(a, b string) int {
		x, xok := index(a)
		y, yok := index(b)
		if xok && yok {
			return cmp.Compare(x, y)
		}
		if xok {
			return -1
		}
		if yok {
			return 1
		}
		return 0
	})
}

func modelDataQuote(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func modelDataValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "undefined"
	}
	if value, ok := modelDataString(raw); ok {
		return modelDataQuote(value)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return string(raw)
	}
	return compact.String()
}

func modelDataNumber(raw json.RawMessage) (float64, bool) {
	var value float64
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0)
}

func modelDataNumberEquals(raw json.RawMessage, want int) bool {
	got, ok := modelDataNumber(raw)
	return ok && got == float64(want)
}

func modelDataTimestampValid(raw json.RawMessage) (bool, error) {
	value, ok := modelDataString(raw)
	if !ok {
		return false, nil
	}
	// Generated stamps are canonical UTC. Node owns Date.parse's legacy and overflow grammar for other strings.
	if parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value); err == nil && parsed.Format("2006-01-02T15:04:05.000Z") == value {
		return true, nil
	}
	result, err := runModelDataNode(`const fs = require("node:fs"); process.stdout.write(String(!Number.isNaN(Date.parse(fs.readFileSync(0, "utf8")))));`, []byte(value))
	return result == "true", err
}

func runModelDataNode(script string, input []byte) (string, error) {
	command := exec.Command("node", "-e", script)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("model data JavaScript value validation: %w", err)
	}
	return string(output), nil
}

// modelDataJSString is JavaScript's String(value) for a decoded JSON value.
func modelDataJSString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "undefined"
	}
	switch raw[0] {
	case '"':
		value, _ := modelDataString(raw)
		return value
	case '{':
		return "[object Object]"
	case '[':
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil {
			return string(raw)
		}
		parts := make([]string, len(elements))
		for i, element := range elements {
			if trimmed := bytes.TrimSpace(element); !bytes.Equal(trimmed, []byte("null")) {
				parts[i] = modelDataJSString(element)
			}
		}
		return strings.Join(parts, ",")
	case 't', 'f', 'n':
		return string(raw)
	}
	return jsnumber.String(jsnumber.FromJSON(raw))
}

func modelDataModalityList(raw json.RawMessage) ([]string, bool) {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, false
	}
	list := make([]string, len(entries))
	for i, entry := range entries {
		value, ok := modelDataString(entry)
		if !ok || (value != "text" && value != "image") {
			return nil, false
		}
		list[i] = value
	}
	return list, true
}

func modelDataPositiveNumber(raw json.RawMessage) bool {
	value, ok := modelDataNumber(raw)
	return ok && value > 0
}

func validateModelValue(raw json.RawMessage, provider, id, api string, errs *[]string) {
	label := provider + "/" + id
	model := parseModelDataObject(raw)
	if model == nil {
		*errs = append(*errs, label+" must be an object")
		return
	}
	for _, field := range []struct{ key, want string }{{"id", id}, {"provider", provider}, {"api", api}} {
		if value, ok := modelDataString(model.get(field.key)); !ok || value != field.want {
			*errs = append(*errs, label+" has "+field.key+" "+modelDataValue(model.get(field.key))+", expected "+modelDataQuote(field.want))
		}
	}
	if name, ok := modelDataString(model.get("name")); !ok || name == "" {
		*errs = append(*errs, label+" has no model name")
	}
	if _, ok := modelDataString(model.get("baseUrl")); !ok {
		*errs = append(*errs, label+" has no baseUrl string")
	}
	if _, ok := modelDataModalityList(model.get("input")); !ok {
		*errs = append(*errs, label+" has invalid input modalities")
	}
	modelType, _ := modelDataString(model.get("type"))
	if modelType == "image" {
		if output, ok := modelDataModalityList(model.get("output")); !ok || !slices.Contains(output, "image") {
			*errs = append(*errs, label+" has invalid output modalities")
		}
	} else if model.get("output") != nil {
		*errs = append(*errs, label+" has unsupported output modalities")
	}
	switch modelType {
	case "chat":
		if value := string(bytes.TrimSpace(model.get("reasoning"))); value != "true" && value != "false" {
			*errs = append(*errs, label+" has no reasoning boolean")
		}
		for _, field := range []string{"contextWindow", "maxTokens"} {
			if !modelDataPositiveNumber(model.get(field)) {
				*errs = append(*errs, label+" has invalid "+field)
			}
		}
	case "classifier":
		if !modelDataPositiveNumber(model.get("contextWindow")) {
			*errs = append(*errs, label+" has invalid contextWindow")
		}
	case "image":
	default:
		*errs = append(*errs, label+" has type "+modelDataValue(model.get("type"))+`, expected "chat", "image", or "classifier"`)
	}
	cost := parseModelDataObject(model.get("cost"))
	if cost == nil {
		*errs = append(*errs, label+" has invalid cost metadata")
	} else {
		for _, field := range []string{"input", "output", "cacheRead", "cacheWrite"} {
			if _, ok := modelDataNumber(cost.get(field)); !ok {
				*errs = append(*errs, label+" has invalid cost."+field)
			}
		}
	}
}
