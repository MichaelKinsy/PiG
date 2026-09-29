package testfixture

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type noUIComponent struct{}

func (noUIComponent) Render(int) []string                                   { panic("headless render") }
func (noUIComponent) HandleInput(string) (sdk.RemoteComponentResult, error) { panic("headless input") }
func (noUIComponent) SetInvalidate(func())                                  { panic("headless invalidation") }
func (noUIComponent) Dispose()                                              { panic("headless disposal") }

func probeNoUI(ctx sdk.Context) error {
	input, ok, err := ctx.Input("Input", "placeholder")
	if err != nil || ok || input != "" {
		return fmt.Errorf("headless input: %q %t %w", input, ok, err)
	}
	editor, ok, err := ctx.Editor("Editor", "prefill")
	if err != nil || ok || editor != "" {
		return fmt.Errorf("headless editor: %q %t %w", editor, ok, err)
	}
	confirmed, err := ctx.Confirm("Confirm", "message")
	if err != nil || confirmed {
		return fmt.Errorf("headless confirm: %t %w", confirmed, err)
	}
	value, err := ctx.Custom(noUIComponent{}, nil)
	if err != nil || value != nil {
		return fmt.Errorf("headless custom: %v %w", value, err)
	}
	off, err := ctx.OnTerminalInput(func(string) sdk.TerminalInputResult { panic("headless terminal") })
	if err != nil {
		return err
	}
	off()
	off()
	if err := ctx.SetEditorComponent(func() { panic("headless factory") }); err != nil {
		return err
	}
	if err := ctx.AddAutocompleteProvider(nil); err != nil {
		return err
	}
	ctx.SetStatus("ignored", "ignored")
	ctx.SetEditorText("ignored")
	ctx.SetToolsExpanded(true)
	text, err := ctx.GetEditorText()
	if err != nil {
		return fmt.Errorf("headless editor text: %w", err)
	}
	if text != "" {
		return fmt.Errorf("headless editor text: %q", text)
	}
	expanded, err := ctx.GetToolsExpanded()
	if err != nil {
		return fmt.Errorf("headless tools expanded: %w", err)
	}
	if expanded {
		return fmt.Errorf("headless tools expanded")
	}
	themes, err := ctx.GetAllThemes()
	if err != nil {
		return fmt.Errorf("headless themes: %w", err)
	}
	if len(themes) != 0 {
		return fmt.Errorf("headless themes: %v", themes)
	}
	theme, err := ctx.GetTheme("dark")
	if err != nil || theme != nil {
		return fmt.Errorf("headless theme: %v %w", theme, err)
	}
	success, message := ctx.SetTheme("dark")
	if success || message != "UI not available" {
		return fmt.Errorf("headless setTheme: %t %s", success, message)
	}
	return nil
}
