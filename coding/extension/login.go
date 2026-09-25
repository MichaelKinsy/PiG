package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D60): Pig validates a typed login definition because
// subprocess extensions cannot pass Pi's live TUI component factories.
const (
	LoginBrandWidth            = 41
	LoginBrandHeight           = 5
	LoginHeroWidth             = 32
	LoginHeroHeight            = 14
	LoginMascotWidth           = 16
	LoginMascotHeight          = 14
	LoginPaletteLimit          = 32
	LoginNameWidthLimit        = 24
	LoginDescriptionWidthLimit = 48
	LoginTaglineWidthLimit     = 76
	LoginMetadataWidthLimit    = 80
)

// LoginDefinition describes one login using Pig's fixed native template.
// Grid cells are printable ASCII palette symbols; '.' is transparent.
type LoginDefinition struct {
	Brand       []string          `json:"brand"`
	Hero        []string          `json:"hero"`
	Mascot      []string          `json:"mascot"`
	Palette     map[string]string `json:"palette"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Tagline     string            `json:"tagline"`
}

// LoginDefinitionError identifies the invalid field in a login definition.
type LoginDefinitionError struct {
	Field   string
	Message string
}

func (e *LoginDefinitionError) Error() string {
	return fmt.Sprintf("invalid login definition %s: %s", e.Field, e.Message)
}

// ValidatedLoginDefinition is an immutable, renderer-ready login definition.
type ValidatedLoginDefinition struct {
	brand       [LoginBrandHeight]string
	hero        [LoginHeroHeight]string
	mascot      [LoginMascotHeight]string
	palette     map[byte]color.RGBA
	name        string
	description string
	tagline     string
}

func (d ValidatedLoginDefinition) Brand() []string {
	return append([]string(nil), d.brand[:]...)
}

func (d ValidatedLoginDefinition) Hero() []string {
	return append([]string(nil), d.hero[:]...)
}

func (d ValidatedLoginDefinition) Mascot() []string {
	return append([]string(nil), d.mascot[:]...)
}

func (d ValidatedLoginDefinition) Color(symbol byte) (color.RGBA, bool) {
	value, ok := d.palette[symbol]
	return value, ok
}

func (d ValidatedLoginDefinition) Name() string        { return d.name }
func (d ValidatedLoginDefinition) Description() string { return d.description }
func (d ValidatedLoginDefinition) Tagline() string     { return d.tagline }

// DecodeLoginDefinitionJSON strictly decodes and validates the current login
// wire shape. Duplicate and unknown object fields are rejected.
func DecodeLoginDefinitionJSON(data []byte) (ValidatedLoginDefinition, error) {
	if err := rejectDuplicateLoginJSONFields(data); err != nil {
		return ValidatedLoginDefinition{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var definition LoginDefinition
	if err := decoder.Decode(&definition); err != nil {
		return ValidatedLoginDefinition{}, fmt.Errorf("decode login definition: %w", err)
	}
	if err := requireLoginJSONEOF(decoder); err != nil {
		return ValidatedLoginDefinition{}, err
	}
	return ValidateLoginDefinition(definition)
}

func rejectDuplicateLoginJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanLoginJSONValue(decoder, ""); err != nil {
		return err
	}
	return requireLoginJSONEOF(decoder)
}

func scanLoginJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode login definition: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode login definition: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("decode login definition: object key is not a string")
			}
			field := loginJSONFieldPath(path, key)
			if _, duplicate := seen[key]; duplicate {
				return loginDefinitionError(field, "field occurs more than once")
			}
			seen[key] = struct{}{}
			if err := scanLoginJSONValue(decoder, field); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode login definition: %w", err)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := scanLoginJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("decode login definition: %w", err)
		}
	default:
		return fmt.Errorf("decode login definition: unexpected delimiter %q", delim)
	}
	return nil
}

func loginJSONFieldPath(parent, key string) string {
	if parent == "" {
		return key
	}
	if parent == "palette" {
		return fmt.Sprintf("palette[%s]", key)
	}
	return parent + "." + key
}

func requireLoginJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode login definition: trailing JSON value")
		}
		return fmt.Errorf("decode login definition: %w", err)
	}
	return nil
}

// ValidateLoginDefinition validates and defensively copies a login definition.
func ValidateLoginDefinition(definition LoginDefinition) (ValidatedLoginDefinition, error) {
	brand, used, err := validateLoginGrid("brand", definition.Brand, LoginBrandWidth, LoginBrandHeight, nil)
	if err != nil {
		return ValidatedLoginDefinition{}, err
	}
	hero, used, err := validateLoginGrid("hero", definition.Hero, LoginHeroWidth, LoginHeroHeight, used)
	if err != nil {
		return ValidatedLoginDefinition{}, err
	}
	mascot, used, err := validateLoginGrid("mascot", definition.Mascot, LoginMascotWidth, LoginMascotHeight, used)
	if err != nil {
		return ValidatedLoginDefinition{}, err
	}

	palette, err := validateLoginPalette(definition.Palette, used)
	if err != nil {
		return ValidatedLoginDefinition{}, err
	}
	if err := validateLoginText("name", definition.Name, LoginNameWidthLimit); err != nil {
		return ValidatedLoginDefinition{}, err
	}
	if err := validateLoginText("description", definition.Description, LoginDescriptionWidthLimit); err != nil {
		return ValidatedLoginDefinition{}, err
	}
	if err := validateLoginText("tagline", definition.Tagline, LoginTaglineWidthLimit); err != nil {
		return ValidatedLoginDefinition{}, err
	}
	metadataWidth := 2 + widthx.VisibleWidth(definition.Name) + 2 + widthx.VisibleWidth(definition.Description)
	if metadataWidth > LoginMetadataWidthLimit {
		return ValidatedLoginDefinition{}, loginDefinitionError("name", "name and description exceed 80 columns")
	}

	validated := ValidatedLoginDefinition{
		palette:     palette,
		name:        definition.Name,
		description: definition.Description,
		tagline:     definition.Tagline,
	}
	copy(validated.brand[:], brand)
	copy(validated.hero[:], hero)
	copy(validated.mascot[:], mascot)
	return validated, nil
}

func validateLoginGrid(field string, rows []string, width, height int, used map[byte]string) ([]string, map[byte]string, error) {
	if len(rows) != height {
		return nil, nil, loginDefinitionError(field, fmt.Sprintf("must contain exactly %d rows", height))
	}
	if used == nil {
		used = make(map[byte]string)
	}
	out := make([]string, len(rows))
	for y, row := range rows {
		rowField := fmt.Sprintf("%s[%d]", field, y)
		for x := range len(row) {
			if row[x] > unicode.MaxASCII {
				return nil, nil, loginDefinitionError(rowField, "must contain only ASCII symbols")
			}
		}
		if len(row) != width {
			return nil, nil, loginDefinitionError(rowField, fmt.Sprintf("must contain exactly %d symbols", width))
		}
		out[y] = row
		for x, symbol := range []byte(row) {
			if symbol == '.' {
				continue
			}
			if _, ok := used[symbol]; !ok {
				used[symbol] = fmt.Sprintf("%s[%d]", rowField, x)
			}
		}
	}
	return out, used, nil
}

func validateLoginPalette(input map[string]string, used map[byte]string) (map[byte]color.RGBA, error) {
	if len(input) == 0 {
		return nil, loginDefinitionError("palette", "must not be empty")
	}
	if len(input) > LoginPaletteLimit {
		return nil, loginDefinitionError("palette", fmt.Sprintf("must contain at most %d colors", LoginPaletteLimit))
	}

	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	palette := make(map[byte]color.RGBA, len(input))
	for _, key := range keys {
		field := fmt.Sprintf("palette[%s]", key)
		if len(key) != 1 || key[0] > unicode.MaxASCII || key[0] <= ' ' || key[0] == unicode.MaxASCII {
			return nil, loginDefinitionError(field, "key must be one printable non-space ASCII character")
		}
		if key == "." {
			return nil, loginDefinitionError(field, "'.' is reserved for transparency")
		}
		if _, ok := used[key[0]]; !ok {
			return nil, loginDefinitionError(field, "symbol is not used by any grid")
		}
		parsed, err := parseLoginColor(input[key])
		if err != nil {
			return nil, loginDefinitionError(field, err.Error())
		}
		palette[key[0]] = parsed
	}

	usedSymbols := make([]byte, 0, len(used))
	for symbol := range used {
		usedSymbols = append(usedSymbols, symbol)
	}
	sort.Slice(usedSymbols, func(i, j int) bool {
		left, right := used[usedSymbols[i]], used[usedSymbols[j]]
		if left == right {
			return usedSymbols[i] < usedSymbols[j]
		}
		return left < right
	})
	for _, symbol := range usedSymbols {
		if _, ok := palette[symbol]; !ok {
			return nil, loginDefinitionError(used[symbol], fmt.Sprintf("symbol %q has no palette color", symbol))
		}
	}
	return palette, nil
}

func parseLoginColor(value string) (color.RGBA, error) {
	if len(value) != 7 || value[0] != '#' {
		return color.RGBA{}, fmt.Errorf("color must use #RRGGBB")
	}
	parsed, err := strconv.ParseUint(value[1:], 16, 24)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("color must use #RRGGBB")
	}
	return color.RGBA{
		R: uint8(parsed >> 16),
		G: uint8(parsed >> 8),
		B: uint8(parsed),
		A: 0xFF,
	}, nil
}

func validateLoginText(field, value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return loginDefinitionError(field, "must not be empty")
	}
	if !utf8.ValidString(value) {
		return loginDefinitionError(field, "must be valid UTF-8")
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return loginDefinitionError(field, "must not contain control or line-separator characters")
		}
	}
	if width := widthx.VisibleWidth(value); width > limit {
		return loginDefinitionError(field, fmt.Sprintf("visible width %d exceeds %d", width, limit))
	}
	return nil
}

func loginDefinitionError(field, message string) error {
	return &LoginDefinitionError{Field: field, Message: message}
}
