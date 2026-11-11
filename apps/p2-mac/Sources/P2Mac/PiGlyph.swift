// Ports apps/p2-mac/Sources/P2Mac/PiGlyph.swift (the menu bar glyph is PiG's pixel pig, not Pi's logo)
// The PiG pixel pig as a template image for the menu bar: the tray template of the desktop app (apps/p2desktop), which build-app.sh copies into the bundle.
import AppKit

enum PiGlyph {
	/// A template image of the pig, `size` points square: macOS tints it for the menu bar (black or white). Nil when the bundle lacks the file.
	static func templateImage(size: CGFloat) -> NSImage? {
		guard let url = Bundle.main.url(forResource: "tray-template", withExtension: "png"), let image = NSImage(contentsOf: url) else { return nil }
		image.size = NSSize(width: size, height: size)
		image.isTemplate = true
		image.accessibilityDescription = "PiG"
		return image
	}
}
