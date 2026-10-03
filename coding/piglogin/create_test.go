package piglogin

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type createUI struct {
	extension.UIContext
	options  []string
	selected string
	notified []string
}

func (u *createUI) Select(_ context.Context, _ string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.options = options
	return u.selected, nil
}

func (u *createUI) Notify(message, _ string) { u.notified = append(u.notified, message) }

type providerCount int

func (p providerCount) AvailableProviderCount() int { return int(p) }

func createContext(t *testing.T, ui *createUI, model extension.Model, providers int, sent *[]any) context.Context {
	t.Helper()
	c := extension.NewContext(t.TempDir(), ui, func() error { return nil }, extension.ContextActions{
		ModelRegistry: providerCount(providers),
		GetModel:      func() extension.Model { return model },
		SendUserMessage: func(content any, _ *extension.SendUserMessageOptions) error {
			*sent = append(*sent, content)
			return nil
		},
	})
	return extension.WithContext(context.Background(), c)
}

// /sprite create with a model that has credentials sends the guided sprite turn; without one it tells the user to log in
// and run /sprite create again, and sends nothing. The picker offers only sprites.
func TestSpriteCreate(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	Refresh()
	type model struct{ id string }
	var nilModel *model
	for _, tc := range []struct {
		name      string
		model     extension.Model
		providers int
		sends     bool
	}{{"model", &model{"m"}, 1, true}, {"no model", nil, 1, false}, {"typed nil model", nilModel, 1, false}, {"model without credentials", &model{"m"}, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ui := &createUI{}
			var sent []any
			if err := selectSprite(createContext(t, ui, tc.model, tc.providers, &sent), "create"); err != nil {
				t.Fatal(err)
			}
			if tc.sends {
				if len(sent) != 1 || !strings.Contains(sent[0].(string), "ctx.ui.registerSprite(") || len(ui.notified) != 0 {
					t.Fatalf("sent %v, notified %v", sent, ui.notified)
				}
				return
			}
			if len(sent) != 0 || len(ui.notified) != 1 || ui.notified[0] != CreateNeedsModel {
				t.Fatalf("sent %v, notified %v", sent, ui.notified)
			}
		})
	}
	if Active().ID != DefaultID {
		t.Fatalf("/sprite create changed the active sprite to %s", Active().ID)
	}
}

// The prompt carries the default pig's 14 rows of 16 cells and a color for each non-transparent symbol, and the template
// it shows is a sprite registerSprite accepts: a palette entry for '.' fails with "'.' is reserved for transparency".
func TestCreatePromptCarriesTheStandardPig(t *testing.T) {
	prompt := CreatePrompt()
	for _, row := range pigMascot {
		if !strings.Contains(prompt, `"`+row+`",`) {
			t.Fatalf("prompt omits mascot row %q", row)
		}
	}
	for symbol, value := range paletteFor(Default()) {
		if symbol == '.' {
			continue
		}
		if !strings.Contains(prompt, `"`+string(symbol)+`": "`+rgbaHex(value)+`"`) {
			t.Fatalf("prompt omits palette symbol %q", symbol)
		}
	}
	_, block, found := strings.Cut(prompt, "mascot: [")
	mascotPart, palettePart, ok := strings.Cut(block, "palette: {")
	if !found || !ok {
		t.Fatalf("prompt template has no palette:\n%s", prompt)
	}
	definition := extension.SpriteDefinition{ID: "my-pig", Name: "My PiG", Tagline: "One line about my pig.", Palette: map[string]string{}}
	for _, match := range regexp.MustCompile(`"([^"]{16})",`).FindAllStringSubmatch(mascotPart, -1) {
		definition.Mascot = append(definition.Mascot, match[1])
	}
	for _, match := range regexp.MustCompile(`"(.)": "(#[0-9A-Fa-f]{6})",`).FindAllStringSubmatch(palettePart, -1) {
		definition.Palette[match[1]] = match[2]
	}
	if _, err := extension.ValidateSpriteDefinition(definition); err != nil {
		t.Fatalf("the prompt's template sprite does not register: %v", err)
	}
}

// The picker's last item, "Create your own...", shows how to create a sprite and returns: it sends no turn, even with a
// model, and changes no sprite.
func TestSpritePickerCreateYourOwnShowsTheHint(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	Refresh()
	type model struct{ id string }
	for _, providers := range []int{0, 1} {
		ui := &createUI{selected: CreateOption}
		var sent []any
		if err := selectSprite(createContext(t, ui, &model{"m"}, providers, &sent), ""); err != nil {
			t.Fatal(err)
		}
		if got := ui.options[len(ui.options)-1]; got != CreateOption {
			t.Fatalf("last picker item = %q, want %q", got, CreateOption)
		}
		if len(sent) != 0 || len(ui.notified) != 1 || ui.notified[0] != CreateHint {
			t.Fatalf("providers %d: sent %v, notified %v", providers, sent, ui.notified)
		}
	}
	if Active().ID != DefaultID {
		t.Fatalf("Create your own... changed the active sprite to %s", Active().ID)
	}
}
