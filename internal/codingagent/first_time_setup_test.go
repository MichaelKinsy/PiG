package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/first-time-setup.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func firstTimeSetupText(c *FirstTimeSetupComponent) string {
	return string(tools.StripANSI([]byte(strings.Join(c.Render(100), "\n"))))
}

func firstTimeSetupExtensionSprite(t *testing.T) {
	t.Helper()
	definition, err := extension.ValidateSpriteDefinition(extension.SpriteDefinition{
		ID: "blue-pig", Name: "Blue PiG", Tagline: "Registered by an extension.",
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
	if err := piglogin.Register("first-time-setup-test", definition); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { piglogin.Unregister("first-time-setup-test") })
}

func containsHead(rendered string, head []string) bool {
	for _, line := range head {
		if !strings.Contains(rendered, line) {
			return false
		}
	}
	return true
}

const (
	setupDown  = "\x1b[B"
	setupEnter = "\r"
	setupEsc   = "\x1b"
)

// Port of Pi main f8149d1abde50b31c8cb5aed09e89319b927f247 (packages/coding-agent/test/first-time-setup.test.ts):
// first-time setup has no theme picker and cancels on Escape.
func TestFirstTimeSetupOmitsThemePickerAndCancelsOnEsc(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	piglogin.Refresh()
	cancelled, submitted := false, false
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
		OnSubmit: func(FirstTimeSetupResult) { submitted = true },
		OnCancel: func() { cancelled = true },
	})
	text := firstTimeSetupText(c)
	if strings.Contains(text, "Pick a theme.") {
		t.Fatalf("first-time setup should not show a theme picker:\n%s", text)
	}
	c.HandleInput(setupEsc)
	if !cancelled || submitted {
		t.Fatalf("Esc: cancelled=%v submitted=%v", cancelled, submitted)
	}
}

type keysStartupTerminal struct{ keys []string }

func (k keysStartupTerminal) StartWithReadError(input func([]byte), _ func(), _ func(error)) error {
	for _, key := range k.keys {
		input([]byte(key))
	}
	return nil
}
func (keysStartupTerminal) Stop()        {}
func (keysStartupTerminal) Write(string) {}

// The hint line uses Pi's keyHint, whose keys are lowercase keyText (keybinding-hints.ts keyHint), not keyDisplayText.
func TestFirstTimeSetupHintUsesLowercaseKeyText(t *testing.T) {
	c := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	if text := firstTimeSetupText(c); !strings.Contains(text, " ↑↓ navigate  enter continue  escape/ctrl+c skip setup") {
		t.Fatalf("hint line differs from Pi's:\n%s", text)
	}
}

// firstTimeSetupBody is the dialog from its welcome line down, right-trimmed: everything Pi's first-time-setup.ts draws below SETUP_LOGO_LINES, which D88 replaces with the pig head.
func firstTimeSetupBody(t *testing.T, c *FirstTimeSetupComponent) []string {
	t.Helper()
	lines := strings.Split(firstTimeSetupText(c), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, " Welcome to PiG") {
			body := lines[i:]
			for j := range body {
				body[j] = strings.TrimRight(body[j], " ")
			}
			return body
		}
	}
	t.Fatalf("no welcome line in\n%s", strings.Join(lines, "\n"))
	return nil
}
