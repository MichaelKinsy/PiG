package theme

// Copied from coding-agent src/modes/interactive/theme/theme-schema.ts (p2-tui-port.md section 5.3): the theme document and its validation.
// The schema checks are PiG's port of the same schema (tui.ValidateThemeJSON), which this file applies to the raw document.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/tui"
)

// ColorValue is theme-schema.ts ColorValue: a hex, OKLCH or OKHSL color, a variable reference or "" (terminal default), or a 256-color palette index.
type ColorValue struct {
	Text    string
	Index   int
	IsIndex bool
}

// Str is a string ColorValue.
func Str(text string) ColorValue { return ColorValue{Text: text} }

// Idx is a 256-color palette index ColorValue.
func Idx(index int) ColorValue { return ColorValue{Index: index, IsIndex: true} }

// MarshalJSON writes the string or number the value was read from.
func (v ColorValue) MarshalJSON() ([]byte, error) {
	if v.IsIndex {
		return json.Marshal(v.Index)
	}
	return json.Marshal(v.Text)
}

// UnmarshalJSON reads a JSON string or an integral number.
func (v *ColorValue) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*v = ColorValue{Text: text}
		return nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err != nil || number != float64(int(number)) {
		return fmt.Errorf("theme color must be a string or a 256-color index: %s", data)
	}
	*v = ColorValue{Index: int(number), IsIndex: true}
	return nil
}

// NamedColor is one member of a theme's colors object; the members keep the document's order, in which a theme reports its first failure.
type NamedColor struct {
	Name  string
	Value ColorValue
}

// OrderedColors is a JSON object of colors in document order (a repeated key keeps its first position and its last value, as a JS object).
type OrderedColors []NamedColor

// Get returns a member's value.
func (o OrderedColors) Get(name string) (ColorValue, bool) {
	for _, entry := range o {
		if entry.Name == name {
			return entry.Value, true
		}
	}
	return ColorValue{}, false
}

// With returns the colors with name set to value, in place when present and last otherwise (a JS `{...colors, name: value}`).
func (o OrderedColors) With(name string, value ColorValue) OrderedColors {
	out := slices.Clone(o)
	for i := range out {
		if out[i].Name == name {
			out[i].Value = value
			return out
		}
	}
	return append(out, NamedColor{Name: name, Value: value})
}

// MarshalJSON writes the members in order.
func (o OrderedColors) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, entry := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(entry.Name)
		value, err := entry.Value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// UnmarshalJSON reads the members in document order.
func (o *OrderedColors) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("theme colors must be an object")
	}
	var out OrderedColors
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		var value ColorValue
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		name := key.(string)
		if i := slices.IndexFunc(out, func(entry NamedColor) bool { return entry.Name == name }); i >= 0 {
			out[i].Value = value
		} else {
			out = append(out, NamedColor{Name: name, Value: value})
		}
	}
	*o = out
	return nil
}

// ThemeJSON is theme-schema.ts ValidatedThemeJson.
type ThemeJSON struct {
	Name       string            `json:"name"`
	Appearance tui.TerminalTheme `json:"appearance,omitempty"`
	Vars       map[string]ColorValue
	Colors     OrderedColors `json:"colors"`
	Export     OrderedColors `json:"export,omitempty"`
}

// themeJSONWire is ThemeJSON's wire form, with the "vars" member present only when set.
type themeJSONWire struct {
	Schema     string                `json:"$schema,omitempty"`
	Name       string                `json:"name"`
	Appearance tui.TerminalTheme     `json:"appearance,omitempty"`
	Vars       map[string]ColorValue `json:"vars,omitempty"`
	Colors     OrderedColors         `json:"colors"`
	Export     OrderedColors         `json:"export,omitempty"`
}

// MarshalJSON writes the theme document.
func (t ThemeJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(themeJSONWire{Name: t.Name, Appearance: t.Appearance, Vars: t.Vars, Colors: t.Colors, Export: t.Export})
}

// UnmarshalJSON reads a theme document without validating it; ValidateThemeJSON does.
func (t *ThemeJSON) UnmarshalJSON(data []byte) error {
	var wire themeJSONWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*t = ThemeJSON{Name: wire.Name, Appearance: wire.Appearance, Vars: wire.Vars, Colors: wire.Colors, Export: wire.Export}
	return nil
}

// WithColor returns the theme with one color replaced (a JS `{...theme, colors: {...theme.colors, name: value}}`).
func (t ThemeJSON) WithColor(name string, value ColorValue) ThemeJSON {
	t.Colors = t.Colors.With(name, value)
	return t
}

// ValidateThemeJSON is validateThemeJson: it validates one theme document, returning an error whose message names the offending tokens.
func ValidateThemeJSON(label string, data []byte) (ThemeJSON, error) {
	if err := tui.ValidateThemeJSON(label, data); err != nil {
		return ThemeJSON{}, err
	}
	var theme ThemeJSON
	if err := json.Unmarshal(data, &theme); err != nil {
		return ThemeJSON{}, err
	}
	return theme, nil
}
