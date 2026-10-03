package piglogin_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

// The catalogue and the persisted selection are PiG Standard's piglogin (extension_test.go and login_art_test.go at
// MichaelKinsy/PiG d86eb93), which the Pigpen games read: the IDs and the state file location are a contract with them.
// The character sprites of the owner's earlier PiG piglogin follow the color sprites, with the sheriff, in that
// piglogin's order (owner decision 2026-10-01: the default first, then the colors, then the characters).

var wantIDs = []string{
	"pig-default", "pink", "green", "mint", "sandy", "grey", "blush", "lavender", "cloud",
	"pigrogu", "darth-vader", "kratos", "piglet", "spider-ham", "sheriff",
}

func TestCatalogueHasEverySpriteInOrder(t *testing.T) {
	var ids []string
	for _, variant := range piglogin.Variants {
		ids = append(ids, variant.ID)
		if variant.Name == "" || variant.Tagline == "" {
			t.Errorf("%s has no name or tagline", variant.ID)
		}
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("ids = %v, want %v", ids, wantIDs)
	}
	if piglogin.DefaultID != "pig-default" || piglogin.Default().ID != "pig-default" {
		t.Fatalf("default = %q / %q, want pig-default", piglogin.DefaultID, piglogin.Default().ID)
	}
}

func TestDefaultSpriteIsTheWebsiteGreenPig(t *testing.T) {
	// assets/pig/website-art.txt of the pig-play Package: the modal colors of the website's pixel pig and wordmark.
	got := piglogin.Default()
	if got.Name != "PiG" || got.Tagline != "The minimal coding agent, in Go." {
		t.Errorf("default = %q %q", got.Name, got.Tagline)
	}
	body, snout := got.Body, got.Snout
	if body.R != 0x48 || body.G != 0xA3 || body.B != 0x81 {
		t.Errorf("body = %v, want 48A381", body)
	}
	if snout.R != 0x32 || snout.G != 0x77 || snout.B != 0x5E {
		t.Errorf("snout = %v, want 32775E", snout)
	}
	if period := piglogin.LogoFor(got).Period; period.R != 0x16 || period.G != 0x86 || period.B != 0x6F {
		t.Errorf("period = %v, want 16866F", period)
	}
}

func TestByIDAndFindVariant(t *testing.T) {
	for _, id := range wantIDs {
		variant, ok := piglogin.ByID(id)
		if !ok || variant.ID != id {
			t.Errorf("ByID(%q) = %q, %v", id, variant.ID, ok)
		}
		if got := piglogin.FindVariant(id); got.ID != id {
			t.Errorf("FindVariant(%q) = %q", id, got.ID)
		}
	}
	if _, ok := piglogin.ByID("nope"); ok {
		t.Error("ByID found an unknown sprite")
	}
	if got := piglogin.FindVariant("nope"); got.ID != piglogin.DefaultID {
		t.Errorf("FindVariant(unknown) = %q, want the default", got.ID)
	}
}

func TestVariantStateDefaultsAndPersists(t *testing.T) {
	root := t.TempDir()
	if got := piglogin.LoadVariant(root); got.ID != piglogin.DefaultID {
		t.Fatalf("default variant = %q", got.ID)
	}
	if err := piglogin.SaveVariant(root, "green"); err != nil {
		t.Fatal(err)
	}
	if got := piglogin.LoadVariant(root); got.ID != "green" {
		t.Fatalf("persisted variant = %q, want green", got.ID)
	}
	if want := filepath.Join(root, "state", "pig-standard", "login.json"); piglogin.StatePath(root) != want {
		t.Fatalf("state path = %s, want %s", piglogin.StatePath(root), want)
	}
	data, err := os.ReadFile(piglogin.StatePath(root))
	if err != nil || string(data) != "{\"variant\":\"green\"}\n" {
		t.Fatalf("state file = %q, %v", data, err)
	}
	info, err := os.Stat(piglogin.StatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); !onlyOwner(mode) {
		t.Fatalf("state mode = %o, want owner-only", mode)
	}
}

func TestUnreadableSavedSelectionFallsBackToDefault(t *testing.T) {
	for name, content := range map[string]string{"unknown id": `{"variant":"unknown"}`, "malformed": `{`, "empty": ``, "wrong type": `{"variant":7}`} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := piglogin.StatePath(root)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := piglogin.LoadVariant(root); got.ID != piglogin.DefaultID {
				t.Fatalf("variant = %q, want the default", got.ID)
			}
		})
	}
}

func TestConfigHomeIsPigHomeElseDotPig(t *testing.T) {
	t.Setenv("PIG_HOME", "/somewhere/pig")
	if got := piglogin.ConfigHome(); got != "/somewhere/pig" {
		t.Fatalf("ConfigHome = %q", got)
	}
	t.Setenv("PIG_HOME", "")
	t.Setenv("HOME", "/home/someone")
	t.Setenv("USERPROFILE", "/home/someone")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := piglogin.ConfigHome(), filepath.Join("/home/someone", ".pig"); got != want {
		t.Fatalf("ConfigHome = %q, want %q (the games read the same root; XDG_CONFIG_HOME does not move it)", got, want)
	}
}

func TestActivateSavesAndSelects(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	piglogin.Refresh()
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Fatalf("active = %q before any selection", got.ID)
	}
	if err := piglogin.Activate("sheriff"); err != nil {
		t.Fatal(err)
	}
	if got := piglogin.Active(); got.ID != "sheriff" {
		t.Fatalf("active = %q after Activate(sheriff)", got.ID)
	}
	if got := piglogin.LoadVariant(root); got.ID != "sheriff" {
		t.Fatalf("saved = %q, want sheriff", got.ID)
	}
	if err := piglogin.Activate("nope"); err == nil {
		t.Fatal("Activate accepted an unknown sprite")
	}
	if got := piglogin.Active(); got.ID != "sheriff" {
		t.Fatalf("a rejected Activate changed the active sprite to %q", got.ID)
	}
}

func TestActiveFollowsAnotherWriterAfterRefresh(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	piglogin.Refresh()
	if err := piglogin.SaveVariant(root, "cloud"); err != nil {
		t.Fatal(err)
	}
	piglogin.Refresh()
	if got := piglogin.Active(); got.ID != "cloud" {
		t.Fatalf("active = %q after Refresh, want cloud (a game or another PiG wrote it)", got.ID)
	}
	other := t.TempDir()
	t.Setenv("PIG_HOME", other)
	if got := piglogin.Active(); got.ID != piglogin.DefaultID {
		t.Fatalf("active = %q under another PIG_HOME, want the default", got.ID)
	}
}

func writeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o600) }
