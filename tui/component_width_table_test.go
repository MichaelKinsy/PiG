package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// widthTableLong is long, space-separated prose that forces word wrapping in
// every component that renders free text.
const widthTableLong = "This dialog text is intentionally long so that every narrow width must wrap or truncate it before rendering"

// widthTableComponent names one selector, dialog or list component and builds a
// fresh instance for each render width, so no cache from a previous width can
// hide an overflow.
type widthTableComponent struct {
	name  string
	build func() Component
}

func widthTableComponents() []widthTableComponent {
	return []widthTableComponent{
		{"ExtensionSelector", func() Component {
			return NewExtensionSelector("Choose how to continue with the current session", []string{"Continue with the current session", "Cancel"})
		}},
		{"ExtensionSelectorDescription", func() Component {
			c := NewExtensionSelector("Bug report", []string{widthTableLong, "Cancel"})
			c.SetDescription(widthTableLong)
			return c
		}},
		{"ExtensionInput", func() Component {
			return NewExtensionInputComponent("Enter the name of the new session branch", "placeholder text")
		}},
		{"ExtensionEditor", func() Component {
			c := NewExtensionEditorComponent("Report a bug with a long dialog title", widthTableLong)
			c.SetDescription(widthTableLong)
			return c
		}},
		{"FilterableList", func() Component {
			return NewFilterableList("Resume a previous session", []string{widthTableLong, "short"})
		}},
		{"FilterableListDescriptions", func() Component {
			f := NewFilterableList("Pick one", []string{"first-option-label", "second"})
			f.Descriptions = []string{widthTableLong, "short description"}
			return f
		}},
		{"ShowImagesSelector", func() Component { return NewShowImagesSelector(true, nil, nil) }},
		{"ThemeSelector", func() Component { return NewThemeSelector("dark", nil, nil, nil) }},
		{"SelectSubmenu", func() Component {
			return NewSelectSubmenu("Thinking level", widthTableLong, []SelectItem{
				{Value: "high", Label: "high", Description: widthTableLong},
				{Value: "off", Label: "off", Description: "No reasoning"},
			}, "high")
		}},
		{"SettingsList", func() Component {
			return NewSettingsList([]SettingItem{
				{ID: "a", Label: "Automatically compact the context", Description: widthTableLong, CurrentValue: "true", Values: []string{"true", "false"}},
				{ID: "b", Label: "Theme", Description: "Theme", CurrentValue: "dark-with-a-long-name", Values: []string{"dark-with-a-long-name"}},
			})
		}},
		{"UserMessageSelector", func() Component {
			return NewUserMessageSelector([]string{widthTableLong, "second message"})
		}},
		{"UserMessageSelectorScrolled", func() Component {
			messages := make([]string, 15)
			for i := range messages {
				messages[i] = widthTableLong
			}
			return NewUserMessageSelector(messages)
		}},
		{"UserMessageSelectorEmpty", func() Component { return NewUserMessageSelector(nil) }},
		{"ModelSelector", func() Component {
			items := []ModelSelectorItem{
				{Provider: "anthropic", ID: "claude-model-with-a-very-long-identifier", Name: "Claude model with a very long display name"},
				{Provider: "openai", ID: "gpt", Name: "GPT"},
			}
			m := NewModelSelector("Select a model for this session", items, items, "anthropic/claude-model-with-a-very-long-identifier")
			m.SetStatus(widthTableLong)
			return m
		}},
		{"ModelSelectorError", func() Component {
			items := []ModelSelectorItem{{Provider: "openai", ID: "gpt", Name: "GPT"}}
			m := NewModelSelector("Select a model", items, items, "openai/gpt")
			m.SetError(widthTableLong)
			return m
		}},
		{"ModelSelectorNoAuth", func() Component {
			return NewModelSelector("Select a model", nil, []ModelSelectorItem{{Provider: "openai", ID: "gpt"}}, "")
		}},
		{"OAuthSelectorLogin", func() Component {
			return NewOAuthSelector("login", []OAuthProvider{
				{ID: "github-copilot", Name: "GitHub Copilot with a long provider name", AuthType: "oauth", Stored: true, StoredType: "oauth"},
				{ID: "openai", Name: "OpenAI", AuthType: "api_key", AuthStatusSource: "environment", AuthStatusLabel: "OPENAI_API_KEY_WITH_LONG_NAME"},
			})
		}},
		{"OAuthSelectorLogoutEmpty", func() Component { return NewOAuthSelector("logout", nil) }},
		{"ScopedModelsList", func() Component {
			s := NewScopedModelsList(ScopedModelsConfig{
				AllModels: []ModelItem{
					{FullID: "anthropic/claude-model-with-a-very-long-identifier", Name: "Claude long", Provider: "anthropic"},
					{FullID: "openai/gpt", Name: "GPT", Provider: "openai"},
				},
				EnabledModelIDs: []string{"openai/gpt", "missing/model"},
			})
			s.SetRefreshStatus(widthTableLong, RefreshStatusWarning)
			return s
		}},
		{"ConfigSelector", func() Component {
			item := &ResourceItem{Path: "/very/long/path/to/an/extension/that/does/not/fit.ts", Enabled: true, ResourceType: "extensions", DisplayName: "extension-with-a-long-display-name", Scope: "user", Origin: "top-level", Health: "missing"}
			group := &ResourceGroup{Key: "user", Label: "User resources with a long group label", Scope: "user", Origin: "top-level", Subgroups: []*ResourceSubgroup{{Type: "extensions", Label: "Extensions with a long subgroup label", Items: []*ResourceItem{item}}}}
			return NewScopedConfigSelector([]*ResourceGroup{group}, []*ResourceGroup{group}, 30, "project", true)
		}},
		{"ConfigSelectorEmpty", func() Component { return NewConfigSelector(nil, 30) }},
		{"LoginDialogAuth", func() Component {
			d := NewLoginDialog("Provider with a long display name", nil)
			d.ShowAuth("https://example.com/oauth/authorize?client_id=abcdefghijklmnopqrstuvwxyz&redirect_uri=http://localhost", widthTableLong)
			return d
		}},
		{"LoginDialogInput", func() Component {
			d := NewLoginDialog("Provider", nil)
			d.ShowInput(widthTableLong, "placeholder")
			d.HandleInput("typed-code-value-that-is-longer-than-narrow-widths")
			return d
		}},
		{"LoginDialogProgress", func() Component {
			d := NewLoginDialog("Provider", nil)
			d.ShowWaiting(widthTableLong)
			d.ShowProgress(widthTableLong)
			return d
		}},
		{"BorderedLoader", func() Component { return NewBorderedLoader(widthTableLong, true) }},
		{"BorderedLoaderNonCancellable", func() Component { return NewBorderedLoader(widthTableLong, false) }},
		{"EditorSlashAutocomplete", func() Component {
			e := NewEditor()
			e.Focused = true
			e.SetAutocomplete(NewSlashOnlyProvider(append(sampleCommands(), SlashCommand{Name: "a-very-long-command-name-for-width-tests", Description: widthTableLong})))
			e.HandleInput("/")
			return e
		}},
		{"TextInput", func() Component {
			in := NewTextInput("Title of the text input dialog")
			in.SetText(widthTableLong)
			return in
		}},
		{"TruncatedText", func() Component { return NewPaddedTruncatedText(widthTableLong, 1, 0) }},
		{"Text", func() Component { return NewPaddedText(widthTableLong, 1, 0, nil) }},
	}
}

// TestSelectorDialogListComponentsNeverExceedRenderWidth renders every
// selector, dialog and list component at widths 1..120 and requires each line
// to fit. Upstream's TUI treats a wider line as fatal ("Rendered line N exceeds
// terminal width"), so every one of these components must stay in bounds.
func TestSelectorDialogListComponentsNeverExceedRenderWidth(t *testing.T) {
	for _, tc := range widthTableComponents() {
		t.Run(tc.name, func(t *testing.T) {
			for width := 1; width <= 120; width++ {
				c := tc.build()
				for i, line := range c.Render(width) {
					if got := widthx.VisibleWidth(line); got > width {
						t.Fatalf("width %d: line %d is %d cells wide: %q", width, i, got, stripANSI(line))
					}
				}
				if d, ok := c.(interface{ Dispose() }); ok {
					d.Dispose()
				}
			}
		})
	}
}
