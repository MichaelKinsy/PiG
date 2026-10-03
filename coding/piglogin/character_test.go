package piglogin

// The character sprite tests of the owner's earlier PiG piglogin (definition_test.go and render_test.go), with its inputs
// and expectations. Not ported: the HPE wordmark and the hpe-agentic default (PiG draws no HPE branding), the shared-hero
// assertion (the hero here is the website "PiG." wordmark, pinned by TestDefaultHeaderGoldenRenders and the per-sprite
// goldens below), and the "dark-vader" alias (a settings value of that PiG, never written to this login state). The
// sheriff keeps PiG Standard's name, "Sheriff PiG".

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestCustomVariantsUseApprovedMetadataAndColors(t *testing.T) {
	tests := []struct {
		id, name, tagline string
		wantColors        map[byte]color.RGBA
	}{
		{
			id: "pigrogu", name: "PiGrogu", tagline: "Passing tests, this is the way.",
			wantColors: map[byte]color.RGBA{'P': rgb(0x8B, 0xB5, 0x70), 'p': rgb(0xB7, 0xD4, 0x96), 's': rgb(0x72, 0x99, 0x5B), 'e': rgb(0xC7, 0x8E, 0x94), 'B': rgb(0xB7, 0x98, 0x6D), 'b': rgb(0x78, 0x5B, 0x3D)},
		},
		{
			id: "darth-vader", name: "Darth Vader", tagline: "I find your lack of tests disturbing.",
			wantColors: map[byte]color.RGBA{'P': rgb(0x0B, 0x0C, 0x14), 'p': rgb(0x2E, 0x31, 0x45), 's': rgb(0x22, 0x24, 0x32), 'b': rgb(0xD8, 0x00, 0x00), 'e': rgb(0x16, 0x18, 0x24), 'O': rgb(0x03, 0x03, 0x07), 'K': rgb(0x00, 0x00, 0x03)},
		},
		{
			id: "kratos", name: "Kratos", tagline: "War on bugs.",
			wantColors: map[byte]color.RGBA{'E': rgb(0xD1, 0xC7, 0xB5), 'e': rgb(0xAD, 0xA1, 0x91), 's': rgb(0xA6, 0x48, 0x32), 'R': rgb(0x91, 0x1F, 0x1F), 'W': rgb(0xB5, 0xBE, 0xBE), 'O': rgb(0x16, 0x12, 0x11), 'K': rgb(0x16, 0x12, 0x11)},
		},
		{
			id: "piglet", name: "Piglet", tagline: "A Very Small Animal with very large diffs.",
			wantColors: map[byte]color.RGBA{'P': rgb(0xF6, 0xA6, 0xB6), 's': rgb(0xE8, 0x6F, 0x88), 'e': rgb(0xF0, 0x6A, 0x8A), 'R': rgb(0xD9, 0x1F, 0x4E), 'r': rgb(0xA8, 0x0F, 0x38)},
		},
		{
			id: "spider-ham", name: "Spider-Ham", tagline: "Does whatever a spider can.",
			wantColors: map[byte]color.RGBA{'R': rgb(0xE6, 0x24, 0x29), 'r': rgb(0x7A, 0x11, 0x18), 's': rgb(0xA9, 0x13, 0x1B), 'W': rgb(0xFF, 0xFF, 0xFF), 'O': rgb(0x12, 0x0B, 0x0D), 'K': rgb(0x12, 0x0B, 0x0D)},
		},
		{
			id: "sheriff", name: "Sheriff PiG", tagline: "Laying down the law, one commit at a time.",
			wantColors: map[byte]color.RGBA{'P': rgb(0xF2, 0xB8, 0xA8), 'H': rgb(0x6B, 0x4C, 0x3A), 'h': rgb(0x8B, 0x6F, 0x47), 'S': rgb(0xFF, 0xD7, 0x00)},
		},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			variant := FindVariant(test.id)
			if variant.ID != test.id || variant.Name != test.name || variant.Tagline != test.tagline {
				t.Fatalf("variant = %#v", variant)
			}
			if len(variant.Sprite) != extension.LoginMascotHeight {
				t.Fatalf("sprite height = %d", len(variant.Sprite))
			}
			palette := paletteFor(variant)
			for symbol, want := range test.wantColors {
				if got := palette[symbol]; got != want {
					t.Fatalf("palette[%q] = %#v, want %#v", symbol, got, want)
				}
			}
			if _, err := extension.ValidateLoginDefinition(LoginDefinitionFor(variant)); err != nil {
				t.Fatalf("canonical definition: %v", err)
			}
		})
	}
}

func TestPiGroguVisualAnchors(t *testing.T) {
	piGrogu := FindVariant("pigrogu").Sprite
	if piGrogu[0] != "OO............OO" || piGrogu[1] != "OOO..........OOO" {
		t.Fatalf("PiGrogu ear span changed: %q / %q", piGrogu[0], piGrogu[1])
	}
	if piGrogu[6] != ".OPPKKPPPPKKPPO." || piGrogu[7] != ".OPPKWPPPPWKPPO." {
		t.Fatalf("PiGrogu eye anchors changed: %q / %q", piGrogu[6], piGrogu[7])
	}
	if piGrogu[12] != "..OBBBBBBBBBBO.." || piGrogu[13] != ".OBbbbbbbbbbbBO." {
		t.Fatalf("PiGrogu robe anchors changed: %q / %q", piGrogu[12], piGrogu[13])
	}
}

func TestPigletVisualAnchors(t *testing.T) {
	piglet := FindVariant("piglet").Sprite
	if piglet[0] != "..OOOO....OOOO.." || piglet[1] != ".OeeeO....OeeeO." {
		t.Fatalf("Piglet tall ear anchors changed: %q / %q", piglet[0], piglet[1])
	}
	if piglet[5] != ".OPPWWPPPPWWPPO." || piglet[6] != ".OPPWKPPPPKWPPO." {
		t.Fatalf("Piglet standard eye anchors changed: %q / %q", piglet[5], piglet[6])
	}
	if piglet[9] != ".OPPPPPPPPPPPPO." || piglet[10] != "..OPPPPKKPPPPO.." {
		t.Fatalf("Piglet nose gap or centered mouth changed: %q / %q", piglet[9], piglet[10])
	}
	if piglet[11] != "..ORRRRRRRRRRO.." || piglet[12] != ".OrrrrrrrrrrrrO." || piglet[13] != "ORRRRRRRRRRRRRRO" {
		t.Fatalf("Piglet horizontal shirt bands changed: %q / %q / %q", piglet[11], piglet[12], piglet[13])
	}
}

func TestSpiderHamVisualContracts(t *testing.T) {
	spiderHam := FindVariant("spider-ham").Sprite
	for y, row := range spiderHam {
		for x := range len(row) {
			if (row[x] == '.') != (pigMascot[y][x] == '.') {
				t.Fatalf("Spider-Ham silhouette differs from standard Pig at row %d column %d", y, x)
			}
		}
	}
	if strings.Count(spiderHam[1], "r") != 2 || strings.Count(spiderHam[2], "r") != 2 {
		t.Fatalf("Spider-Ham ear webs are missing: %q / %q", spiderHam[1], spiderHam[2])
	}
	for y := 3; y <= 6; y++ {
		if !strings.ContainsRune(spiderHam[y], 'r') {
			t.Fatalf("Spider-Ham upper web is missing from row %d: %q", y, spiderHam[y])
		}
	}
	for y, row := range spiderHam {
		for x := range len(row) {
			if row[x] != 'W' {
				continue
			}
			for _, adjacent := range [][2]int{{x - 1, y}, {x + 1, y}, {x, y - 1}, {x, y + 1}} {
				adjacentX, adjacentY := adjacent[0], adjacent[1]
				if adjacentX < 0 || adjacentX >= len(row) || adjacentY < 0 || adjacentY >= len(spiderHam) {
					t.Fatalf("Spider-Ham white eye reaches sprite boundary at row %d column %d", y, x)
				}
				if adjacentSymbol := spiderHam[adjacentY][adjacentX]; adjacentSymbol != 'W' && adjacentSymbol != 'K' {
					t.Fatalf("Spider-Ham eye at row %d column %d borders %q instead of black mask", y, x, adjacentSymbol)
				}
			}
		}
	}
	if strings.Count(strings.Join(spiderHam, ""), "W") != 8 {
		t.Fatalf("Spider-Ham white lens area changed")
	}
	if strings.Count(spiderHam[7], "s") != 4 || strings.Count(spiderHam[8], "s") != 4 || strings.Count(spiderHam[9], "s") != 4 {
		t.Fatalf("Spider-Ham 4/6/4 snout changed: %q / %q / %q", spiderHam[7], spiderHam[8], spiderHam[9])
	}
	if strings.Count(spiderHam[8], "K") != 2 {
		t.Fatalf("Spider-Ham nostril count changed: %q", spiderHam[8])
	}
	for y := 7; y <= 11; y++ {
		if !strings.ContainsRune(spiderHam[y], 'r') {
			t.Fatalf("Spider-Ham lower web is missing from row %d: %q", y, spiderHam[y])
		}
	}
}

func TestSheriffVisualAnchors(t *testing.T) {
	sheriff := FindVariant("sheriff").Sprite
	if got := sheriff[6]; got != pigMascot[6] {
		t.Fatalf("Sheriff gaze row = %q, want standard Pig gaze %q", got, pigMascot[6])
	}
	if sheriff[0] != ".....HHHHHH....." || sheriff[1] != "..OeHHHHHHHHeO.." {
		t.Fatalf("Sheriff crown and ear anchors changed: %q / %q", sheriff[0], sheriff[1])
	}
	if sheriff[2] != ".OeehhSSSShheeO." || sheriff[3] != "HHHHHHHHHHHHHHHH" {
		t.Fatalf("Sheriff band, badge, or brim anchors changed: %q / %q", sheriff[2], sheriff[3])
	}
}

func TestCustomVariantSpriteUsed(t *testing.T) {
	v := FindVariant("darth-vader")
	if len(v.Sprite) == 0 {
		t.Fatal("darth-vader should define a custom sprite")
	}
	if got := MascotSpriteFor(v); &got[0] != &v.Sprite[0] {
		t.Fatal("MascotSpriteFor did not return custom sprite")
	}
}

// Every sprite's full login art (the wordmark and the mascot, which first-time setup and /sprite preview show) is pinned
// by testdata/login-<id>-120.golden, rendered by the same mirror of the native login template as the default's goldens.
func TestEveryVariantLoginGolden(t *testing.T) {
	for _, variant := range Variants {
		t.Run(variant.ID, func(t *testing.T) {
			definition := LoginDefinitionFor(variant)
			view := LoginDefinitionView{
				Hero: definition.Hero, Mascot: definition.Mascot, Palette: definition.Palette,
				Name: definition.Name, Description: definition.Description, Tagline: definition.Tagline,
			}
			got := strings.Join(renderHeader(view, 120), "\n") + "\n"
			path := filepath.Join("testdata", fmt.Sprintf("login-%s-120.golden", variant.ID))
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("%s login differs from %s", variant.ID, path)
			}
		})
	}
}
