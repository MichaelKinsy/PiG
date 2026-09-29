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
	if value := string(bytes.TrimSpace(model.get("reasoning"))); value != "true" && value != "false" {
		*errs = append(*errs, label+" has no reasoning boolean")
	}
	var input []json.RawMessage
	validInput := json.Unmarshal(model.get("input"), &input) == nil && len(input) > 0
	for _, entry := range input {
		value, ok := modelDataString(entry)
		if !ok || (value != "text" && value != "image") {
			validInput = false
		}
	}
	if !validInput {
		*errs = append(*errs, label+" has invalid input modalities")
	}
	for _, field := range []string{"contextWindow", "maxTokens"} {
		if value, ok := modelDataNumber(model.get(field)); !ok || value <= 0 {
			*errs = append(*errs, label+" has invalid "+field)
		}
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
