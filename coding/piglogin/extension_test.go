package piglogin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The `/sprite` command is PiG Standard's piglogin command (extension.go at MichaelKinsy/PiG d86eb93): its subcommands,
// messages and usage text carry over; only the dialog title no longer says "Standard".

type note struct{ message, kind string }

type fakeUI struct {
	extension.UIContext
	mu      sync.Mutex
	notes   []note
	asked   [][]string
	titles  []string
	answer  string
	failure error
	headers []any
	logins  []extension.LoginDefinition
	customs []customCall
}

// customCall is one ui.Custom: whether it was an overlay, the lines its component drew at 100 columns, and whether a key
// press closed it.
type customCall struct {
	overlay bool
	lines   []string
	closed  bool
}

type previewRenderer interface {
	Render(width int) []string
	HandleInput(data string)
}

func (u *fakeUI) Custom(_ context.Context, factory any, opts any) (any, error) {
	call := customCall{}
	if options, ok := opts.(extension.CustomOptions); ok {
		call.overlay = options.Overlay
	}
	build, _ := factory.(extension.CustomFactory)
	component, err := build(nil, tui.ActiveTheme(), nil, func(any) { call.closed = true })
	if err != nil {
		return nil, err
	}
	if renderer, ok := component.(previewRenderer); ok {
		call.lines = renderer.Render(100)
		renderer.HandleInput("q")
	}
	u.mu.Lock()
	u.customs = append(u.customs, call)
	u.mu.Unlock()
	return nil, nil
}

func (u *fakeUI) SetLogin(definition extension.LoginDefinition) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.logins = append(u.logins, definition)
	return nil
}

func (u *fakeUI) SetHeader(factory any) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.headers = append(u.headers, factory)
}

// restoredHeader reports whether the command restored the built-in header exactly once, which repaints it with the sprite.
func (u *fakeUI) restoredHeader() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.headers) == 1 && u.headers[0] == nil
}

func (u *fakeUI) Notify(message, kind string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.notes = append(u.notes, note{message, kind})
}

func (u *fakeUI) Select(_ context.Context, title string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.titles = append(u.titles, title)
	u.asked = append(u.asked, options)
	return u.answer, u.failure
}

type spriteHarness struct {
	t    *testing.T
	ext  extension.Extension
	ui   *fakeUI
	root string
}

func newSpriteHarness(t *testing.T) *spriteHarness {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	piglogin.Refresh()
	ext, err := piglogin.Extension()
	if err != nil {
		t.Fatal(err)
	}
	return &spriteHarness{t: t, ext: ext, ui: &fakeUI{UIContext: extension.NoopUIContext}, root: root}
}

func (h *spriteHarness) run(args string) error {
	h.t.Helper()
	command, ok := h.ext.Commands["sprite"]
	if !ok || command.Handler == nil {
		h.t.Fatal("the extension registers no /sprite command")
	}
	c := extension.NewContext(h.root, h.ui, func() error { return nil }, extension.ContextActions{})
	return command.Handler(extension.WithContext(context.Background(), c), args)
}

func TestExtensionRegistersOnlyTheSpriteCommand(t *testing.T) {
	h := newSpriteHarness(t)
	if len(h.ext.Commands) != 1 || len(h.ext.CommandOrder) != 1 || h.ext.CommandOrder[0] != "sprite" {
		t.Fatalf("commands = %v, order %v", h.ext.Commands, h.ext.CommandOrder)
	}
	if got, want := h.ext.Commands["sprite"].Description, "Select the PiG login sprite."; got != want {
		t.Errorf("description = %q, want %q", got, want)
	}
	if len(h.ext.Tools) != 0 {
		t.Errorf("the login registers tools: %v", h.ext.Tools)
	}
}

func TestSpriteListNotifiesEverySprite(t *testing.T) {
	h := newSpriteHarness(t)
	if err := h.run("list"); err != nil {
		t.Fatal(err)
	}
	if len(h.ui.notes) != 1 || h.ui.notes[0].kind != "info" {
		t.Fatalf("notes = %v", h.ui.notes)
	}
	lines := strings.Split(h.ui.notes[0].message, "\n")
	if len(lines) != len(wantIDs) {
		t.Fatalf("list has %d lines, want %d", len(lines), len(wantIDs))
	}
	for i, variant := range piglogin.Variants {
		if want := variant.ID + ": " + variant.Name + ": " + variant.Tagline; lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
	if !strings.HasPrefix(lines[0], "pig-default: PiG: ") {
		t.Errorf("first line = %q", lines[0])
	}
}

func TestSpriteSetPersistsAndSelects(t *testing.T) {
	h := newSpriteHarness(t)
	if err := h.run("set green"); err != nil {
		t.Fatal(err)
	}
	if got := piglogin.Active(); got.ID != "green" {
		t.Fatalf("active = %q, want green", got.ID)
	}
	if got := piglogin.LoadVariant(h.root); got.ID != "green" {
		t.Fatalf("saved = %q, want green (the games read $PIG_HOME/state/pig-standard/login.json)", got.ID)
	}
	if !h.ui.restoredHeader() {
		t.Fatalf("setHeader calls = %v, want one setHeader(undefined) so the header shows the new sprite now", h.ui.headers)
	}
}

func TestSpriteSetUnknownAndUsageErrors(t *testing.T) {
	h := newSpriteHarness(t)
	err := h.run("set nope")
	want := `unknown sprite "nope"; available: ` + strings.Join(wantIDs, ", ")
	if err == nil || err.Error() != want {
		t.Fatalf("set nope: %v, want %q", err, want)
	}
	for _, args := range []string{"set", "list extra", "set green extra", "preview green extra", "bogus", "bogus arg"} {
		err := h.run(args)
		if err == nil || err.Error() != "usage: /sprite [list|set <id>|preview [id]|create]" {
			t.Errorf("%q: %v, want the usage error", args, err)
		}
	}
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Errorf("a failed command changed the sprite to %q", got.ID)
	}
	if len(h.ui.headers) != 0 {
		t.Errorf("a failed command touched the header: %v", h.ui.headers)
	}
}

func TestSpriteWithoutArgumentsPicksFromTheList(t *testing.T) {
	h := newSpriteHarness(t)
	if len(piglogin.Variants) != len(wantIDs) {
		t.Fatalf("%d sprites, want %d", len(piglogin.Variants), len(wantIDs))
	}
	h.ui.answer = piglogin.Variants[3].Name + ": " + piglogin.Variants[3].Tagline
	if err := h.run(""); err != nil {
		t.Fatal(err)
	}
	if len(h.ui.asked) != 1 || len(h.ui.asked[0]) != len(wantIDs)+1 || h.ui.asked[0][len(wantIDs)] != piglogin.CreateOption || h.ui.titles[0] != "Choose a PiG sprite" {
		t.Fatalf("asked %v with %v", h.ui.titles, h.ui.asked)
	}
	if got := piglogin.Active(); got.ID != "mint" {
		t.Fatalf("active = %q, want mint", got.ID)
	}
	if !h.ui.restoredHeader() {
		t.Fatalf("setHeader calls = %v, want one setHeader(undefined)", h.ui.headers)
	}
}

func TestSpritePickerDismissedAndBroken(t *testing.T) {
	h := newSpriteHarness(t)
	// The interactive host's runDialog returns context.Canceled when the picker is dismissed (escape or ctrl+c); a command
	// error there would be reported as an extension error.
	h.ui.failure = context.Canceled
	if err := h.run(""); err != nil {
		t.Fatalf("dismissed picker: %v", err)
	}
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Errorf("a dismissed picker changed the sprite to %q", got.ID)
	}
	if len(h.ui.headers) != 0 {
		t.Errorf("a dismissed picker touched the header: %v", h.ui.headers)
	}
	h.ui.failure = nil
	if err := h.run(""); err != nil {
		t.Fatalf("empty selection: %v", err)
	}
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Errorf("a dismissed picker changed the sprite to %q", got.ID)
	}
	h.ui.answer = "not an option"
	if err := h.run(""); err == nil || err.Error() != `unknown sprite selection "not an option"` {
		t.Errorf("unknown selection: %v", err)
	}
	h.ui.failure = errors.New("dialog failed")
	if err := h.run(""); err == nil || err.Error() != "dialog failed" {
		t.Errorf("dialog error: %v", err)
	}
}

func TestSpriteSetReportsAFailureToSave(t *testing.T) {
	h := newSpriteHarness(t)
	// A file where the state directory belongs.
	t.Setenv("PIG_HOME", h.root+"/file")
	if err := writeFile(h.root + "/file"); err != nil {
		t.Fatal(err)
	}
	piglogin.Refresh()
	err := h.run("set green")
	if err == nil {
		t.Fatal("a failed save returned no error")
	}
	if got := piglogin.Active(); got.ID == "green" {
		t.Error("the sprite was selected although it could not be saved")
	}
}

// `/sprite preview` shows a sprite's full art (wordmark and pig), name and tagline in an overlay, the active sprite without
// an argument; any key closes it, and it does not change the saved sprite.
func TestSpritePreviewShowsTheFullArt(t *testing.T) {
	h := newSpriteHarness(t)
	if err := h.run("set mint"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ args, want string }{{"preview", "mint"}, {"preview kratos", "kratos"}} {
		h.ui.customs = nil
		if err := h.run(tc.args); err != nil {
			t.Fatalf("%s: %v", tc.args, err)
		}
		variant := piglogin.FindVariant(tc.want)
		if len(h.ui.customs) != 1 || !h.ui.customs[0].overlay {
			t.Fatalf("%s: custom calls = %d, want one overlay", tc.args, len(h.ui.customs))
		}
		got := h.ui.customs[0].lines
		if want := piglogin.PreviewLines(variant, tui.TerminalColorModeTrueColor); !slices.Equal(got, want) {
			t.Fatalf("%s: preview = %q, want %q", tc.args, got, want)
		}
		art := piglogin.ArtLines(variant, tui.TerminalColorModeTrueColor)
		if len(art) != piglogin.ArtRows || widthx.VisibleWidth(art[0]) != piglogin.ArtWidth || !strings.Contains(strings.Join(got, "\n"), art[3]) {
			t.Fatalf("%s: the preview does not draw the %dx%d art", tc.args, piglogin.ArtWidth, piglogin.ArtRows)
		}
		if !strings.Contains(strings.Join(got, "\n"), variant.Name+"\x1b[0m  "+variant.Tagline) || !h.ui.customs[0].closed {
			t.Fatalf("%s: the preview lacks the name and tagline or a key did not close it", tc.args)
		}
	}
	if got := piglogin.LoadVariant(h.root); got.ID != "mint" {
		t.Errorf("preview changed the saved sprite to %q", got.ID)
	}
	h.ui.customs = nil
	err := h.run("preview nope")
	if want := `unknown sprite "nope"; available: ` + strings.Join(wantIDs, ", "); err == nil || err.Error() != want {
		t.Fatalf("preview nope: %v, want %q", err, want)
	}
	if len(h.ui.customs) != 0 {
		t.Errorf("an unknown sprite opened the preview")
	}
}
