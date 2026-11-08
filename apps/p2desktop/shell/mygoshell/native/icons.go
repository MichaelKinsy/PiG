package native

import "github.com/egoist/mygo/ui"

// The icons the web UI draws (24-unit stroke icons in the colour of the text), as MyGo SVGs.
func icon(body string) *ui.SVG {
	svg, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
	if err != nil {
		panic(err)
	}
	return svg
}

var (
	iconPlus     = icon(`<path d="M12 5v14M5 12h14"/>`)
	iconFolder   = icon(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconChevronL = icon(`<path d="m15 6-6 6 6 6"/>`)
	iconChevronR = icon(`<path d="m9 6 6 6-6 6"/>`)
	iconChevronU = icon(`<path d="m6 15 6-6 6 6"/>`)
	iconMoon     = icon(`<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/>`)
	iconSun      = icon(`<circle cx="12" cy="12" r="4"/><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M5.6 18.4 7 17M17 7l1.4-1.4"/>`)
	iconSystem   = icon(`<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>`)
	iconMore     = icon(`<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`)
	iconPin      = icon(`<path d="M12 17v5M9 3h6l-1 6 4 3v2H6v-2l4-3z"/>`)
)

var iconSearch = icon(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`)
