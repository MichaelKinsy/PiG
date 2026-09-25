package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validLoginDefinition() LoginDefinition {
	return LoginDefinition{
		Brand: []string{
			".LLL..LLLLL.L...L.LLLLL.L...L..GGG..GGGGG",
			"L...L...L...L...L.L.....LL..L.G...G...G..",
			"LLLLL...L...LLLLL.LLLL..L.L.L.GGGGG...G..",
			"L...L...L...L...L.L.....L..LL.G...G...G..",
			"L...L...L...L...L.LLLLL.L...L.G...G.GGGGG",
		},
		Hero: []string{
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
			"11111111111111111111111111111111",
		},
		Mascot: []string{
			"...H........H...",
			"..HhOO....OOhH..",
			"..OeeO....OeeO..",
			".OeePPPPPPPPeeO.",
			".OPPpPPPPPPpPPO.",
			".OPPWWPPPPWWPPO.",
			".OPPWKPPPPKWPPO.",
			".OPbbPssssPbbPO.",
			".OTPPsKssKsPPTO.",
			".OTPPPssssPPPTO.",
			".OPPPPPPPPPPPPO.",
			"..OPPPPKKKKPPO..",
			"...OOOOOOOOOO...",
			"................",
		},
		Palette: map[string]string{
			"L": "#F4E9D0",
			"G": "#16C7B7",
			"1": "#E8B14A",
			"O": "#17120F",
			"K": "#17120F",
			"P": "#2B211C",
			"p": "#6D3F29",
			"s": "#B85C38",
			"b": "#B85C38",
			"e": "#5E2B2B",
			"H": "#EFDEAE",
			"T": "#EFDEAE",
			"W": "#B5BEBE",
			"h": "#BE842D",
		},
		Name:        "EXAMPLE",
		Description: "Generated coding agent",
		Tagline:     "Ancient appetite. Modern intelligence.",
	}
}

func TestValidateLoginDefinitionCopiesAcceptedInput(t *testing.T) {
	input := validLoginDefinition()

	validated, err := ValidateLoginDefinition(input)
	if err != nil {
		t.Fatalf("ValidateLoginDefinition() error = %v", err)
	}

	input.Brand[0] = strings.Repeat(".", LoginBrandWidth)
	input.Palette["L"] = "#000000"
	if got := validated.Brand()[0]; got != ".LLL..LLLLL.L...L.LLLLL.L...L..GGG..GGGGG" {
		t.Fatalf("validated brand changed with caller input: %q", got)
	}
	if got, ok := validated.Color('L'); !ok || got.R != 0xF4 || got.G != 0xE9 || got.B != 0xD0 || got.A != 0xFF {
		t.Fatalf("validated color L = %#v, %v", got, ok)
	}
}

func TestValidateLoginDefinitionRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*LoginDefinition)
		field  string
	}{
		{name: "missing brand", mutate: func(d *LoginDefinition) { d.Brand = nil }, field: "brand"},
		{name: "wrong brand width", mutate: func(d *LoginDefinition) { d.Brand[0] = d.Brand[0][:40] }, field: "brand[0]"},
		{name: "wrong hero height", mutate: func(d *LoginDefinition) { d.Hero = d.Hero[:6] }, field: "hero"},
		{name: "wrong mascot width", mutate: func(d *LoginDefinition) { d.Mascot[0] += "." }, field: "mascot[0]"},
		{name: "non ASCII grid cell", mutate: func(d *LoginDefinition) { d.Mascot[0] = "é" + d.Mascot[0][2:] }, field: "mascot[0]"},
		{name: "transparent palette entry", mutate: func(d *LoginDefinition) { d.Palette["."] = "#000000" }, field: "palette[.]"},
		{name: "space palette key", mutate: func(d *LoginDefinition) { d.Palette[" "] = "#000000" }, field: "palette[ ]"},
		{name: "multi byte palette key", mutate: func(d *LoginDefinition) { d.Palette["LL"] = "#000000" }, field: "palette[LL]"},
		{name: "unresolved symbol", mutate: func(d *LoginDefinition) { d.Mascot[0] = "X" + d.Mascot[0][1:] }, field: "mascot[0][0]"},
		{name: "unused symbol", mutate: func(d *LoginDefinition) { d.Palette["Z"] = "#000000" }, field: "palette[Z]"},
		{name: "invalid color", mutate: func(d *LoginDefinition) { d.Palette["L"] = "F4E9D0" }, field: "palette[L]"},
		{name: "too many colors", mutate: func(d *LoginDefinition) {
			for ch := byte('!'); len(d.Palette) <= LoginPaletteLimit; ch++ {
				if ch != '.' {
					d.Palette[string(ch)] = "#000000"
					d.Brand[0] = string(ch) + d.Brand[0][1:]
				}
			}
		}, field: "palette"},
		{name: "empty name", mutate: func(d *LoginDefinition) { d.Name = "" }, field: "name"},
		{name: "blank name", mutate: func(d *LoginDefinition) { d.Name = "   " }, field: "name"},
		{name: "invalid UTF8", mutate: func(d *LoginDefinition) { d.Name = string([]byte{0xff}) }, field: "name"},
		{name: "control character", mutate: func(d *LoginDefinition) { d.Description = "bad\x1btext" }, field: "description"},
		{name: "Unicode line separator", mutate: func(d *LoginDefinition) { d.Description = "left\u2028right" }, field: "description"},
		{name: "Unicode paragraph separator", mutate: func(d *LoginDefinition) { d.Tagline = "left\u2029right" }, field: "tagline"},
		{name: "name too wide", mutate: func(d *LoginDefinition) { d.Name = strings.Repeat("x", LoginNameWidthLimit+1) }, field: "name"},
		{name: "description too wide", mutate: func(d *LoginDefinition) { d.Description = strings.Repeat("x", LoginDescriptionWidthLimit+1) }, field: "description"},
		{name: "tagline too wide", mutate: func(d *LoginDefinition) { d.Tagline = strings.Repeat("x", LoginTaglineWidthLimit+1) }, field: "tagline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validLoginDefinition()
			tt.mutate(&input)

			_, err := ValidateLoginDefinition(input)
			if err == nil {
				t.Fatal("ValidateLoginDefinition() error = nil")
			}
			var validationErr *LoginDefinitionError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error type = %T, want *LoginDefinitionError", err)
			}
			if validationErr.Field != tt.field {
				t.Fatalf("error field = %q, want %q (error: %v)", validationErr.Field, tt.field, err)
			}
		})
	}
}

func TestValidateLoginDefinitionTextWidthBoundaries(t *testing.T) {
	input := validLoginDefinition()
	input.Name = strings.Repeat("界", LoginNameWidthLimit/2)
	input.Description = strings.Repeat("界", LoginDescriptionWidthLimit/2)
	input.Tagline = strings.Repeat("界", LoginTaglineWidthLimit/2)

	if _, err := ValidateLoginDefinition(input); err != nil {
		t.Fatalf("ValidateLoginDefinition() rejected exact visible-width limits: %v", err)
	}
}

func TestValidateLoginDefinitionReportsMissingColorsDeterministically(t *testing.T) {
	input := validLoginDefinition()
	delete(input.Palette, "L")
	delete(input.Palette, "G")

	for range 20 {
		_, err := ValidateLoginDefinition(input)
		var validationErr *LoginDefinitionError
		if !errors.As(err, &validationErr) {
			t.Fatalf("error type = %T, want *LoginDefinitionError", err)
		}
		if validationErr.Field != "brand[0][1]" {
			t.Fatalf("error field = %q, want first unresolved grid cell", validationErr.Field)
		}
	}
}

func TestDecodeLoginDefinitionJSONRejectsDuplicateAndUnknownFields(t *testing.T) {
	input := validLoginDefinition()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(encoded, []byte(`"L":"#F4E9D0"`), []byte(`"L":"#F4E9D0","L":"#000000"`), 1)
	if bytes.Equal(duplicate, encoded) {
		t.Fatal("fixture did not contain palette entry")
	}

	_, err = DecodeLoginDefinitionJSON(duplicate)
	var validationErr *LoginDefinitionError
	if !errors.As(err, &validationErr) {
		t.Fatalf("duplicate error type = %T, want *LoginDefinitionError", err)
	}
	if validationErr.Field != "palette[L]" {
		t.Fatalf("duplicate error field = %q, want palette[L]", validationErr.Field)
	}

	unknown := bytes.Replace(encoded, []byte(`"name":`), []byte(`"unknown":true,"name":`), 1)
	if _, err := DecodeLoginDefinitionJSON(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestDecodeLoginDefinitionJSONRejectsTrailingValue(t *testing.T) {
	encoded, err := json.Marshal(validLoginDefinition())
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, []byte(` {}`)...)
	if _, err := DecodeLoginDefinitionJSON(encoded); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("trailing value error = %v", err)
	}
}
