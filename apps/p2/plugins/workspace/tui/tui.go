// Package tui is the workspace in the TUI (p2-tui-port.md P11i), after the web's Workspace panel, read-only: `/files` browses the session directory and shows a
// file, `/changes` lists git changes and shows a change's diff (pi's diff renderer), `/grep` searches text and opens a match at its line. Participants only:
// holding `Workspace` leases the worker's filesystem watch, which read-only guests cannot get. Comments, commits and uploads stay on the web for now.
//
// Ports apps/p2/plugins/workspace/tui.ts.
package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// workspace holds the viewed conversation's workspace, released when the panel closes; nil without a session.
func workspace(ctx runtime.TuiContext) (*host.Reference, error) {
	identity := ctx.Identity()
	if identity.Session == "" {
		return nil, nil
	}
	return ctx.Host().Hold(context.Background(), contract.Workspace, delta.JsonObjectOf("session", identity.Session, "conversation", float64(identity.Conversation)), host.GetOptions{})
}

// query is a blocking call (off the owner loop); the result's kind must be kind.
func query(ref *host.Reference, request *delta.JsonObject, kind string) (any, error) {
	if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
		return nil, err
	}
	if variant, _, _ := ref.Variant(); p2runtime.StatusOf(ref) != host.RefStateLive || variant != "reader" {
		return nil, errors.New("The workspace is not available")
	}
	result, err := ref.Call("query", request, context.Background())
	if err != nil {
		return nil, err
	}
	if got := p2tui.JStr(p2tui.JField(result, "kind")); got != kind {
		return nil, fmt.Errorf("Unexpected workspace result: %s", got)
	}
	return result, nil
}

// ask runs query off the loop and hands its outcome to done on the loop, then redraws.
func ask(ctx runtime.TuiContext, ref *host.Reference, request *delta.JsonObject, kind string, done func(result any, err error)) {
	go func() {
		result, err := query(ref, request, kind)
		ctx.Post(func() {
			done(result, err)
			ctx.RequestRender()
		})
	}()
}

var (
	leadingSlashes  = regexp.MustCompile(`^\.?/+`)
	trailingSlashes = regexp.MustCompile(`/+$`)
	hunkPattern     = regexp.MustCompile(`(?m)^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	hunkLinePart    = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
)

func padStart(n, width int) string {
	text := strconv.Itoa(n)
	if len(text) < width {
		text = strings.Repeat(" ", width-len(text)) + text
	}
	return text
}

// PiDiff is git's unified patch in pi's diff line format ("+12 text", "-12 text", " 12 text", "     ..."), so the copied renderer (intra-line highlighting)
// applies. File headers are dropped; a hunk boundary becomes "...".
func PiDiff(patch string) string {
	var out []string
	oldLine, newLine, width := 0, 0, 1
	for _, match := range hunkPattern.FindAllStringSubmatch(patch, -1) {
		count := func(index int) int {
			if match[index] == "" {
				return 1
			}
			n, _ := strconv.Atoi(match[index])
			return n
		}
		start := func(index int) int { n, _ := strconv.Atoi(match[index]); return n }
		width = max(width, len(strconv.Itoa(start(3)+count(4))), len(strconv.Itoa(start(1)+count(2))))
	}
	inHunk := false
	for _, raw := range strings.Split(patch, "\n") {
		if hunk := hunkLinePart.FindStringSubmatch(raw); hunk != nil {
			if inHunk {
				out = append(out, strings.Repeat(" ", width+1)+" ...")
			}
			oldLine, _ = strconv.Atoi(hunk[1])
			newLine, _ = strconv.Atoi(hunk[2])
			inHunk = true
			continue
		}
		if !inHunk || strings.HasPrefix(raw, "\\") {
			continue
		}
		var body string
		if len(raw) > 0 {
			body = render.LineText(raw[1:])
		} else {
			body = render.LineText("")
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			out = append(out, "+"+padStart(newLine, width)+" "+body)
			newLine++
		case strings.HasPrefix(raw, "-"):
			out = append(out, "-"+padStart(oldLine, width)+" "+body)
			oldLine++
		case strings.HasPrefix(raw, " "):
			out = append(out, " "+padStart(newLine, width)+" "+body)
			oldLine++
			newLine++
		case strings.HasPrefix(raw, "diff "):
			inHunk = false
		}
	}
	return strings.Join(out, "\n")
}

// showFile shows a file (optionally at a line) above the list it came from.
func showFile(ctx runtime.TuiContext, ref *host.Reference, path string, line int) {
	lines := []string{"Loading…"}
	var viewer runtime.TuiOverlayHandle
	viewer = ctx.Overlay("workspace-file", components.NewTextViewer(runtime.Selectors(ctx), components.TextViewerOptions{
		Title: path, Lines: func(int) []string { return lines }, OnClose: func() { viewer.Close() }, Line: line,
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	ask(ctx, ref, delta.JsonObjectOf("kind", "file", "path", path), "file", func(file any, err error) {
		if err != nil {
			lines = []string{err.Error()}
			return
		}
		text, hasText := p2tui.JField(file, "text").(string)
		if p2tui.JBool(p2tui.JField(file, "binary")) || !hasText {
			lines = []string{fmt.Sprintf("Binary file, %s bytes", formatNumber(p2tui.JNum(p2tui.JField(file, "size"))))}
			return
		}
		pieces := strings.Split(text, "\n")
		width := len(strconv.Itoa(len(pieces)))
		t := ctx.Theme().Current()
		lines = nil
		for index, piece := range pieces {
			color := "dim"
			if index+1 == line {
				color = "accent"
			}
			lines = append(lines, t.Fg(color, padStart(index+1, width))+" "+render.LineText(piece))
		}
		if p2tui.JBool(p2tui.JField(file, "truncated")) {
			lines = append(lines, t.Fg("warning", "… truncated (the web panel shows more)"))
		}
	})
}

func formatNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

func showDiff(ctx runtime.TuiContext, ref *host.Reference, path string) {
	lines := []string{"Loading…"}
	var viewer runtime.TuiOverlayHandle
	viewer = ctx.Overlay("workspace-diff", components.NewTextViewer(runtime.Selectors(ctx), components.TextViewerOptions{
		Title: "Changes: " + path, Lines: func(int) []string { return lines }, OnClose: func() { viewer.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	ask(ctx, ref, delta.JsonObjectOf("kind", "diff", "path", path), "diff", func(diff any, err error) {
		if err != nil {
			lines = []string{err.Error()}
			return
		}
		converted := PiDiff(p2tui.JStr(p2tui.JField(diff, "patch")))
		if converted != "" {
			lines = strings.Split(components.RenderDiff(converted, ctx.Theme().Current()), "\n")
		} else {
			lines = []string{"No textual changes"}
		}
		if p2tui.JBool(p2tui.JField(diff, "truncated")) {
			lines = append(lines, ctx.Theme().Current().Fg("warning", "… truncated"))
		}
	})
}

// panel opens a panel with a workspace reference that is released when it closes.
func panel(ctx runtime.TuiContext, id string, build func(ref *host.Reference, close func()) components.ListOverlayOptions) error {
	ref, err := workspace(ctx)
	if err != nil || ref == nil {
		return err
	}
	var overlay runtime.TuiOverlayHandle
	closeAll := func() {
		ref.Release()
		overlay.Close()
	}
	overlay = ctx.Overlay(id, components.NewListOverlay(runtime.Selectors(ctx), build(ref, closeAll)), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

// localeLess is a.localeCompare(b) < 0 for the names a listing shows: case-insensitive first, lowercase before uppercase on a tie.
func localeCompare(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return -strings.Compare(a, b)
}

func footerStatus(ctx runtime.TuiContext, status *string) func() []string {
	return func() []string {
		if *status == "" {
			return nil
		}
		return []string{ctx.Theme().Current().Fg("muted", "  "+render.LineText(*status))}
	}
}

// openFiles is `/files [dir]`: the directory's entries, directories first; Enter opens a directory or shows a file.
func openFiles(ctx runtime.TuiContext, start string) error {
	return panel(ctx, "files", func(ref *host.Reference, closeAll func()) components.ListOverlayOptions {
		// start.replace(/^\.?\/+|\/+$/g, "")
		dir := trailingSlashes.ReplaceAllString(leadingSlashes.ReplaceAllString(start, ""), "")
		var rows []components.ListRow
		status := "Loading…"
		var load func()
		load = func() {
			status = "Loading…"
			ask(ctx, ref, delta.JsonObjectOf("kind", "list", "path", dir), "list", func(list any, err error) {
				if err != nil {
					rows, status = nil, err.Error()
					return
				}
				type entry struct {
					name, kind string
					size       float64
					ignored    bool
				}
				var entries []entry
				for _, each := range p2tui.JList(p2tui.JField(list, "entries")) {
					entries = append(entries, entry{p2tui.JStr(p2tui.JField(each, "name")), p2tui.JStr(p2tui.JField(each, "kind")), p2tui.JNum(p2tui.JField(each, "size")), p2tui.JBool(p2tui.JField(each, "ignored"))})
				}
				slices.SortStableFunc(entries, func(a, b entry) int {
					da, db := 0, 0
					if a.kind == "directory" {
						da = 1
					}
					if b.kind == "directory" {
						db = 1
					}
					if da != db {
						return db - da
					}
					return localeCompare(a.name, b.name)
				})
				rows = nil
				if dir != "" {
					rows = append(rows, components.ListRow{Key: "..", Label: "../"})
				}
				for _, each := range entries {
					row := components.ListRow{Key: each.name, Label: each.name}
					if each.kind == "directory" {
						row.Label += "/"
					}
					switch each.kind {
					case "file":
						row.Detail = formatNumber(each.size) + " B"
					case "symlink":
						row.Detail = "link"
					}
					if each.ignored {
						row.Color = "muted"
					}
					rows = append(rows, row)
				}
				status = ""
				if p2tui.JBool(p2tui.JField(list, "truncated")) {
					status = "Listing truncated"
				}
			})
		}
		load()
		return components.ListOverlayOptions{
			Title: func() string {
				shown := dir
				if shown == "" {
					shown = "."
				}
				return "Files: " + shown
			},
			Rows: func() []components.ListRow { return rows },
			OnSelect: func(row components.ListRow) {
				switch {
				case row.Key == "..":
					parts := strings.Split(dir, "/")
					dir = strings.Join(parts[:len(parts)-1], "/")
					load()
				case strings.HasSuffix(row.Label, "/"):
					if dir != "" {
						dir += "/"
					}
					dir += row.Key
					load()
				default:
					path := row.Key
					if dir != "" {
						path = dir + "/" + row.Key
					}
					showFile(ctx, ref, path, 0)
				}
			},
			Footer:  footerStatus(ctx, &status),
			OnClose: closeAll,
		}
	})
}

// openChanges is `/changes`: the working tree's git status; Enter shows the change's diff.
func openChanges(ctx runtime.TuiContext) error {
	return panel(ctx, "changes", func(ref *host.Reference, closeAll func()) components.ListOverlayOptions {
		var rows []components.ListRow
		status, branch := "Loading…", ""
		ask(ctx, ref, delta.JsonObjectOf("kind", "status"), "status", func(result any, err error) {
			if err != nil {
				status = err.Error()
				return
			}
			if !p2tui.JBool(p2tui.JField(result, "repo")) {
				status = p2tui.JStr(p2tui.JField(result, "reason"))
				if status == "" {
					status = "Not a git repository"
				}
				return
			}
			branch = p2tui.JStr(p2tui.JField(result, "branch"))
			if branch == "" {
				head := []rune(p2tui.JStr(p2tui.JField(result, "head")))
				branch = string(head[:min(7, len(head))])
			}
			for _, change := range p2tui.JList(p2tui.JField(result, "changes")) {
				path, from, state := p2tui.JStr(p2tui.JField(change, "path")), p2tui.JStr(p2tui.JField(change, "from")), p2tui.JStr(p2tui.JField(change, "status"))
				row := components.ListRow{Key: path, Label: path, Detail: state}
				if from != "" {
					row.Label = from + " → " + path
				}
				if strings.Contains(state, "U") {
					row.Color = "error"
				}
				rows = append(rows, row)
			}
			switch {
			case p2tui.JBool(p2tui.JField(result, "truncated")):
				status = "List truncated"
			case len(rows) == 0:
				status = "No changes"
			default:
				status = ""
			}
		})
		return components.ListOverlayOptions{
			Title: func() string {
				on := ""
				if branch != "" {
					on = " on " + branch
				}
				return fmt.Sprintf("Changes%s (%d)", on, len(rows))
			},
			Rows:     func() []components.ListRow { return rows },
			OnSelect: func(row components.ListRow) { showDiff(ctx, ref, row.Key) },
			Footer:   footerStatus(ctx, &status),
			OnClose:  closeAll,
		}
	})
}

// openGrep is `/grep <text>`: literal matches below the session directory; Enter opens the file at the match.
func openGrep(ctx runtime.TuiContext, text string) error {
	if strings.TrimSpace(text) == "" {
		ctx.Notify("Usage: /grep <text>", runtime.NoticeInfo)
		return nil
	}
	return panel(ctx, "grep", func(ref *host.Reference, closeAll func()) components.ListOverlayOptions {
		type match struct {
			path string
			line int
		}
		var rows []components.ListRow
		var matches []match
		status := "Searching…"
		ask(ctx, ref, delta.JsonObjectOf("kind", "search", "text", text, "path", ""), "search", func(result any, err error) {
			if err != nil {
				status = err.Error()
				return
			}
			for index, each := range p2tui.JList(p2tui.JField(result, "matches")) {
				path, line := p2tui.JStr(p2tui.JField(each, "path")), int(p2tui.JNum(p2tui.JField(each, "line")))
				rows = append(rows, components.ListRow{Key: strconv.Itoa(index), Label: strings.TrimSpace(p2tui.JStr(p2tui.JField(each, "text"))), Detail: fmt.Sprintf("%s:%d", path, line)})
				matches = append(matches, match{path, line})
			}
			switch {
			case p2tui.JBool(p2tui.JField(result, "truncated")):
				status = "Results truncated"
			case len(rows) == 0:
				status = "No matches"
			default:
				status = ""
			}
		})
		return components.ListOverlayOptions{
			Title: func() string { return fmt.Sprintf("Search %q (%d)", render.LineText(text), len(rows)) },
			Rows:  func() []components.ListRow { return rows },
			OnSelect: func(row components.ListRow) {
				if index, err := strconv.Atoi(row.Key); err == nil && index < len(matches) {
					showFile(ctx, ref, matches[index].path, matches[index].line)
				}
			},
			Footer:  footerStatus(ctx, &status),
			OnClose: closeAll,
		}
	})
}

// Commands are the workspace's commands.
var Commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.files", runtime.TuiCommand{
		Title: "Browse files", Category: "Workspace", When: "chat && participant && !overlay", Slash: &runtime.TuiCommandSlash{Name: "files", ArgumentHint: "[directory]"},
		Run: func(ctx runtime.TuiContext, args string) error { return openFiles(ctx, args) },
	}),
	p2runtime.Entry("p2.changes", runtime.TuiCommand{
		Title: "Working tree changes", Category: "Workspace", When: "chat && participant && !overlay", Slash: &runtime.TuiCommandSlash{Name: "changes"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openChanges(ctx) },
	}),
	p2runtime.Entry("p2.grep", runtime.TuiCommand{
		Title: "Search files", Category: "Workspace", When: "chat && participant && !overlay", Slash: &runtime.TuiCommandSlash{Name: "grep", ArgumentHint: "<text>"},
		Run: func(ctx runtime.TuiContext, args string) error { return openGrep(ctx, args) },
	}),
}

// BranchSlot is the git branch's footer slot.
const BranchSlot = "footer:branch"

// GitBranch is the viewed conversation's git branch for the footer (pi's `cwd (branch)`), from `Workspace.query({kind: "status"})`, asked again when files
// change (debounced). "detached" for a detached HEAD, as pi. Only for those who may hold `Workspace` (the owner, full-access guests): view-only guests are
// refused, so they never ask. Its fields are the owner loop's.
type GitBranch struct {
	runtime *runtime.TuiRuntime
	// Branch is the branch shown, "" for none.
	Branch string
	key    string
	hasKey bool
	ref    *host.Reference
	stop   func()
	timer  *time.Timer
	asking bool
	again  bool
	remove func()
}

// NewGitBranch is the branch surface of rt.
func NewGitBranch(rt *runtime.TuiRuntime) *GitBranch {
	g := &GitBranch{runtime: rt}
	g.remove = rt.AddListener(g.sync)
	g.sync()
	return g
}

func (g *GitBranch) allowed() bool {
	rt := g.runtime
	return rt.Role == runtime.RoleOwner || (rt.Role == runtime.RoleGuest && rt.GuestMode() == "full")
}

func (g *GitBranch) release() {
	if g.stop != nil {
		g.stop()
		g.stop = nil
	}
	if g.ref != nil {
		g.ref.Release()
		g.ref = nil
	}
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}

func (g *GitBranch) sync() {
	rt := g.runtime
	allowed := rt.HasSession && g.allowed()
	key := ""
	if allowed {
		key = runtime.IdentityKey(rt.Session, rt.Conversation)
	}
	if g.hasKey && key == g.key {
		return
	}
	g.key, g.hasKey = key, true
	g.release()
	g.Branch = ""
	if !allowed {
		return
	}
	ref, err := rt.Host.Hold(context.Background(), contract.Workspace, delta.JsonObjectOf("session", rt.Session, "conversation", float64(rt.Conversation)), host.GetOptions{})
	if err != nil {
		return
	}
	g.ref = ref
	g.stop = p2tui.FollowMember(ref.Member("changes"), func(delta.JsonValue) {
		rt.Post(func() {
			if g.key != key || g.ref != ref {
				return
			}
			// The first answer at once; file changes coalesce.
			if g.timer != nil {
				g.timer.Stop()
			}
			delay := 500 * time.Millisecond
			if g.Branch == "" {
				delay = 0
			}
			g.timer = time.AfterFunc(delay, func() { rt.Post(func() { g.ask(ref, key) }) })
		})
	})
}

func (g *GitBranch) ask(ref *host.Reference, key string) {
	if variant, _, _ := ref.Variant(); variant != "reader" || p2runtime.StatusOf(ref) != host.RefStateLive || key != g.key {
		return
	}
	if g.asking {
		g.again = true
		return
	}
	g.asking = true
	go func() {
		result, err := ref.Call("query", delta.JsonObjectOf("kind", "status"), context.Background())
		g.runtime.Post(func() {
			g.asking = false
			if err == nil && key == g.key && p2tui.JStr(p2tui.JField(result, "kind")) == "status" {
				branch := ""
				if p2tui.JBool(p2tui.JField(result, "repo")) {
					if branch = p2tui.JStr(p2tui.JField(result, "branch")); branch == "" && p2tui.JStr(p2tui.JField(result, "head")) != "" {
						branch = "detached"
					}
				}
				if branch != g.Branch {
					g.Branch = branch
					g.runtime.Changed()
				}
			}
			if g.again {
				g.again = false
				g.ask(ref, key)
			}
		})
	}()
}

// Render is the branch, or nothing.
func (g *GitBranch) Render(int) []string {
	if g.Branch == "" {
		return nil
	}
	return []string{render.LineText(g.Branch)}
}

// Invalidate is a no-op.
func (g *GitBranch) Invalidate() {}

// Dispose releases the reference.
func (g *GitBranch) Dispose() {
	g.remove()
	g.release()
}

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet(BranchSlot, func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewGitBranch(rt) }, Commands, nil)
