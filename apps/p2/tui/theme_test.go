package tui_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// localTheme is Pi's `local({ theme })` of tui-theme.test.ts: empty local preferences with a theme setting.
func localTheme(name string) *prefs.TuiLocalPreferences {
	local := prefs.EmptyPreferences()
	local.Theme = name
	return &local
}

func heading(ref *theme.ThemeRef) string {
	markdown := pitui.NewMarkdownWithOptions("# Title", 0, 0, &ref.Markdown, nil, nil)
	return joinLines(markdown.Render(40))
}

func joinLines(lines []string) string {
	text := ""
	for i, line := range lines {
		if i > 0 {
			text += "\n"
		}
		text += line
	}
	return text
}

func TestTwoRuntimesInOneProcessKeepTheirOwnThemesPreviewsAndTerminalColors(t *testing.T) {
	darkRuntime := tuitest.New(t, tuitest.Options{Columns: 60, Rows: 20})
	lightRuntime := tuitest.New(t, tuitest.Options{Columns: 60, Rows: 20})
	var dark, light *runtime.TuiRuntime
	dark, light = darkRuntime.Runtime, lightRuntime.Runtime
	darkRuntime.Do(func() { dark.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("dark")}) })
	lightRuntime.Do(func() { light.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("light")}) })

	var component *pitui.Markdown
	var before, darkHeading, lightHeading string
	var version int
	darkRuntime.Do(func() {
		if name := dark.Appearance.Theme.Current().Name; name != "dark" {
			t.Errorf("dark runtime theme = %q", name)
		}
		darkHeading = heading(dark.Appearance.Theme)
	})
	lightRuntime.Do(func() {
		if name := light.Appearance.Theme.Current().Name; name != "light" {
			t.Errorf("light runtime theme = %q", name)
		}
		lightHeading = heading(light.Appearance.Theme)
	})
	if darkHeading == lightHeading {
		t.Fatal("different SGR per instance")
	}

	// A rendered component built on the ref's derived theme picks up a swap without being rebuilt.
	darkRuntime.Do(func() {
		component = pitui.NewMarkdownWithOptions("# Title", 0, 0, &dark.Appearance.Theme.Markdown, nil, nil)
		before = joinLines(component.Render(40))
		version = dark.Appearance.Theme.Version
		dark.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("light")})
		if dark.Appearance.Theme.Version != version+1 {
			t.Errorf("version = %d, want %d", dark.Appearance.Theme.Version, version+1)
		}
		component.Invalidate()
		after := joinLines(component.Render(40))
		if after != lightHeading {
			t.Errorf("a swapped theme did not reach the built component: %q != %q", after, lightHeading)
		}
		if after == before {
			t.Error("the component did not change with the swap")
		}
		// Unchanged inputs do not swap (no needless rebuilds).
		dark.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("light")})
		if dark.Appearance.Theme.Version != version+1 {
			t.Errorf("an unchanged update swapped the theme: version %d", dark.Appearance.Theme.Version)
		}

		// A picker preview uses its own ref; the live one is untouched.
		created, err := theme.CreateTheme(theme.DarkTheme, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
		if err != nil {
			t.Fatal(err)
		}
		preview := theme.NewThemeRef(created)
		if heading(preview) == heading(dark.Appearance.Theme) {
			t.Error("the preview renders like the live theme")
		}
		if name := dark.Appearance.Theme.Current().Name; name != "light" {
			t.Errorf("the preview touched the live theme: %q", name)
		}
	})

	// Terminal colors reach only the instance they were given to (system theme follows them).
	var untouched string
	darkRuntime.Do(func() { dark.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("")}) })
	lightRuntime.Do(func() {
		light.Appearance.Update(runtime.AppearanceUpdate{Local: localTheme("")})
		untouched = light.Appearance.Theme.Current().Fg("accent", "x")
	})
	darkRuntime.Do(func() {
		dark.Appearance.Update(runtime.AppearanceUpdate{Terminal: &theme.TerminalState{Colors: pitui.TerminalColors{
			Background: &pitui.RgbColor{R: 250, G: 250, B: 250}, Foreground: &pitui.RgbColor{R: 10, G: 10, B: 10},
		}}})
		if got := dark.Appearance.Theme.Current().Appearance(); got != "light" {
			t.Errorf("dark runtime appearance = %q, want light", got)
		}
	})
	lightRuntime.Do(func() {
		if got := light.Appearance.Theme.Current().Appearance(); got != "dark" {
			t.Errorf("light runtime appearance = %q, want dark (never given the colors)", got)
		}
		if got := light.Appearance.Theme.Current().Fg("accent", "x"); got != untouched {
			t.Errorf("the other runtime's colors leaked: %q != %q", got, untouched)
		}
	})
}
