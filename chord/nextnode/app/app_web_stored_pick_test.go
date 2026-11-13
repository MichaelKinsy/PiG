package app

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

// Not upstream's: Pi has no test of a pick that is stored before a page opens, and its page does not apply one. D636: a page
// applies the language and the theme its user chose earlier when it opens (the followers apply the value their watch of the
// pick was acquired with before they start its listener: automation/gen/chord-patches/D636-apply-acquired-pick.patch over Pi's
// surfaces/active-language.ts:57-66 and surfaces/active-theme.ts:43-52). In a real Chromium, as app_web_browser_test.go;
// chord/next/surfaces has the same rows for the Go ports of the followers.

// lightTheme is a web plugin contributing one light theme, told by its accent.
func lightTheme(id, accent string) map[string]string {
	colors := `{ accent: "` + accent + `", bg: "#ffffff", fg: "#000000", muted: "#888888", error: "#ff0000", warning: "#ffff00", success: "#00ff00" }`
	return map[string]string{
		"web.ts": `import { defineFacet, Themes } from "@test/app/v1";
export default defineFacet((_host, self) => {
	self.contribute(Themes, { id: "` + id + `", name: "` + id + `", mode: "light", colors: ` + colors + ` });
});`,
	}
}

func TestThePagesLanguageFollowsAPickStoredBeforeThePageOpens(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{"arabic": arabic}, nil)
	// Stored before the page opens.
	chooseLocale(t, s, "ar")
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.documentElement.dir === "rtl"`)
	if got := browser.String(`document.documentElement.lang`); got != "ar" {
		t.Fatalf("lang = %q, want ar", got)
	}
	browser.Wait(`document.getElementById("greeting")?.textContent === "\u0645\u0631\u062d\u0628\u0627, 2 clicks"`)
	// The pick's next change is followed as before.
	chooseLocale(t, s, "de")
	browser.Wait(`document.documentElement.lang === "de"`)
	if got := browser.String(`document.documentElement.dir`); got != "ltr" {
		t.Fatalf("dir = %q, want ltr", got)
	}
}

func TestAPageAppliesAThemeStoredBeforeItOpens(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{
		"first":  lightTheme("first", "#111111"),
		"second": lightTheme("second", "#222222"),
		"third":  lightTheme("third", "#333333"),
	}, nil)
	browser.Emulate("prefers-color-scheme", "light")
	// Stored before the page opens.
	chooseTheme(t, s, "second")
	signIn(browser, s.app.OwnerLink())
	accent := func(color string) string {
		return `getComputedStyle(document.documentElement).getPropertyValue("--accent").trim() === "` + color + `"`
	}
	browser.Wait(accent("#222222"))
	// The pick's next change is followed as before.
	chooseTheme(t, s, "third")
	browser.Wait(accent("#333333"))
}
