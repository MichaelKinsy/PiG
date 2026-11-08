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

var (
	iconSend = icon(`<path d="M12 19V5M5 12l7-7 7 7"/>`)
	iconStop = icon(`<rect x="6" y="6" width="12" height="12" rx="2"/>`)
)

var iconSearch = icon(`<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`)

var (
	iconCheck    = icon(`<path d="m5 12.5 4.5 4.5L19 7"/>`)
	iconSpark    = icon(`<path d="M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6"/>`)
	iconTerminal = icon(`<path d="m5 8 4 4-4 4M12 16h7"/>`)
	iconFile     = icon(`<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>`)
	iconPencil   = icon(`<path d="M4 20h4L19 9l-4-4L4 16z"/>`)
	iconTool     = icon(`<path d="M14.5 6.5a4 4 0 0 0 4.5 5L10 20.5a2.1 2.1 0 0 1-3-3L15.5 9a4 4 0 0 0-1-2.5z"/>`)
	iconUsers    = icon(`<circle cx="9" cy="8" r="3"/><path d="M3 20a6 6 0 0 1 12 0M16 5a3 3 0 0 1 0 6M18 20a5 5 0 0 0-3-4.6"/>`)
)

var iconCopy = icon(`<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h8"/>`)
