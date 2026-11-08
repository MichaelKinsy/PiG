package components

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	pitui "github.com/MichaelKinsy/PiG/tui"
)

func pasted(text string) string { return "\x1b[200~" + text + "\x1b[201~" }

func ups(n int) []string { return slices.Repeat([]string{keyUpArrow}, n) }

func init() {
	statusScene := func(build func(ui pitui.TUI, ctx oracleContext) *StatusIndicator) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			screen, _ := newScreen(t, 80, 24)
			indicator := build(screen, ctx)
			return oracleScene{component: indicator.Loader, cleanup: indicator.Dispose}
		}
	}
	registerScene("status-working", statusScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewWorkingStatusIndicator(ui, ctx, "Working 日本語 on a long message that wraps around at narrow widths", nil)
	}))
	registerScene("status-working-color", statusScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewWorkingStatusIndicator(ui, ctx, "Plain", func(text string) string { return "<" + text + ">" })
	}))
	registerScene("status-retry", statusScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewRetryStatusIndicator(context.Background(), ui, ctx, 2, 0, "rate limited\x1b]0;x\x07")
	}))
	for _, trigger := range []string{"manual", "overflow", "threshold"} {
		registerScene("status-compaction-"+trigger, statusScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
			return NewCompactionStatusIndicator(ui, ctx, trigger, nil)
		}))
	}
	registerScene("status-compaction-retry", statusScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewCompactionStatusIndicator(ui, ctx, "threshold", new(1))
	}))

	longText := func() string {
		lines := make([]string, 14)
		for i := range lines {
			lines[i] = fmt.Sprintf("editor line %d 日本語", i)
		}
		return strings.Join(lines, "\n")
	}()
	editorScene := func(indicator func(ui pitui.TUI, ctx oracleContext) *StatusIndicator, inputs func() []string) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			screen, _ := newScreen(t, 80, 24)
			editor := NewCustomEditor(screen, ctx, pitui.EditorOptions{})
			scene := oracleScene{component: editor, focus: new(true), inputs: inputs()}
			if indicator != nil {
				status := indicator(screen, ctx)
				editor.SetWorkingStatusIndicator(status)
				scene.cleanup = status.Dispose
			}
			return scene
		}
	}
	registerScene("editor-plain", editorScene(nil, func() []string { return []string{pasted("hello"), pasted(longText), keyUpArrow, keyUpArrow} }))
	registerScene("editor-working", editorScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewWorkingStatusIndicator(ui, ctx, "Working...", nil)
	}, func() []string { return append([]string{pasted("hello"), pasted(longText)}, ups(8)...) }))
	registerScene("editor-working-long", editorScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewWorkingStatusIndicator(ui, ctx, "Working on a long message that does not fit the border at narrow widths 日本語", nil)
	}, func() []string { return append([]string{pasted(longText)}, ups(10)...) }))
	registerScene("editor-retry", editorScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewRetryStatusIndicator(context.Background(), ui, ctx, 1, 0, "overloaded")
	}, func() []string { return []string{pasted("one\ntwo"), pasted(longText)} }))
	registerScene("editor-compaction", editorScene(func(ui pitui.TUI, ctx oracleContext) *StatusIndicator {
		return NewCompactionStatusIndicator(ui, ctx, "manual", nil)
	}, func() []string { return append([]string{pasted(longText)}, ups(14)...) }))
}
