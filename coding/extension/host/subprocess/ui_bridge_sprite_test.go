package subprocess

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// spriteUI is a UI that keeps sprites: it records each registration and removal in order.
type spriteUI struct {
	*mockUIContext
	events []string
	reject error
}

func (u *spriteUI) RegisterSprite(owner string, definition extension.ValidatedSpriteDefinition) error {
	if u.reject != nil {
		return u.reject
	}
	u.events = append(u.events, "register "+owner+"/"+definition.ID())
	return nil
}

func (u *spriteUI) UnregisterSprites(owner string) {
	u.events = append(u.events, "unregister "+owner)
}

func spriteJSON(t *testing.T, id string, mascotWidth int) []byte {
	t.Helper()
	encoded, err := json.Marshal(extension.SpriteDefinition{
		ID:      id,
		Name:    "Sprite " + id,
		Tagline: "tagline",
		Mascot:  slices.Repeat([]string{strings.Repeat("A", mascotWidth)}, extension.LoginMascotHeight),
		Palette: map[string]string{"A": "#112233"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func registerSprite(t *testing.T, bridge *UIBridge, ext string, args []byte) *CallResultPayload {
	t.Helper()
	result, err := bridge.HandleCall(ext, &CallPayload{Method: CallUIRegisterSprite, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUIBridgeRegisterSpriteReachesTheUI(t *testing.T) {
	ui := &spriteUI{mockUIContext: &mockUIContext{}}
	bridge := newTestBridge(ui)
	if result := registerSprite(t, bridge, "ext-a", spriteJSON(t, "blue", extension.LoginMascotWidth)); result.Error != nil {
		t.Fatalf("register sprite error = %+v", result.Error)
	}
	if want := []string{"register ext-a/blue"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
}

func TestUIBridgeRegisterSpriteRejectsAnInvalidDefinition(t *testing.T) {
	ui := &spriteUI{mockUIContext: &mockUIContext{}}
	bridge := newTestBridge(ui)
	result := registerSprite(t, bridge, "ext-a", spriteJSON(t, "blue", extension.LoginMascotWidth-1))
	if result.Error == nil || result.Error.Code != "invalid_sprite" || !strings.Contains(result.Error.Message, "mascot[0]") {
		t.Fatalf("register sprite error = %+v, want invalid_sprite naming mascot[0]", result.Error)
	}
	if len(ui.events) != 0 {
		t.Fatalf("an invalid sprite reached the UI: %q", ui.events)
	}
}

func TestUIBridgeRegisterSpriteReportsTheUIError(t *testing.T) {
	ui := &spriteUI{mockUIContext: &mockUIContext{}, reject: errors.New(`sprite "pig-default" is built in`)}
	bridge := newTestBridge(ui)
	result := registerSprite(t, bridge, "ext-a", spriteJSON(t, "pig-default", extension.LoginMascotWidth))
	if result.Error == nil || result.Error.Code != "ui_error" || !strings.Contains(result.Error.Message, "built in") {
		t.Fatalf("register sprite error = %+v, want the UI's rejection as ui_error", result.Error)
	}
}

// A sprite registered before the UI exists, or while it is replaced (reload), reaches every UI bound later, in a
// deterministic order, and an extension that re-registers an ID replaces its sprite.
func TestUIBridgeReplaysSpritesToEveryNewUI(t *testing.T) {
	bridge := NewUIBridge(func() {})
	registerSprite(t, bridge, "ext-b", spriteJSON(t, "red", extension.LoginMascotWidth))
	registerSprite(t, bridge, "ext-a", spriteJSON(t, "blue", extension.LoginMascotWidth))
	registerSprite(t, bridge, "ext-a", spriteJSON(t, "green", extension.LoginMascotWidth))
	registerSprite(t, bridge, "ext-a", spriteJSON(t, "blue", extension.LoginMascotWidth))
	want := []string{"register ext-a/blue", "register ext-a/green", "register ext-b/red"}
	for range 2 {
		ui := &spriteUI{mockUIContext: &mockUIContext{}}
		bridge.SetUIContext(ui)
		if !slices.Equal(ui.events, want) {
			t.Fatalf("replayed events = %q, want %q", ui.events, want)
		}
	}
}

// An extension that goes away takes its sprites out of the UI and out of later replays; another extension's stay.
func TestUIBridgeClearExtensionDropsItsSprites(t *testing.T) {
	ui := &spriteUI{mockUIContext: &mockUIContext{}}
	bridge := newTestBridge(ui)
	registerSprite(t, bridge, "ext-a", spriteJSON(t, "blue", extension.LoginMascotWidth))
	registerSprite(t, bridge, "ext-b", spriteJSON(t, "red", extension.LoginMascotWidth))
	bridge.ClearExtension("ext-a")
	if want := []string{"register ext-a/blue", "register ext-b/red", "unregister ext-a"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
	bridge.ClearExtension("ext-c")
	if len(ui.events) != 3 {
		t.Fatalf("clearing an extension without sprites touched the UI: %q", ui.events)
	}
	next := &spriteUI{mockUIContext: &mockUIContext{}}
	bridge.SetUIContext(next)
	if want := []string{"register ext-b/red"}; !slices.Equal(next.events, want) {
		t.Fatalf("replayed events after clear = %q, want %q", next.events, want)
	}
}
