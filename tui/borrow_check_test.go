//go:build pigborrowcheck

package tui

import "testing"

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: no panic after an index write into the borrowed lines", name)
		}
	}()
	fn()
}

// Under -tags pigborrowcheck a write into the slice Container.Render or Box.Render returned panics at the next render of the same component.
func TestBorrowCheckCatchesAWriteIntoContainerRender(t *testing.T) {
	container := NewContainer(&cacheProbeComponent{lines: []string{"one", "two"}})
	lines := container.Render(40)
	lines[0] = "written"
	mustPanic(t, "container", func() { container.Render(40) })
}

func TestBorrowCheckCatchesAWriteIntoBoxRender(t *testing.T) {
	box := NewPaddedBox(1, 0, nil)
	box.AddChild(&cacheProbeComponent{lines: []string{"one"}})
	lines := box.Render(40)
	lines[0] = "written"
	mustPanic(t, "box", func() { box.Render(40) })
}
