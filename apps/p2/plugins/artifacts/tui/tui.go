// Package tui is artifacts in the TUI (p2-tui-port.md P11c), after the web's artifact card, picker and viewer. The card shows the title, type and version;
// expanded, a text or Markdown artifact shows the content the call wrote. A call with `inline: true` shows all of it at once (Markdown rendered); inline HTML and
// SVG keep the card line. `/artifacts` lists the conversation's artifacts (`ArtifactList`): Enter shows one inline (`ArtifactContent`; Markdown rendered, HTML
// and SVG as source; in the side panel, where it follows new versions, when the terminal is wide enough), Ctrl+B opens it in the browser (launcher `openUrl`; the
// browser needs its own sign-in) and Ctrl+S downloads it (launcher `download`). The worker never fetches the `/artifact` route itself: it has no cookie.
//
// Ports apps/p2/plugins/artifacts/tui.ts.
package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/contract"
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

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// previewLines: collapsed cards show this many lines of a text or Markdown artifact; expanded ones all.
const previewLines = 8

const browserNote = "the browser needs its own sign-in to this server"

// Renderers is the artifact tool's card.
var Renderers = &tools.ToolRenderers{
	RenderCall: func(args tools.ToolArgs, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		text, ok := context.LastComponent.(*pitui.Text)
		if !ok {
			text = pitui.NewPaddedText("", 0, 0, nil)
		}
		title, _ := args.Lookup("title").(string)
		if title == "" {
			title, _ = args.Lookup("name").(string)
		}
		if title == "" {
			title = "..."
		}
		text.SetText(t.Fg("toolTitle", t.Bold("artifact")) + " " + t.Fg("accent", render.LineText(title)))
		return text
	},
	RenderResult: func(result tools.ToolResultView, options tools.ToolRenderResultOptions, t *theme.Theme, context *tools.ToolRenderContext) pitui.Component {
		component, ok := context.LastComponent.(*pitui.Container)
		if !ok {
			component = pitui.NewContainer()
		}
		component.Clear()
		component.AddChild(pitui.NewSpacer(1))
		details, _ := result.Details.(*jsvalue.Object)
		title, hasTitle := details.Lookup("title").(string)
		kind, hasType := details.Lookup("type").(string)
		version, hasVersion := details.Lookup("version").(float64)
		if context.IsError || !hasTitle || !hasType || !hasVersion {
			color := "toolOutput"
			if context.IsError {
				color = "error"
			}
			component.AddChild(pitui.NewPaddedText(t.Fg(color, tools.GetTextOutput(&result)), 0, 0, nil))
			return component
		}
		component.AddChild(pitui.NewPaddedText(t.Fg("success", "■")+" "+render.LineText(title)+" "+t.Fg("muted", "· "+strings.ToUpper(render.LineText(kind))+" · version "+jsvalue.String(version))+" "+t.Fg("dim", "· /artifacts opens"), 0, 0, nil))
		// What the call wrote (text and Markdown; HTML and SVG run only in a browser): the version this call produced.
		content, _ := context.Args.Lookup("content").(string)
		if content == "" || (kind != "text" && kind != "markdown") {
			return component
		}
		component.AddChild(pitui.NewSpacer(1))
		inline := details.Lookup("inline") == true
		if inline && kind == "markdown" {
			markdown := context.UI.Theme().Markdown
			component.AddChild(pitui.NewMarkdownWithOptions(render.SafeText(content), 0, 0, &markdown, nil, nil))
			return component
		}
		var all []string
		for _, line := range strings.Split(content, "\n") {
			all = append(all, t.Fg("toolOutput", render.LineText(line)))
		}
		shown := all
		if !options.Expanded && !inline && len(all) > previewLines {
			shown = all[:previewLines]
		}
		if len(shown) < len(all) {
			shown = append(append([]string(nil), shown...), t.Fg("muted", fmt.Sprintf("... (%d more lines, %s to expand)", len(all)-len(shown), context.UI.KeyText("app.tools.expand"))))
		}
		component.AddChild(pitui.NewPaddedText(strings.Join(shown, "\n"), 0, 0, nil))
		return component
	},
}

type artifactInfo struct {
	name, title, kind string
	version           int
}

func infoOf(value any) artifactInfo {
	return artifactInfo{name: p2tui.JStr(p2tui.JField(value, "name")), title: p2tui.JStr(p2tui.JField(value, "title")), kind: p2tui.JStr(p2tui.JField(value, "type")), version: int(p2tui.JNum(p2tui.JField(value, "version")))}
}

func infosOf(value any) []artifactInfo {
	var out []artifactInfo
	for _, each := range p2tui.JList(value) {
		out = append(out, infoOf(each))
	}
	return out
}

// contentReader reads artifact versions of session through one held `ArtifactContent`, until released.
type contentReader struct{ ref *host.Reference }

func newContentReader(ctx runtime.TuiContext, session string) (*contentReader, error) {
	ref, err := ctx.Host().Hold(context.Background(), contract.ArtifactContent, delta.JsonObjectOf("session", session), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &contentReader{ref}, nil
}

// read is a blocking call (off the owner loop).
func (r *contentReader) read(artifact artifactInfo) (content string, truncated bool, err error) {
	if err := p2runtime.WhenLive(context.Background(), r.ref); err != nil {
		return "", false, err
	}
	if variant, _, _ := r.ref.Variant(); p2runtime.StatusOf(r.ref) != host.RefStateLive || variant != "reader" {
		return "", false, errors.New("Artifacts are not available")
	}
	value, err := r.ref.Call("read", delta.JsonObjectOf("name", artifact.name, "version", float64(artifact.version)), context.Background())
	if err != nil {
		return "", false, err
	}
	return p2tui.JStr(p2tui.JField(value, "content")), p2tui.JBool(p2tui.JField(value, "truncated")), nil
}

func (r *contentReader) release() { r.ref.Release() }

// artifactLines is one version of an artifact as lines at a width (Markdown rendered, text and HTML/SVG source), cached per width; load reads a version and
// renders it once read. Its fields are the owner loop's.
type artifactLines struct {
	ctx      runtime.TuiContext
	content  *string
	status   string
	cache    []string
	cacheFor int
	cached   bool
	artifact artifactInfo
}

func newArtifactLines(ctx runtime.TuiContext, artifact artifactInfo) *artifactLines {
	return &artifactLines{ctx: ctx, status: "Loading…", artifact: artifact}
}

// load reads artifact (a version) and shows it once read; the previous version stays shown meanwhile.
func (a *artifactLines) load(reader *contentReader, artifact artifactInfo) {
	go func() {
		content, truncated, err := reader.read(artifact)
		a.ctx.Post(func() {
			defer func() {
				a.cached = false
				a.ctx.RequestRender()
			}()
			if err != nil {
				a.status = err.Error()
				return
			}
			// Reads may finish out of order: an older version never replaces a newer one.
			if artifact.version < a.artifact.version && a.content != nil {
				return
			}
			a.artifact = artifact
			a.status = ""
			if truncated {
				a.status = "… truncated: Ctrl+S in /artifacts downloads all of it"
			}
			a.content = &content
		})
	}()
}

func (a *artifactLines) render(width int) []string {
	t := a.ctx.Theme().Current()
	if a.content == nil {
		return []string{t.Fg("muted", a.status)}
	}
	if a.cached && a.cacheFor == width {
		return a.cache
	}
	var body []string
	if a.artifact.kind == "markdown" {
		markdown := a.ctx.Theme().Markdown
		body = pitui.NewMarkdownWithOptions(render.SafeText(*a.content), 0, 0, &markdown, nil, nil).Render(width)
	} else {
		for _, line := range strings.Split(*a.content, "\n") {
			body = append(body, render.LineText(line))
		}
	}
	var lines []string
	if a.artifact.kind == "html" || a.artifact.kind == "svg" {
		lines = append(lines, t.Fg("warning", fmt.Sprintf("Source only: Ctrl+B in /artifacts opens it in the browser (%s).", browserNote)), "")
	}
	lines = append(lines, body...)
	if a.status != "" {
		lines = append(lines, "", t.Fg("warning", a.status))
	}
	a.cache, a.cacheFor, a.cached = lines, width, true
	return lines
}

func titleOf(a artifactInfo) string {
	return fmt.Sprintf("%s · %s · version %d", a.title, strings.ToUpper(a.kind), a.version)
}

// showArtifact shows an artifact inline, over the chat.
func showArtifact(ctx runtime.TuiContext, session string, artifact artifactInfo) {
	shown := newArtifactLines(ctx, artifact)
	reader, err := newContentReader(ctx, session)
	if err != nil {
		ctx.Fail(err)
		return
	}
	var viewer runtime.TuiOverlayHandle
	viewer = ctx.Overlay("artifact-view", components.NewTextViewer(runtime.Selectors(ctx), components.TextViewerOptions{
		Title:   titleOf(artifact),
		Lines:   shown.render,
		OnClose: func() { reader.release(); viewer.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	shown.load(reader, artifact)
}

// panelContent is the artifact in the side panel.
type panelContent struct {
	shown   *artifactLines
	dispose func()
}

func (p *panelContent) Render(width int) []string { return p.shown.render(width) }
func (p *panelContent) Invalidate()               { p.shown.cached = false }
func (p *panelContent) Dispose()                  { p.dispose() }

// pinArtifact pins an artifact in the side panel: it shows each new version as the conversation's artifact list reports it.
func pinArtifact(ctx runtime.TuiContext, session string, conversation int, artifact artifactInfo) {
	shown := newArtifactLines(ctx, artifact)
	reader, err := newContentReader(ctx, session)
	if err != nil {
		ctx.Fail(err)
		return
	}
	list, err := ctx.Host().Hold(context.Background(), contract.ArtifactList, delta.JsonObjectOf("session", session, "conversation", float64(conversation)), host.GetOptions{})
	if err != nil {
		reader.release()
		ctx.Fail(err)
		return
	}
	loaded := artifact.version
	shown.load(reader, artifact)
	stop := p2tui.FollowMember(list.Member("artifacts"), func(value delta.JsonValue) {
		// The list has one entry per version.
		var latest *artifactInfo
		for _, candidate := range infosOf(value) {
			if candidate.name == artifact.name && (latest == nil || latest.version < candidate.version) {
				candidate := candidate
				latest = &candidate
			}
		}
		if latest == nil {
			return
		}
		next := *latest
		ctx.Post(func() {
			if next.version <= loaded {
				return
			}
			loaded = next.version
			shown.load(reader, next)
		})
	})
	ctx.Panel().Open("artifact:"+artifact.name, &panelContent{shown, func() { stop(); list.Release(); reader.release() }}, func() string {
		return fmt.Sprintf("%s · %s · v%d", render.LineText(shown.artifact.title), strings.ToUpper(shown.artifact.kind), shown.artifact.version)
	})
}

func openArtifacts(ctx runtime.TuiContext) error {
	identity := ctx.Identity()
	session := identity.Session
	if session == "" {
		return nil
	}
	list, err := ctx.Host().Hold(context.Background(), contract.ArtifactList, delta.JsonObjectOf("session", session, "conversation", float64(identity.Conversation)), host.GetOptions{})
	if err != nil {
		return err
	}
	var artifacts []artifactInfo
	stop := p2tui.FollowMember(list.Member("artifacts"), func(value delta.JsonValue) {
		ctx.Post(func() {
			artifacts = nil
			for _, artifact := range infosOf(value) {
				if namePattern.MatchString(artifact.name) {
					artifacts = append(artifacts, artifact)
				}
			}
			ctx.RequestRender()
		})
	})
	var overlay runtime.TuiOverlayHandle
	closeAll := func() {
		stop()
		list.Release()
		overlay.Close()
	}
	// One entry per version: a row is a name and a version.
	keyOf := func(a artifactInfo) string { return a.name + ":" + strconv.Itoa(a.version) }
	byRow := func(row *components.ListRow) *artifactInfo {
		if row == nil {
			return nil
		}
		for _, a := range artifacts {
			if keyOf(a) == row.Key {
				a := a
				return &a
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
	overlay = ctx.Overlay("artifacts", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("Artifacts (%d)", len(artifacts)) },
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for _, a := range artifacts {
				rows = append(rows, components.ListRow{Key: keyOf(a), Label: a.title, Detail: fmt.Sprintf("%s · v%d", strings.ToUpper(a.kind), a.version)})
			}
			return rows
		},
		OnSelect: func(row components.ListRow) {
			artifact := byRow(&row)
			if artifact == nil {
				return
			}
			if !ctx.Panel().Fits() {
				showArtifact(ctx, session, *artifact)
				return
			}
			closeAll()
			pinArtifact(ctx, session, ctx.Identity().Conversation, *artifact)
		},
		Actions: []components.ListAction{
			{Key: "ctrl+b", Label: "open in browser", Run: func(row *components.ListRow) {
				if a := byRow(row); a != nil {
					query := "session=" + p2tui.URLSearchEscape(session) + "&name=" + p2tui.URLSearchEscape(a.name) + "&version=" + strconv.Itoa(a.version)
					grant("openUrl", map[string]any{"kind": "origin", "path": "/artifact?" + query}, "Opened "+a.title+" in the browser")
				}
			}},
			{Key: "ctrl+s", Label: "download", Run: func(row *components.ListRow) {
				if a := byRow(row); a != nil {
					grant("download", map[string]any{"kind": "artifact", "session": session, "name": a.name, "version": float64(a.version)}, "Downloaded "+a.title)
				}
			}},
		},
		Footer: func() []string {
			where := "here"
			if ctx.Panel().Fits() {
				where = "beside the chat"
			}
			return []string{ctx.Theme().Current().Fg("dim", fmt.Sprintf("  Enter shows it %s; Ctrl+B opens it in the browser (%s)", where, browserNote))}
		},
		OnClose: closeAll,
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.artifacts", runtime.TuiCommand{
		Title: "Artifacts", Category: "Session", When: "chat && !overlay", Slash: &runtime.TuiCommandSlash{Name: "artifacts"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openArtifacts(ctx) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-artifacts", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiToolRenderers, p2runtime.Entry("artifact", Renderers))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
