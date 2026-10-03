package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"maps"
	"strings"
)

// pig divergence (D2): an extension adds a sprite to PiG's /sprite catalogue with the same kind of data as its native login
// (D60): pixel grids of palette symbols, a palette and display text. The host validates it and draws it; a sprite never
// replaces the header by itself.
const SpriteIDLimit = 32

// SpriteDefinition is one sprite: its stable ID, the name and tagline /sprite lists, its pig (LoginMascotWidth by
// LoginMascotHeight), which the startup header draws in Pi's logo slot and /sprite preview beside the wordmark, and the
// palette that colors it. Grid cells are printable ASCII palette symbols; '.' is transparent.
type SpriteDefinition struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Tagline string            `json:"tagline"`
	Mascot  []string          `json:"mascot"`
	Palette map[string]string `json:"palette"`
}

// SpriteDefinitionError identifies the invalid field in a sprite definition.
type SpriteDefinitionError struct {
	Field   string
	Message string
}

func (e *SpriteDefinitionError) Error() string {
	return fmt.Sprintf("invalid sprite definition %s: %s", e.Field, e.Message)
}

// ValidatedSpriteDefinition is an immutable, renderer-ready sprite definition.
type ValidatedSpriteDefinition struct {
	id, name, tagline string
	mascot            [LoginMascotHeight]string
	palette           map[byte]color.RGBA
}

func (d ValidatedSpriteDefinition) ID() string      { return d.id }
func (d ValidatedSpriteDefinition) Name() string    { return d.name }
func (d ValidatedSpriteDefinition) Tagline() string { return d.tagline }
func (d ValidatedSpriteDefinition) Mascot() []string {
	return append([]string(nil), d.mascot[:]...)
}

// Palette returns a copy of the sprite's colors by symbol.
func (d ValidatedSpriteDefinition) Palette() map[byte]color.RGBA { return maps.Clone(d.palette) }

// SpriteRegistrar is a UI that keeps extension sprites: the interactive mode adds them to /sprite. owner names the
// extension; a sprite's ID is unique across owners, and an owner registering an ID again replaces its sprite.
type SpriteRegistrar interface {
	RegisterSprite(owner string, definition ValidatedSpriteDefinition) error
	UnregisterSprites(owner string)
}

// ValidateSpriteDefinition validates and defensively copies a sprite definition. The grids, palette and text follow the
// login definition's rules (ValidateLoginDefinition); the name has the login name's width and the tagline the login
// tagline's.
func ValidateSpriteDefinition(definition SpriteDefinition) (ValidatedSpriteDefinition, error) {
	if err := validateSpriteID(definition.ID); err != nil {
		return ValidatedSpriteDefinition{}, err
	}
	mascot, used, err := validateLoginGrid("mascot", definition.Mascot, LoginMascotWidth, LoginMascotHeight, nil)
	if err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	palette, err := validateLoginPalette(definition.Palette, used)
	if err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	if err := validateLoginText("name", definition.Name, LoginNameWidthLimit); err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	if err := validateLoginText("tagline", definition.Tagline, LoginTaglineWidthLimit); err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	validated := ValidatedSpriteDefinition{id: definition.ID, name: definition.Name, tagline: definition.Tagline, palette: palette}
	copy(validated.mascot[:], mascot)
	return validated, nil
}

// DecodeSpriteDefinitionJSON strictly decodes and validates the sprite wire shape. Duplicate and unknown object fields are
// rejected.
func DecodeSpriteDefinitionJSON(data []byte) (ValidatedSpriteDefinition, error) {
	if err := rejectDuplicateLoginJSONFields(data); err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var definition SpriteDefinition
	if err := decoder.Decode(&definition); err != nil {
		return ValidatedSpriteDefinition{}, fmt.Errorf("decode sprite definition: %w", err)
	}
	if err := requireLoginJSONEOF(decoder); err != nil {
		return ValidatedSpriteDefinition{}, spriteError(err)
	}
	return ValidateSpriteDefinition(definition)
}

// validateSpriteID accepts a lowercase slug, the form of the built-in IDs: ASCII letters, digits and inner hyphens.
func validateSpriteID(id string) error {
	if id == "" || len(id) > SpriteIDLimit {
		return &SpriteDefinitionError{Field: "id", Message: fmt.Sprintf("must be 1 to %d characters", SpriteIDLimit)}
	}
	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return &SpriteDefinitionError{Field: "id", Message: "must contain only lowercase ASCII letters, digits and hyphens"}
		}
	}
	if id[0] == '-' || id[len(id)-1] == '-' {
		return &SpriteDefinitionError{Field: "id", Message: "must not start or end with a hyphen"}
	}
	return nil
}

// spriteError names the sprite definition in an error of the shared login validators.
func spriteError(err error) error {
	if field, ok := errors.AsType[*LoginDefinitionError](err); ok {
		return &SpriteDefinitionError{Field: field.Field, Message: field.Message}
	}
	if message, ok := strings.CutPrefix(err.Error(), "decode login definition: "); ok {
		return fmt.Errorf("decode sprite definition: %s", message)
	}
	return err
}
