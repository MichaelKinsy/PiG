package piglogin_test

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

func blueSprite(t *testing.T, id, name string) extension.ValidatedSpriteDefinition {
	t.Helper()
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: id, Name: name, Tagline: "Registered by an extension.",
		Mascot: []string{
			"................", "...OOOO..OOOO...", "...OeeO..OeeO...", "..OeePPPPPPeeO..",
			".OPPPpPPPPpPPPO.", ".OPPWWPPPPWWPPO.", ".OPPWKPPPPKWPPO.", ".OPbbPssssPbbPO.",
			".OPPPsKssKsPPPO.", ".OPPPPssssPPPPO.", ".OPPPPPPPPPPPPO.", "..OPPPPPPPPPPO..",
			"...OOOOOOOOOO...", "................",
		},
		Palette: map[string]string{
			"O": "#18141E", "K": "#18141E", "W": "#FFFFFF", "P": "#5B8DEF",
			"p": "#8FB2F5", "s": "#3D6BC4", "b": "#F49AA6", "e": "#4A7BD8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func registerFor(t *testing.T, owner string, definition extension.ValidatedSpriteDefinition) {
	t.Helper()
	if err := piglogin.Register(owner, definition); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { piglogin.Unregister(owner) })
}

// A registered sprite follows the built-in sprites in /sprite list, the picker and the IDs, and draws its own head.
func TestRegisteredSpriteJoinsTheCatalogue(t *testing.T) {
	h := newSpriteHarness(t)
	registerFor(t, "piglet-a", blueSprite(t, "blue-pig", "Blue PiG"))
	all := piglogin.All()
	if len(all) != len(wantIDs)+1 || all[len(all)-1].ID != "blue-pig" {
		t.Fatalf("catalogue = %d sprites ending in %q", len(all), all[len(all)-1].ID)
	}
	if err := h.run("list"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(h.ui.notes[0].message, "\n")
	if got := lines[len(lines)-1]; got != "blue-pig: Blue PiG: Registered by an extension." {
		t.Fatalf("last /sprite list line = %q", got)
	}
	h.ui.answer = "Blue PiG: Registered by an extension."
	if err := h.run(""); err != nil {
		t.Fatal(err)
	}
	// The registered sprite is the last sprite, before "Create your own...".
	if got := h.ui.asked[0][len(h.ui.asked[0])-2]; got != h.ui.answer {
		t.Fatalf("picker's last sprite = %q", got)
	}
	if got := piglogin.Active(); got.ID != "blue-pig" || got.Sprite[2] != "...OeeO..OeeO..." {
		t.Fatalf("active = %q", got.ID)
	}
	if pixels := piglogin.HeadPixels(piglogin.Active()); pixels[0].Color.B != 0x1E || !slices.ContainsFunc(pixels, func(p piglogin.HeadPixel) bool { return p.Color.R == 0x5B && p.Color.B == 0xEF }) {
		t.Fatal("the registered head is not drawn in its own palette")
	}
}

func TestRegisterRejectsTakenIDsAndReplacesItsOwn(t *testing.T) {
	isolate(t)
	if err := piglogin.Register("piglet-a", blueSprite(t, "sheriff", "Fake Sheriff")); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("registering a built-in ID = %v", err)
	}
	registerFor(t, "piglet-a", blueSprite(t, "blue-pig", "Blue PiG"))
	if err := piglogin.Register("piglet-b", blueSprite(t, "blue-pig", "Other Blue")); err == nil || !strings.Contains(err.Error(), "another extension") {
		t.Fatalf("registering another extension's ID = %v", err)
	}
	registerFor(t, "piglet-a", blueSprite(t, "blue-pig", "Bluer PiG"))
	if got, _ := piglogin.ByID("blue-pig"); got.Name != "Bluer PiG" || len(piglogin.All()) != len(wantIDs)+1 {
		t.Fatalf("re-registering = %q with %d sprites", got.Name, len(piglogin.All()))
	}
	piglogin.Unregister("piglet-a")
	if _, ok := piglogin.ByID("blue-pig"); ok || len(piglogin.All()) != len(wantIDs) {
		t.Fatal("Unregister left the sprite")
	}
}

// /sprite set saves a registered sprite like a built-in one. Without its extension the header draws the default sprite
// and the saved choice stays, so it returns when the extension registers the sprite again (a restart with the extension).
func TestSavedRegisteredSpriteFallsBackAndReturnsWithItsExtension(t *testing.T) {
	h := newSpriteHarness(t)
	registerFor(t, "piglet-a", blueSprite(t, "blue-pig", "Blue PiG"))
	if err := h.run("set blue-pig"); err != nil {
		t.Fatal(err)
	}
	piglogin.Unregister("piglet-a")
	piglogin.Refresh()
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Fatalf("without its extension the header draws %q, want the default", got.ID)
	}
	data, err := os.ReadFile(piglogin.StatePath(h.root))
	if err != nil || !strings.Contains(string(data), `"blue-pig"`) {
		t.Fatalf("the saved choice was lost: %q %v", data, err)
	}
	if err := h.run("set blue-pig"); err == nil {
		t.Fatal("/sprite set accepted a sprite no extension registered")
	}
	registerFor(t, "piglet-a", blueSprite(t, "blue-pig", "Blue PiG"))
	if got := piglogin.Active(); got.ID != "blue-pig" {
		t.Fatalf("after the extension registers again the header draws %q", got.ID)
	}
}

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
}

// An extension's mascot may use the symbols the "PiG." wordmark uses (digits and punctuation). The login definition moves
// them to free symbols, so the mascot keeps its colors and the wordmark keeps the logo's.
func TestRegisteredMascotSymbolsDoNotRecolorTheWordmark(t *testing.T) {
	isolate(t)
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: "digits", Name: "Digits", Tagline: "Uses the wordmark's symbols.",
		Mascot:  append(slices.Repeat([]string{"1111111111111111"}, extension.LoginMascotHeight-1), "@@@@####........"),
		Palette: map[string]string{"1": "#5B8DEF", "@": "#FF0000", "#": "#00FF00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerFor(t, "ext", definition)
	variant, ok := piglogin.ByID("digits")
	if !ok {
		t.Fatal("the registered sprite is not in the catalogue")
	}
	login := piglogin.LoginDefinitionFor(variant)
	if _, err := extension.ValidateLoginDefinition(login); err != nil {
		t.Fatal(err)
	}
	reference := piglogin.LoginDefinitionFor(piglogin.Default())
	for _, row := range login.Hero {
		for i := range len(row) {
			if symbol := string(row[i]); symbol != "." && login.Palette[symbol] != reference.Palette[symbol] {
				t.Fatalf("wordmark symbol %q is %s, want the logo's %s", symbol, login.Palette[symbol], reference.Palette[symbol])
			}
		}
	}
	colors := map[string]int{}
	for _, row := range login.Mascot {
		for i := range len(row) {
			if symbol := string(row[i]); symbol != "." {
				colors[login.Palette[symbol]]++
			}
		}
	}
	if want := map[string]int{"#5B8DEF": 13 * 16, "#FF0000": 4, "#00FF00": 4}; !maps.Equal(colors, want) {
		t.Fatalf("mascot colors = %v, want %v", colors, want)
	}
}
