package uiview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// Shape is the closed JSON subset an intent argument or answer is checked against: boolean, string, integer, number,
// enum (at most 32 strings), object (at most 32 properties, no extra members) and array (at most 64 items), nested to
// depth 4.
type Shape struct {
	Type string `json:"type"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Enum []string `json:"enum,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty map mean the same: none
	Properties map[string]Shape `json:"properties,omitempty"`
	//portlint:allow emptydrop the D614 records are PiG-owned with no Pi counterpart, and an absent and an empty list mean the same: none
	Required []string `json:"required,omitempty"`
	Items    *Shape   `json:"items,omitempty"`
	MaxItems int      `json:"maxItems,omitempty"`
}

// pig additive (D614): the closed argument and answer shape limits of a view intent.
const (
	maxShapeDepth = 4  // pig additive (D614): nesting depth
	maxEnum       = 32 // pig additive (D614): enum members
	maxProperties = 32 // pig additive (D614): object properties
	maxItemsCap   = 64 // pig additive (D614): array length
)

func (s Shape) check() error { return s.checkDepth(1) }

func (s Shape) checkDepth(depth int) error {
	if depth > maxShapeDepth {
		return fmt.Errorf("shape is nested deeper than %d", maxShapeDepth)
	}
	switch s.Type {
	case "boolean", "string", "integer", "number":
		if len(s.Enum) > 0 || len(s.Properties) > 0 || s.Items != nil {
			return fmt.Errorf("%s takes no enum, properties or items", s.Type)
		}
	case "enum":
		if n := len(s.Enum); n < 1 || n > maxEnum {
			return fmt.Errorf("enum needs 1..%d values", maxEnum)
		}
	case "object":
		if len(s.Properties) > maxProperties {
			return fmt.Errorf("object takes at most %d properties", maxProperties)
		}
		for name, p := range s.Properties {
			if err := p.checkDepth(depth + 1); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		for _, name := range s.Required {
			if _, ok := s.Properties[name]; !ok {
				return fmt.Errorf("required %q is not a property", name)
			}
		}
	case "array":
		if s.Items == nil {
			return errors.New("array needs items")
		}
		if s.MaxItems < 1 || s.MaxItems > maxItemsCap {
			return fmt.Errorf("array maxItems must be 1..%d", maxItemsCap)
		}
		return s.Items.checkDepth(depth + 1)
	default:
		return fmt.Errorf("unknown shape type %q", s.Type)
	}
	return nil
}

// Validate reports whether raw is a value of the shape.
func (s Shape) Validate(raw json.RawMessage) error {
	if !json.Valid(raw) {
		return errors.New("not one JSON value")
	}
	return s.validate(bytes.TrimSpace(raw), &shapePath{name: "value"})
}

// shapePath names the value validate is checking: the root, a member of an object or an item of an array. Its text is
// built only for an error, so a value that passes formats no path.
type shapePath struct {
	parent *shapePath
	name   string // the root's text, or the member's name
	index  int    // the item's index when item is set
	item   bool
}

func (p *shapePath) String() string { return string(p.appendTo(nil)) }

func (p *shapePath) appendTo(b []byte) []byte {
	if p.parent == nil {
		return append(b, p.name...)
	}
	b = p.parent.appendTo(b)
	if p.item {
		b = append(b, '[')
		b = strconv.AppendInt(b, int64(p.index), 10)
		return append(b, ']')
	}
	b = append(b, '.')
	return append(b, p.name...)
}

// validate checks one valid JSON value against the shape. It decodes objects and arrays one level at a time into raw
// members, so no value is decoded into an interface.
func (s Shape) validate(raw []byte, at *shapePath) error {
	first := raw[0]
	switch s.Type {
	case "boolean":
		if first != 't' && first != 'f' {
			return fmt.Errorf("%s is not a boolean", at.String())
		}
	case "string":
		if first != '"' {
			return fmt.Errorf("%s is not a string", at.String())
		}
	case "enum":
		var str string
		if first != '"' || json.Unmarshal(raw, &str) != nil || !slices.Contains(s.Enum, str) {
			return fmt.Errorf("%s is not one of the enum values", at.String())
		}
	case "number", "integer":
		if first != '-' && (first < '0' || first > '9') {
			return fmt.Errorf("%s is not a number", at.String())
		}
		f, err := strconv.ParseFloat(string(raw), 64)
		if err != nil || math.IsInf(f, 0) {
			return fmt.Errorf("%s is not a finite number", at.String())
		}
		if s.Type == "integer" && (f != math.Trunc(f) || math.Abs(f) > 1<<53-1) {
			return fmt.Errorf("%s is not a safe integer", at.String())
		}
	case "object":
		var obj map[string]json.RawMessage
		if first != '{' || json.Unmarshal(raw, &obj) != nil {
			return fmt.Errorf("%s is not an object", at.String())
		}
		for name, val := range obj {
			p, known := s.Properties[name]
			if !known {
				return fmt.Errorf("%s has unknown member %q", at.String(), name)
			}
			if err := p.validate(bytes.TrimSpace(val), &shapePath{parent: at, name: name}); err != nil {
				return err
			}
		}
		for _, name := range s.Required {
			if _, ok := obj[name]; !ok {
				return fmt.Errorf("%s lacks required member %q", at.String(), name)
			}
		}
	case "array":
		var arr []json.RawMessage
		if first != '[' || json.Unmarshal(raw, &arr) != nil {
			return fmt.Errorf("%s is not an array", at.String())
		}
		if len(arr) > s.MaxItems {
			return fmt.Errorf("%s has %d items, at most %d", at.String(), len(arr), s.MaxItems)
		}
		for i, val := range arr {
			if err := s.Items.validate(bytes.TrimSpace(val), &shapePath{parent: at, index: i, item: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
