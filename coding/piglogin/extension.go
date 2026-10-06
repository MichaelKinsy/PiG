package piglogin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Name is the name of the built-in extension: its path is `builtin:pig-login`.
const Name = "pig-login"

// Extension is the built-in `pig-login` extension. It registers `/sprite`, which chooses the sprite the startup header
// shows. The header itself is the host's built-in header (the one Pi's setHeader(undefined) restores), which draws the
// active sprite's head, so an extension replaces it with setHeader exactly as in Pi. `/sprite preview` shows a sprite's
// full art, the wordmark and the pig, in an overlay until a key is pressed.
func Extension() (extension.Extension, error) {
	ext := extension.Extension{Commands: map[string]extension.RegisteredCommand{}}
	ext.Commands["sprite"] = extension.RegisteredCommand{
		Name:        "sprite",
		Description: "Select the PiG login sprite.",
		Handler:     selectSprite,
	}
	ext.CommandOrder = []string{"sprite"}
	return ext, nil
}

func selectSprite(ctx context.Context, args string) error {
	fields := strings.Fields(args)
	switch {
	case len(fields) == 0:
		return selectSpriteInteractively(ctx)
	case len(fields) == 1 && fields[0] == "create":
		return createSprite(ctx)
	case len(fields) == 1 && fields[0] == "list":
		if ui := commandUI(ctx); ui != nil {
			ui.Notify(spriteList(), "info")
		}
		return nil
	case len(fields) == 2 && fields[0] == "set":
		return activate(ctx, fields[1])
	case len(fields) <= 2 && fields[0] == "preview":
		variant := Active()
		if len(fields) == 2 {
			var ok bool
			if variant, ok = ByID(fields[1]); !ok {
				return fmt.Errorf("unknown sprite %q; available: %s", fields[1], IDs())
			}
		}
		return preview(ctx, variant)
	default:
		return fmt.Errorf("usage: /sprite [list|set <id>|preview [id]|create]")
	}
}

func commandUI(ctx context.Context) extension.UIContext {
	c := extension.FromContext(ctx)
	if c == nil {
		return nil
	}
	ui, err := c.UI()
	if err != nil {
		return nil
	}
	return ui
}

func selectSpriteInteractively(ctx context.Context) error {
	ui := commandUI(ctx)
	if ui == nil {
		return nil
	}
	variants := All()
	options := spriteOptions(variants)
	selected, err := chooseSprite(ctx, ui, variants, options)
	// A dismissed picker reports nothing and changes nothing: the custom call answers "" and the plain selector
	// answers context.Canceled, the UIContext form of Pi's undefined select result.
	if err != nil || selected == "" {
		return err
	}
	if selected == CreateOption {
		if ui := commandUI(ctx); ui != nil {
			ui.Notify(CreateHint, "info")
		}
		return nil
	}
	for i, option := range options[:len(variants)] {
		if selected == option {
			return activate(ctx, variants[i].ID)
		}
	}
	return fmt.Errorf("unknown sprite selection %q", selected)
}

// CreateOption is the last item of the sprite picker and of first-time setup's sprite step. Choosing it shows CreateHint
// and changes nothing.
const CreateOption = "Create your own..."

// CreateHint explains how to create a sprite.
const CreateHint = "To create your own sprite: log in with /login and pick a model with /model, then run /sprite create and describe your pig. PiG writes a sprite extension and loads it with /reload."

// CreateNeedsModel is what `/sprite create` shows when no model is selected.
const CreateNeedsModel = "Designing a sprite needs a working model. Run /login to log into a provider, select a model with /model, then run /sprite create."

// createSprite starts a guided turn in which the model designs a sprite with the user and writes an extension that
// registers it with registerSprite. Without a model it explains how to get one.
func createSprite(ctx context.Context) error {
	c := extension.FromContext(ctx)
	if c == nil {
		return errors.New("/sprite create: extension context unavailable")
	}
	model, err := c.Model()
	if err != nil {
		return err
	}
	if isNil(model) || !hasConfiguredProvider(c) {
		if ui := commandUI(ctx); ui != nil {
			ui.Notify(CreateNeedsModel, "info")
		}
		return nil
	}
	return c.SendUserMessage(CreatePrompt(), nil)
}

// runAgentDir is the run's agent directory, as the PiG CLI resolves it (codingagent.AgentDir): Pi's directory in shared mode
// (PIG_USE_PI_DIRS=1), else PIG_CODING_AGENT_DIR, else the agent directory under PIG_HOME, XDG_CONFIG_HOME/pig or ~/.pig.
// The global extensions directory under it is loaded on /reload.
func runAgentDir() string {
	home, _ := os.UserHomeDir()
	expand := func(path string) string {
		if path == "~" {
			return home
		}
		if rest, ok := strings.CutPrefix(path, "~/"); ok {
			return filepath.Join(home, rest)
		}
		return path
	}
	if os.Getenv("PIG_USE_PI_DIRS") == "1" {
		if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
			return expand(dir)
		}
		return filepath.Join(home, ".pi", "agent")
	}
	if dir := os.Getenv("PIG_CODING_AGENT_DIR"); dir != "" {
		return expand(dir)
	}
	if root := os.Getenv("PIG_HOME"); root != "" {
		return filepath.Join(expand(root), "agent")
	}
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(expand(root), "pig", "agent")
	}
	return filepath.Join(home, ".pig", "agent")
}

// hasConfiguredProvider reports whether any provider has auth: a selected model without credentials cannot run the turn.
// A registry that cannot report it counts as configured.
func hasConfiguredProvider(c *extension.Context) bool {
	registry, err := c.ModelRegistry()
	if err != nil {
		return false
	}
	counter, ok := registry.(interface{ AvailableProviderCount() int })
	return !ok || counter.AvailableProviderCount() > 0
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// CreatePrompt is the user turn `/sprite create` sends: it asks the model to design a sprite with the user and write a
// TypeScript extension that registers it in the run's global extensions directory, starting from the default pig's mascot
// and palette.
func CreatePrompt() string {
	palette := paletteFor(Default())
	symbols := make([]string, 0, len(palette))
	for symbol := range palette {
		// '.' is the transparent cell; registerSprite rejects a palette entry for it.
		if symbol == '.' {
			continue
		}
		symbols = append(symbols, string(symbol))
	}
	slices.Sort(symbols)
	paletteLines := make([]string, len(symbols))
	for i, symbol := range symbols {
		paletteLines[i] = fmt.Sprintf("        %q: %q,", symbol, rgbaHex(palette[symbol[0]]))
	}
	mascotLines := make([]string, len(pigMascot))
	for i, row := range pigMascot {
		mascotLines[i] = fmt.Sprintf("        %q,", row)
	}
	return strings.Join([]string{
		"Help me create my own PiG sprite: the pig my startup header shows.",
		"Start by asking me one plain-language question: what character, colors or style should my pig have? Handle every technical detail yourself and ask me only for decisions that need my preference. After I answer, describe the design in plain language and ask for my approval before writing any file.",
		"After I approve, write one TypeScript extension file in the global extensions directory: `" + filepath.Join(runAgentDir(), "extensions", "<id>.ts") + "`. Do not modify PiG itself or its built-in sprites.",
		"The extension registers the sprite in `session_start` with `await ctx.ui.registerSprite({ id, name, tagline, mascot, palette })`. `id` is a lowercase slug of at most 32 characters (letters, digits and single hyphens) that is not a built-in sprite's id (" + strings.Join(builtInIDs(), ", ") + "). `name` and `tagline` are one line each. `mascot` is exactly 14 strings of exactly 16 characters; each character is a palette symbol, and `.` is transparent. `palette` maps each symbol to a `#RRGGBB` color. Keep the pig's silhouette and change its colors and details.",
		"Start from PiG's standard pig:\n\n```ts\nexport default function (pi) {\n  pi.on(\"session_start\", async (_event, ctx) => {\n    await ctx.ui.registerSprite({\n      id: \"my-pig\",\n      name: \"My PiG\",\n      tagline: \"One line about my pig.\",\n      mascot: [\n" + strings.Join(mascotLines, "\n") + "\n      ],\n      palette: {\n" + strings.Join(paletteLines, "\n") + "\n      },\n    });\n  });\n}\n```",
		"After writing the file, tell me to run /reload and then /sprite set <id> to use it, and /sprite preview <id> to see its full art.",
	}, "\n\n")
}

func builtInIDs() []string {
	ids := make([]string, len(Variants))
	for i, variant := range Variants {
		ids[i] = variant.ID
	}
	return ids
}

// activate selects the sprite and shows it at once. Nothing else repaints the header after a command, so it restores the
// built-in header (setHeader(undefined)), which draws the active sprite; PiG Standard's login showed a chosen sprite the same
// way, by setting its login over whatever header was in place.
func activate(ctx context.Context, id string) error {
	if err := Activate(id); err != nil {
		return err
	}
	if ui := commandUI(ctx); ui != nil {
		ui.SetHeader(nil)
	}
	return nil
}

// preview shows the sprite's full art, its name and tagline in an overlay; any key closes it.
func preview(ctx context.Context, variant Variant) error {
	ui := commandUI(ctx)
	if ui == nil {
		return nil
	}
	_, err := ui.Custom(ctx, extension.CustomFactory(func(_ extension.CustomHost, theme extension.Theme, _ extension.KeybindingsManager, done func(any)) (extension.Component, error) {
		mode := tui.TerminalColorModeTrueColor
		if t, ok := theme.(*tui.Theme); ok && t != nil {
			mode = t.ColorMode()
		}
		return &previewComponent{lines: PreviewLines(variant, mode), done: done}, nil
	}), extension.CustomOptions{Overlay: true})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// PreviewLines are the lines of /sprite preview: the sprite's full art, then its name and tagline and how to close it.
func PreviewLines(variant Variant, mode tui.TerminalColorMode) []string {
	lines := []string{""}
	for _, line := range ArtLines(variant, mode) {
		lines = append(lines, " "+line+" ")
	}
	return append(lines, "", " \x1b[1m"+variant.Name+"\x1b[0m  "+variant.Tagline, " \x1b[2mPress any key to close.\x1b[0m", "")
}

// previewComponent is the /sprite preview overlay.
type previewComponent struct {
	lines []string
	done  func(any)
}

func (p *previewComponent) Render(width int) []string {
	lines := make([]string, len(p.lines))
	for i, line := range p.lines {
		lines[i] = widthx.TruncateToWidth(line, width, "", false)
	}
	return lines
}

func (p *previewComponent) HandleInput(string) { p.done(nil) }

func (p *previewComponent) Invalidate() {}

// IDs lists the sprite IDs for a message.
func IDs() string {
	variants := All()
	ids := make([]string, len(variants))
	for i, variant := range variants {
		ids[i] = variant.ID
	}
	return strings.Join(ids, ", ")
}

func spriteList() string {
	variants := All()
	lines := make([]string, len(variants))
	for i, variant := range variants {
		lines[i] = variant.ID + ": " + variant.Name + ": " + variant.Tagline
	}
	return strings.Join(lines, "\n")
}
