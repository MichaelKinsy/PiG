package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

func registerBridgeSprite(t *testing.T, bridge *subprocess.UIBridge, ext, id string) *subprocess.CallResultPayload {
	t.Helper()
	args, err := json.Marshal(extension.SpriteDefinition{
		ID: id, Name: "Sprite " + id, Tagline: "tagline",
		Mascot:  slices.Repeat([]string{strings.Repeat("A", extension.LoginMascotWidth)}, extension.LoginMascotHeight),
		Palette: map[string]string{"A": "#112233"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := bridge.HandleCall(ext, &subprocess.CallPayload{Method: subprocess.CallUIRegisterSprite, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// D2: a sprite leaves /sprite when its extension goes away. A Session replacement (/new, /resume, /fork) binds the
// replacement build's bridge to the mode after detaching the outgoing one, and the outgoing host shuts down only after
// that, when its bridge no longer reaches the UI. The outgoing build's sprites must not outlive it: the catalogue holds
// exactly the bound build's sprites, so a sprite whose extension the replacement does not load is gone and another
// extension may take its ID.
func TestSessionReplacementDropsTheOutgoingBuildsSprites(t *testing.T) {
	m := newHeaderMode(t)
	t.Cleanup(func() {
		piglogin.Unregister("old-ext")
		piglogin.Unregister("kept-ext")
		piglogin.Unregister("new-ext")
	})
	old := subprocess.NewUIBridge(func() {})
	m.opts.SubprocessUIBridge = old
	m.attachSubprocess()
	for _, sprite := range [][2]string{{"old-ext", "old-pig"}, {"kept-ext", "kept-pig"}} {
		if result := registerBridgeSprite(t, old, sprite[0], sprite[1]); result.Error != nil {
			t.Fatalf("register %s = %+v", sprite[1], result.Error)
		}
	}
	if _, ok := piglogin.ByID("old-pig"); !ok {
		t.Fatal("the registered sprite is not in /sprite")
	}

	// The replacement build loads kept-ext again and a new extension; old-ext is not loaded there.
	replacement := subprocess.NewUIBridge(func() {})
	if result := registerBridgeSprite(t, replacement, "kept-ext", "kept-pig"); result.Error != nil {
		t.Fatalf("register kept-pig in the replacement = %+v", result.Error)
	}
	m.detachSubprocess()
	m.opts.SubprocessUIBridge = replacement
	m.attachSubprocess()
	old.ClearExtension("old-ext")
	old.ClearExtension("kept-ext")

	if _, ok := piglogin.ByID("old-pig"); ok {
		t.Fatalf("/sprite still offers the outgoing build's sprite: %s", piglogin.IDs())
	}
	if _, ok := piglogin.ByID("kept-pig"); !ok {
		t.Fatalf("/sprite lost the replacement build's sprite: %s", piglogin.IDs())
	}
	if result := registerBridgeSprite(t, replacement, "new-ext", "old-pig"); result.Error != nil {
		t.Fatalf("another extension cannot take the gone sprite's ID: %+v", result.Error)
	}

	// A replacement build without extensions has no bridge; the outgoing sprites leave /sprite all the same.
	m.detachSubprocess()
	m.opts.SubprocessUIBridge = nil
	m.attachSubprocess()
	for _, id := range []string{"old-pig", "kept-pig"} {
		if _, ok := piglogin.ByID(id); ok {
			t.Fatalf("/sprite still offers %s after a build without extensions: %s", id, piglogin.IDs())
		}
	}
}
