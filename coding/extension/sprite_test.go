package extension

import (
	"errors"
	"image/color"
	"slices"
	"strings"
	"testing"
)

func validSpriteDefinition() SpriteDefinition {
	return SpriteDefinition{
		ID:      "blue-pig",
		Name:    "Blue PiG",
		Tagline: "A sprite from an extension.",
		Mascot: []string{
			"................",
			"...OOOO..OOOO...",
			"...OeeO..OeeO...",
			"..OeePPPPPPeeO..",
			".OPPPpPPPPpPPPO.",
			".OPPWWPPPPWWPPO.",
			".OPPWKPPPPKWPPO.",
			".OPbbPssssPbbPO.",
			".OPPPsKssKsPPPO.",
			".OPPPPssssPPPPO.",
			".OPPPPPPPPPPPPO.",
			"..OPPPPPPPPPPO..",
			"...OOOOOOOOOO...",
			"................",
		},
		Palette: map[string]string{
			"O": "#18141E", "K": "#18141E", "W": "#FFFFFF", "P": "#5B8DEF",
			"p": "#8FB2F5", "s": "#3D6BC4", "b": "#F49AA6", "e": "#4A7BD8",
		},
	}
}

func TestValidateSpriteDefinitionCopiesAcceptedInput(t *testing.T) {
	definition := validSpriteDefinition()
	validated, err := ValidateSpriteDefinition(definition)
	if err != nil {
		t.Fatalf("ValidateSpriteDefinition() error = %v", err)
	}
	definition.Mascot[1] = "XXXXXXXXXXXXXXXX"
	definition.Palette["P"] = "#000000"
	if validated.Mascot()[1] != "...OOOO..OOOO..." || validated.Palette()['P'] != (color.RGBA{0x5B, 0x8D, 0xEF, 0xFF}) {
		t.Fatalf("validated definition shares the caller's data: %q %v", validated.Mascot()[1], validated.Palette()['P'])
	}
	if validated.ID() != "blue-pig" || validated.Name() != "Blue PiG" || validated.Tagline() != "A sprite from an extension." || len(validated.Mascot()) != LoginMascotHeight {
		t.Fatalf("validated = %+v", validated)
	}
}

func TestValidateSpriteDefinitionRejectsInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*SpriteDefinition)
		field  string
	}{
		{"empty id", func(d *SpriteDefinition) { d.ID = "" }, "id"},
		{"long id", func(d *SpriteDefinition) { d.ID = strings.Repeat("a", SpriteIDLimit+1) }, "id"},
		{"uppercase id", func(d *SpriteDefinition) { d.ID = "Blue" }, "id"},
		{"space in id", func(d *SpriteDefinition) { d.ID = "blue pig" }, "id"},
		{"leading hyphen", func(d *SpriteDefinition) { d.ID = "-blue" }, "id"},
		{"trailing hyphen", func(d *SpriteDefinition) { d.ID = "blue-" }, "id"},
		{"missing mascot", func(d *SpriteDefinition) { d.Mascot = nil }, "mascot"},
		{"short mascot", func(d *SpriteDefinition) { d.Mascot = d.Mascot[:6] }, "mascot"},
		{"wide mascot row", func(d *SpriteDefinition) { d.Mascot[2] += "." }, "mascot[2]"},
		{"non ASCII cell", func(d *SpriteDefinition) { d.Mascot[0] = "é" + d.Mascot[0][2:] }, "mascot[0]"},
		{"unresolved symbol", func(d *SpriteDefinition) { d.Mascot[0] = "X" + d.Mascot[0][1:] }, "mascot[0][0]"},
		{"unused palette symbol", func(d *SpriteDefinition) { d.Palette["Z"] = "#000000" }, "palette[Z]"},
		{"invalid color", func(d *SpriteDefinition) { d.Palette["P"] = "5B8DEF" }, "palette[P]"},
		{"empty name", func(d *SpriteDefinition) { d.Name = " " }, "name"},
		{"long tagline", func(d *SpriteDefinition) { d.Tagline = strings.Repeat("x", LoginTaglineWidthLimit+1) }, "tagline"},
		{"control character", func(d *SpriteDefinition) { d.Name = "bad\x1bname" }, "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := validSpriteDefinition()
			definition.Mascot = slices.Clone(definition.Mascot)
			tc.mutate(&definition)
			_, err := ValidateSpriteDefinition(definition)
			var spriteErr *SpriteDefinitionError
			if !errors.As(err, &spriteErr) || spriteErr.Field != tc.field {
				t.Fatalf("error = %v, want a sprite definition error on %s", err, tc.field)
			}
			if !strings.HasPrefix(err.Error(), "invalid sprite definition "+tc.field+": ") {
				t.Fatalf("error = %q", err)
			}
		})
	}
}

func TestDecodeSpriteDefinitionJSONIsStrict(t *testing.T) {
	valid := `{"id":"blue-pig","name":"Blue PiG","tagline":"t","mascot":["...............P","................","................","................","................","................","................","................","................","................","................","................","................","................"],"palette":{"P":"#5B8DEF"}}`
	if _, err := DecodeSpriteDefinitionJSON([]byte(valid)); err != nil {
		t.Fatalf("valid sprite: %v", err)
	}
	for name, input := range map[string]string{
		"unknown field":   strings.Replace(valid, `"tagline":"t"`, `"tagline":"t","extra":1`, 1),
		"duplicate field": strings.Replace(valid, `"name":"Blue PiG"`, `"name":"Blue PiG","name":"Other"`, 1),
		"trailing value":  valid + " {}",
		"not an object":   `[]`,
	} {
		_, err := DecodeSpriteDefinitionJSON([]byte(input))
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(err.Error(), "login") {
			t.Fatalf("%s: error names the login: %v", name, err)
		}
	}
}
