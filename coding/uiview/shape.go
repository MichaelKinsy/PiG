package uiview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
)

// Shape is the closed JSON subset an intent argument or answer is checked against: boolean, string, integer, number,
// enum (at most 32 strings), object (at most 32 properties, no extra members) and array (at most 64 items), nested to
// depth 4.
type Shape struct {
	Type       string           `json:"type"`
	Enum       []string         `json:"enum,omitempty"`
	Properties map[string]Shape `json:"properties,omitempty"`
	Required   []string         `json:"required,omitempty"`
	Items      *Shape           `json:"items,omitempty"`
	MaxItems   int              `json:"maxItems,omitempty"`
}

const (
	maxShapeDepth = 4
	maxEnum       = 32
	maxProperties = 32
	maxItemsCap   = 64
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
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return s.validate(v, "value")
}

func (s Shape) validate(v any, at string) error {
	switch s.Type {
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s is not a boolean", at)
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s is not a string", at)
		}
	case "enum":
		str, ok := v.(string)
		if !ok || !slices.Contains(s.Enum, str) {
			return fmt.Errorf("%s is not one of the enum values", at)
		}
	case "number", "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s is not a number", at)
		}
		f, err := n.Float64()
		if err != nil || math.IsInf(f, 0) {
			return fmt.Errorf("%s is not a finite number", at)
		}
		if s.Type == "integer" && (f != math.Trunc(f) || math.Abs(f) > 1<<53-1) {
			return fmt.Errorf("%s is not a safe integer", at)
		}
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", at)
		}
		for name, val := range obj {
			p, known := s.Properties[name]
			if !known {
				return fmt.Errorf("%s has unknown member %q", at, name)
			}
			if err := p.validate(val, at+"."+name); err != nil {
				return err
			}
		}
		for _, name := range s.Required {
			if _, ok := obj[name]; !ok {
				return fmt.Errorf("%s lacks required member %q", at, name)
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s is not an array", at)
		}
		if len(arr) > s.MaxItems {
			return fmt.Errorf("%s has %d items, at most %d", at, len(arr), s.MaxItems)
		}
		for i, val := range arr {
			if err := s.Items.validate(val, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
