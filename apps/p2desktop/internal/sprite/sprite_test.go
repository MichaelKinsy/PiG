package sprite

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	return home
}

func TestCatalogueHasTheBuiltInsAndGridsAreCrisp(t *testing.T) {
	all := Catalogue()
	if len(all) < 2 {
		t.Fatalf("catalogue has %d sprites", len(all))
	}
	grid := Grid(all[0])
	if grid.Bounds().Dx() != piglogin.HeadWidth || grid.Bounds().Dy() != piglogin.HeadHeight {
		t.Fatalf("grid %v", grid.Bounds())
	}
	scaled := Scaled(all[0], 3)
	if scaled.Bounds().Dx() != 3*piglogin.HeadWidth {
		t.Fatalf("scaled %v", scaled.Bounds())
	}
	opaque := 0
	for y := range scaled.Bounds().Dy() {
		for x := range scaled.Bounds().Dx() {
			if scaled.NRGBAAt(x, y) != scaled.NRGBAAt(x-x%3, y-y%3) {
				t.Fatalf("pixel %d,%d is not its cell's colour", x, y)
			}
			if scaled.NRGBAAt(x, y).A != 0 {
				opaque++
			}
		}
	}
	if opaque == 0 {
		t.Fatal("the sprite has no pixels")
	}
}

func TestSelectIsTheStateFileOfSpriteAndWatchSeesAnotherWriter(t *testing.T) {
	useHome(t)
	path, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	var moves atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	Watch(ctx, 10*time.Millisecond, func() { moves.Add(1) })
	other := Catalogue()[1].ID
	if err := Select(other); err != nil {
		t.Fatal(err)
	}
	if Active().ID != other {
		t.Fatalf("active %q, want %q", Active().ID, other)
	}
	waitFor(t, func() bool { return moves.Load() >= 1 })
	// Another process (the terminal's /sprite) writes the same file: the app sees it and follows.
	if err := piglogin.SaveVariant(filepath.Dir(filepath.Dir(filepath.Dir(path))), piglogin.DefaultID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return Active().ID == piglogin.DefaultID })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
