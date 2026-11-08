package native

import (
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/sprite"
	"github.com/egoist/mygo/ui"
)

// markCache keeps a bitmap per sprite and size: the pixels of the sprite's own grid and palette, each cell a whole number
// of pixels, so it draws crisp. Bitmaps are made once, not per frame.
type markCache struct {
	mu    sync.Mutex
	known map[string]*ui.Bitmap
}

var marks markCache

// bitmap is v's grid drawn with scale pixels per cell.
func (m *markCache) bitmap(v sprite.Variant, scale int) *ui.Bitmap {
	key := v.ID + "@" + string(rune('0'+scale))
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.known == nil {
		m.known = map[string]*ui.Bitmap{}
	}
	if b, ok := m.known[key]; ok {
		return b
	}
	b := ui.NewBitmap(sprite.Scaled(v, scale))
	m.known[key] = b
	return b
}

// mark is the user's sprite as the brand mark, cells of cell DIPs: the width is 16 cells, the height 14.
func mark(c *ui.Context, v sprite.Variant, cell int) ui.Element {
	return ui.Image(c, marks.bitmap(v, 2)).Size(float32(16*cell), float32(14*cell)).Fit(ui.Contain)
}
