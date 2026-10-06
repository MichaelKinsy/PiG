package piglogin

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pickerPreviewRightCells mirrors the margin tui keeps between the preview column and the dialog's right border: the
// pig ends that many cells short of it instead of touching the border.
const pickerPreviewRightCells = 2

// spriteTestVariants is the built-in catalogue a fresh test process offers.
func spriteTestVariants(t *testing.T) []Variant {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	Refresh()
	variants := All()
	if len(variants) < 12 {
		t.Fatalf("%d sprites, want the built-in catalogue", len(variants))
	}
	return variants
}

// pickerComponent builds the picker exactly as chooseSprite hands it to the interactive host.
func pickerComponent(t *testing.T, variants []Variant, done func(any)) *spritePicker {
	t.Helper()
	component, err := spritePickerFactory(variants, spriteOptions(variants))(nil, tui.ActiveTheme(), nil, done)
	if err != nil {
		t.Fatal(err)
	}
	picker, ok := component.(*spritePicker)
	if !ok {
		t.Fatalf("factory returned %T, want the picker", component)
	}
	return picker
}

// lineCarrying returns the index of the rendered line carrying text.
func lineCarrying(t *testing.T, lines []string, text string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, text) {
			return i
		}
	}
	t.Fatalf("no line carries %q in %q", text, lines)
	return -1
}

// The picker draws the pig of the highlighted sprite on the right of the option rows: the row keeps its label on the
// left and the head's cells start where the row ends, at the render width. The title carries no pig.
func TestSpritePickerDrawsThePigOnTheRightOfTheRows(t *testing.T) {
	variants := spriteTestVariants(t)
	options := spriteOptions(variants)
	picker := pickerComponent(t, variants, func(any) {})
	const width = 100
	mode := tui.ActiveTheme().ColorMode()
	head := HeadLines(variants[0], mode)

	lines := picker.Render(width)
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got > width {
			t.Fatalf("line %d is %d cells wide: %q", i, got, widthx.StripAnsi(line))
		}
	}
	row := lineCarrying(t, lines, head[0])
	plain := widthx.StripAnsi(lines[row])
	if !slices.ContainsFunc(options, func(option string) bool { return strings.Contains(plain, option) }) {
		t.Fatalf("the head is not beside an option row: line %d = %q", row, plain)
	}
	before, _, ok := strings.Cut(lines[row], head[0])
	if !ok {
		t.Fatalf("row line = %q", lines[row])
	}
	if got := widthx.VisibleWidth(before); got != width-HeadCells-pickerPreviewRightCells {
		t.Fatalf("the head starts at column %d, want %d (the rows end there and the margin keeps it off the border)", got, width-HeadCells-pickerPreviewRightCells)
	}
	title := lineCarrying(t, lines, spritePickerTitle)
	if strings.Contains(lines[title], head[0]) {
		t.Fatalf("line %d draws the pig beside the title", title)
	}
}

// Moving down redraws the pig with the sprite under the cursor, Enter returns that sprite's row, and Escape returns no
// choice at all.
func TestSpritePickerFollowsTheCursorAndReportsTheChoice(t *testing.T) {
	variants := spriteTestVariants(t)
	options := spriteOptions(variants)
	var got any
	picker := pickerComponent(t, variants, func(value any) { got = value })
	const width = 100
	mode := tui.ActiveTheme().ColorMode()

	picker.HandleInput("j")
	if !strings.Contains(strings.Join(picker.Render(width), "\n"), HeadLines(variants[1], mode)[0]) {
		t.Fatal("the pig did not follow the cursor down to the next sprite")
	}
	picker.HandleInput("\n")
	if got != options[1] {
		t.Fatalf("choice = %q, want the highlighted row %q", got, options[1])
	}

	picker = pickerComponent(t, variants, func(value any) { got = value })
	picker.HandleInput("\x1b")
	if got != "" {
		t.Fatalf("escape returned %q, want no choice", got)
	}
}

// The create row is not a sprite: it draws no pig, and its list is byte-identical to the plain extension selector, so
// the picker keeps upstream's full-width rows whenever there is no pig to draw. Moving back to a sprite brings the pig
// back.
func TestSpritePickerCreateRowDrawsNoPreview(t *testing.T) {
	variants := spriteTestVariants(t)
	options := spriteOptions(variants)
	picker := pickerComponent(t, variants, func(any) {})
	const width = 100
	mode := tui.ActiveTheme().ColorMode()
	for range len(variants) {
		picker.HandleInput("j")
	}
	lines := picker.Render(width)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, CreateOption) {
		t.Fatal("the picker does not offer the create row")
	}
	plain := tui.NewExtensionSelector(spritePickerTitle, options)
	for range len(options) - 1 {
		plain.HandleInput("j")
	}
	if !slices.Equal(plain.Render(width), lines) {
		t.Fatalf("the create row's list differs from the plain selector's:\n%q\nwant\n%q", lines, plain.Render(width))
	}

	picker.HandleInput("k")
	if got := strings.Join(picker.Render(width), "\n"); !strings.Contains(got, HeadLines(variants[len(variants)-1], mode)[0]) {
		t.Fatal("the sprite above the create row does not draw its pig again")
	}
}

// pickerUI answers the picker's two dialog calls the way two UI contexts do: it mounts the custom picker as the
// interactive host does (build the factory's component, render it, drive it with keys), or, with no keys, answers the
// custom call with no result like the RPC and no-op contexts, which sends chooseSprite to the plain selector. Any
// other UIContext method is never called on this path, so the embedded interface stays nil.
type pickerUI struct {
	extension.UIContext
	keys    []string
	dismiss bool
	customs int
	selects int
	headers []any
	result  any
}

func (u *pickerUI) Custom(_ context.Context, factory any, _ any) (any, error) {
	u.customs++
	build, ok := factory.(extension.CustomFactory)
	if !ok {
		return nil, nil
	}
	component, err := build(nil, tui.ActiveTheme(), nil, func(value any) { u.result = value })
	if err != nil {
		return nil, err
	}
	picker, ok := component.(*spritePicker)
	if !ok {
		return nil, nil
	}
	picker.Render(100)
	for _, key := range u.keys {
		picker.HandleInput(key)
	}
	return u.result, nil
}

func (u *pickerUI) Select(_ context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.selects++
	if u.dismiss {
		return "", context.Canceled
	}
	return "", nil
}

func (u *pickerUI) SetHeader(factory any) { u.headers = append(u.headers, factory) }

// /sprite with no arguments picks through the preview picker: the chosen row activates that sprite, saves it and
// restores the header so it draws at once. The plain selector, which has no preview, is never asked.
func TestSpriteWithoutArgumentsPicksThroughThePreviewPicker(t *testing.T) {
	variants := spriteTestVariants(t)
	ui := &pickerUI{keys: []string{"j", "\n"}}
	ctx := extension.WithContext(context.Background(), extension.NewContext(t.TempDir(), ui, func() error { return nil }, extension.ContextActions{}))

	if err := selectSprite(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if ui.customs != 1 || ui.selects != 0 {
		t.Fatalf("custom calls = %d, plain selects = %d: the picker must come from the custom call", ui.customs, ui.selects)
	}
	if got := Active().ID; got != variants[1].ID {
		t.Fatalf("active sprite = %q, want %q", got, variants[1].ID)
	}
	if got := LoadVariant(ConfigHome()).ID; got != variants[1].ID {
		t.Fatalf("saved sprite = %q, want %q", got, variants[1].ID)
	}
	if len(ui.headers) != 1 || ui.headers[0] != nil {
		t.Fatalf("setHeader calls = %v, want one setHeader(undefined) so the header draws the new sprite", ui.headers)
	}
}

// A UI context that cannot mount a custom component (the RPC and no-op contexts answer a custom call with no result)
// falls back to the plain extension selector, with the same rows and title.
func TestChooseSpriteFallsBackToThePlainSelector(t *testing.T) {
	variants := spriteTestVariants(t)
	options := spriteOptions(variants)
	ui := &pickerUI{}
	selected, err := chooseSprite(context.Background(), ui, variants, options)
	if err != nil || selected != "" {
		t.Fatalf("chooseSprite = %q, %v; want an empty choice and no error", selected, err)
	}
	if ui.customs != 1 || ui.selects != 1 {
		t.Fatalf("custom calls = %d, plain selects = %d, want one of each", ui.customs, ui.selects)
	}
}

// A plain selector the user dismisses reports nothing, exactly as it did before the picker drew a pig.
func TestChooseSpriteReportsADismissedPlainSelector(t *testing.T) {
	variants := spriteTestVariants(t)
	ui := &pickerUI{dismiss: true}
	selected, err := chooseSprite(context.Background(), ui, variants, spriteOptions(variants))
	if err != nil || selected != "" {
		t.Fatalf("chooseSprite = %q, %v; a dismissed picker changes nothing and reports nothing", selected, err)
	}
	if ui.selects != 1 {
		t.Fatalf("plain selects = %d, want the fallback the dismissed picker takes", ui.selects)
	}
}
