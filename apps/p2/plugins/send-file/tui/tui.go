// Package tui is sent files in the TUI (p2-tui-port.md P11c), after the web's file card and viewer. The card names the file and its size. `/downloads` lists
// the files sent in the loaded transcript: Enter previews text (`SentFiles.preview`), Ctrl+S saves it through the launcher's `download` (bounded, never
// overwriting) and Ctrl+B opens the download in the browser (launcher `openUrl`; the browser needs its own sign-in). The worker never fetches “/download` itself.
//
// Ports apps/p2/plugins/send-file/tui.ts.
package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

var idPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type sentFile struct {
	ID, Name string
	Size     float64
}

// sentFileOf is the web card's checks: a snapshot ID, a name and a size.
func sentFileOf(details any) *sentFile {
	value, _ := details.(*jsvalue.Object)
	id, ok1 := value.Lookup("id").(string)
	name, ok2 := value.Lookup("name").(string)
	size, ok3 := value.Lookup("size").(float64)
	if !ok1 || !idPattern.MatchString(id) || !ok2 || !ok3 || size < 0 {
		return nil
	}
	return &sentFile{ID: id, Name: name, Size: size}
}

// Renderers is the send_file tool's card.
var Renderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		path, valid := tools.Str(args.Lookup("path"))
		text.SetText(t.Fg("toolTitle", t.Bold("send_file")) + " " + tools.RenderToolPath(path, valid, t, context.Cwd, tools.RenderToolPathOptions{}))
		return text
	},
	RenderResult: func(result tools.ToolResultView, _ tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		var file *sentFile
		if !context.IsError {
			file = sentFileOf(result.Details)
		}
		if file != nil {
			text.SetText("\n" + t.Fg("success", "■") + " " + render.LineText(file.Name) + " " + t.Fg("muted", "· "+tools.FormatSize(int(file.Size))+" · ready to download") + " " + t.Fg("dim", "· /downloads saves"))
		} else {
			color := "toolOutput"
			if context.IsError {
				color = "error"
			}
			text.SetText("\n" + t.Fg(color, tools.GetTextOutput(&result)))
		}
		return text
	},
}

func preview(ctx runtime.TuiContext, session string, file sentFile) {
	lines := []string{ctx.Theme().Current().Fg("muted", "Loading…")}
	var ref *host.Reference
	var viewer runtime.TuiOverlayHandle
	viewer = ctx.Overlay("download-view", components.NewTextViewer(runtime.Selectors(ctx), components.TextViewerOptions{
		Title: file.Name + " · " + tools.FormatSize(int(file.Size)),
		Lines: func(int) []string { return lines },
		OnClose: func() {
			if ref != nil {
				ref.Release()
			}
			viewer.Close()
		},
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	held, err := ctx.Host().Hold(context.Background(), contract.SentFiles, delta.JsonObjectOf("session", session), host.GetOptions{})
	if err != nil {
		lines = []string{err.Error()}
		return
	}
	ref = held
	go func() {
		var result any
		err := p2runtime.WhenLive(context.Background(), ref)
		if err == nil {
			if variant, _, _ := ref.Variant(); p2runtime.StatusOf(ref) != host.RefStateLive || variant != "reader" {
				err = errors.New("Sent files are not available")
			}
		}
		if err == nil {
			result, err = ref.Call("preview", delta.JsonObjectOf("id", file.ID), context.Background())
		}
		ctx.Post(func() {
			t := ctx.Theme().Current()
			switch {
			case err != nil:
				lines = []string{err.Error()}
			case p2tui.JField(result, "text") == nil:
				lines = []string{t.Fg("muted", "Not a text file: Ctrl+S in /downloads saves it.")}
			default:
				lines = nil
				for _, line := range strings.Split(p2tui.JStr(p2tui.JField(result, "text")), "\n") {
					lines = append(lines, render.LineText(line))
				}
				if p2tui.JBool(p2tui.JField(result, "truncated")) {
					lines = append(lines, "", t.Fg("warning", "… truncated: Ctrl+S in /downloads saves all of it"))
				}
			}
			ctx.RequestRender()
		})
	}()
}

func openDownloads(ctx runtime.TuiContext) error {
	session := ctx.Identity().Session
	chat := runtime.ChatSurfaceOf(ctx)
	if session == "" || chat == nil {
		return nil
	}
	// Newest first; one row per snapshot.
	files := func() []sentFile {
		var all []sentFile
		for _, tool := range chat.ToolViews("send_file") {
			if file := sentFileOf(tool.Details); file != nil {
				all = append(all, *file)
			}
		}
		slices.Reverse(all)
		seen := map[string]bool{}
		var unique []sentFile
		for _, file := range all {
			if !seen[file.ID] {
				seen[file.ID] = true
				unique = append(unique, file)
			}
		}
		return unique
	}
	byRow := func(row *components.ListRow) *sentFile {
		if row == nil {
			return nil
		}
		for _, file := range files() {
			if file.ID == row.Key {
				return &file
			}
		}
		return nil
	}
	grant := func(action string, payload map[string]any, done string) {
		go func() {
			result := ctx.Grant(action, payload)
			ctx.Post(func() { p2tui.GrantNotice(ctx, done, result) })
		}()
	}
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("downloads", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("Sent files (%d)", len(files())) },
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for _, file := range files() {
				rows = append(rows, components.ListRow{Key: file.ID, Label: file.Name, Detail: tools.FormatSize(int(file.Size))})
			}
			return rows
		},
		OnSelect: func(row components.ListRow) {
			if file := byRow(&row); file != nil {
				preview(ctx, session, *file)
			}
		},
		Actions: []components.ListAction{
			{Key: "ctrl+s", Label: "save", Run: func(row *components.ListRow) {
				if file := byRow(row); file != nil {
					grant("download", map[string]any{"kind": "file", "session": session, "id": file.ID, "name": file.Name}, "Saved "+file.Name)
				}
			}},
			{Key: "ctrl+b", Label: "open in browser", Run: func(row *components.ListRow) {
				if file := byRow(row); file != nil {
					grant("openUrl", map[string]any{"kind": "origin", "path": "/download?session=" + p2tui.URLSearchEscape(session) + "&id=" + p2tui.URLSearchEscape(file.ID)}, "Opened "+file.Name+" in the browser")
				}
			}},
		},
		Footer: func() []string {
			return []string{ctx.Theme().Current().Fg("dim", "  Files sent in the loaded transcript; the browser needs its own sign-in")}
		},
		OnClose: func() { overlay.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.downloads", runtime.TuiCommand{
		Title: "Sent files", Category: "Session", When: "chat && !overlay", Slash: &runtime.TuiCommandSlash{Name: "downloads"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openDownloads(ctx) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-send-file", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiToolRenderers, p2runtime.Entry("send_file", Renderers))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
