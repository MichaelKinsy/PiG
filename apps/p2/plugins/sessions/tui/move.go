package tui

// `/move` in the TUI: the viewed session moves to another place of its project (`projects.json`, the projects plugin). `/move <env>` names the machine; `/move`
// alone opens a picker of the project's places, probed as it opens. The session's project is the one it belongs to (`Projects.sessions`), never derived from its
// directory; the move is `/cd`'s (the selector's `relocate`: confirmed, then `Sessions.relocate`) and keeps the subfolder below the project's place.
//
// Ports apps/p2/plugins/sessions/tui-move.ts.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// MoveSource is what `/move` reads from the session selector: its sessions, the projects and their members.
type MoveSource interface {
	// Session is the catalog row of id, nil for none.
	Session(id string) *sessionscontract.Session
	Projects() []contract.Project
	LocalLabel() string
	// ProjectOf is the project name session id belongs to; ok is false for none.
	ProjectOf(id string) (name string, ok bool)
}

// Relocate is the selector's move of a session (`/cd`'s: confirmed when the machine changes, then `Sessions.relocate`). It runs on the owner loop and reports its
// own failures.
type Relocate func(session sessionscontract.Session, location p2runtime.LocationInput)

func envOf(session sessionscontract.Session) *string {
	if session.Env != nil && *session.Env != "" {
		return session.Env
	}
	return nil
}

func sameEnv2(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// RunMove is `/move [env]`: moves the viewed session to its project's place on env, or opens the picker without one.
func RunMove(ctx runtime.TuiContext, source MoveSource, argument string, relocate Relocate) {
	session := source.Session(ctx.Identity().Session)
	if session == nil {
		return
	}
	project, problem := storedProject(source, *session)
	switch {
	case problem != "":
		ctx.Notify(problem, runtime.NoticeError)
	case argument != "":
		go moveTo(ctx, source, relocate, *session, *project, argument)
	default:
		openPicker(ctx, source, relocate, *session, *project)
	}
}

// CompleteMove is `/move`'s completions: the places of the viewed session's project on other machines.
func CompleteMove(ctx runtime.TuiContext, source MoveSource, prefix string) []pitui.AutocompleteItem {
	session := source.Session(ctx.Identity().Session)
	if session == nil {
		return nil
	}
	project, problem := storedProject(source, *session)
	if problem != "" {
		return nil
	}
	var items []pitui.AutocompleteItem
	for _, place := range project.Places {
		if sameEnv2(place.Env, envOf(*session)) {
			continue
		}
		label := contract.PlaceLabel(place, source.LocalLabel())
		if strings.HasPrefix(label, prefix) {
			items = append(items, pitui.AutocompleteItem{Value: label, Label: label, Description: project.Name + ": " + place.Path})
		}
	}
	return items
}

// storedProject is the project session belongs to while projects.json still has it; else why `/move` cannot move it.
func storedProject(source MoveSource, session sessionscontract.Session) (*contract.Project, string) {
	name, ok := source.ProjectOf(session.ID)
	if !ok {
		return nil, "This session belongs to no project: start one from a project's row in /sessions"
	}
	for _, project := range source.Projects() {
		if project.Name == name {
			project := project
			return &project, ""
		}
	}
	return nil, "Project " + render.LineText(name) + " is no longer in projects.json"
}

// moveTo moves session to project's place on the machine name names (an environment's name, or this device as `local` or its label). Below the project's place on
// its current machine (read from the paths alone: that machine may be unreachable), it keeps its subdirectory when the target has it, else goes to the target
// place's root; only the target is resolved. It blocks (run it off the owner loop).
func moveTo(ctx runtime.TuiContext, source MoveSource, relocate Relocate, session sessionscontract.Session, project contract.Project, name string) {
	notify := func(message string, kind runtime.NoticeKind) { ctx.Post(func() { ctx.Notify(message, kind) }) }
	place, ok := contract.PlaceOn(project, name, source.LocalLabel())
	if !ok {
		notify("Project "+render.LineText(project.Name)+" has no place on "+render.LineText(name), runtime.NoticeError)
		return
	}
	label := render.LineText(contract.PlaceLabel(place, source.LocalLabel()))
	path, err := ResolvePlace(ctx, place)
	if err != nil {
		notify(label+" is unreachable: "+render.LineText(err.Error()), runtime.NoticeError)
		return
	}
	var current *contract.ProjectPlace
	for i := range project.Places {
		if sameEnv2(project.Places[i].Env, envOf(session)) {
			current = &project.Places[i]
		}
	}
	if current != nil {
		if subpath, ok := contract.Below(session.Cwd, current.Path); ok && subpath != "" {
			if resolved, err := ResolvePlace(ctx, contract.ProjectPlace{Env: place.Env, Path: p2runtime.JoinPath(path, subpath)}); err == nil {
				path = resolved
			} else {
				notify(render.LineText(subpath)+" does not exist on "+label+"; moving to the project's root", runtime.NoticeWarning)
			}
		}
	}
	if sameEnv2(place.Env, envOf(session)) && path == session.Cwd {
		notify("The session is already at "+render.LineText(LocationLabel(p2runtime.LocationInput{Env: place.Env, Path: path})), runtime.NoticeInfo)
		return
	}
	ctx.Post(func() { relocate(session, p2runtime.LocationInput{Env: place.Env, Path: path}) })
}

// openPicker shows every place of project in projects.json's order, the session's own one marked current, the others probed now (`…`, `✓`, `?`, `✗`, with why a
// place cannot be used). The cursor starts on the first other place that is reachable (it waits on one still checking); Enter on a reachable one moves there
// (`moveTo`), on the current one only closes, the others cannot be chosen.
func openPicker(ctx runtime.TuiContext, source MoveSource, relocate Relocate, session sessionscontract.Session, project contract.Project) {
	var here *contract.ProjectPlace
	var others []contract.ProjectPlace
	for i := range project.Places {
		if here == nil && sameEnv2(project.Places[i].Env, envOf(session)) {
			here = &project.Places[i]
		} else {
			others = append(others, project.Places[i])
		}
	}
	if len(others) == 0 {
		ctx.Notify("Project "+render.LineText(project.Name)+" has no place on another machine", runtime.NoticeError)
		return
	}
	label := func(place contract.ProjectPlace) string { return contract.PlaceLabel(place, source.LocalLabel()) }
	// The cursor follows the first other place not known to be unusable, until it is reachable or the reader moves it.
	following := label(others[0])
	hasFollowing := true
	var list *components.ListOverlay
	var probes *PlaceProbes
	settle := func() {
		if !hasFollowing {
			return
		}
		if selection := list.Selection(); selection == nil || *selection != following {
			hasFollowing = false
		}
		next := slices.IndexFunc(others, func(place contract.ProjectPlace) bool {
			kind := probes.State(place).Kind
			return kind == PlaceChecking || kind == PlaceReachable
		})
		if !hasFollowing || next < 0 {
			return
		}
		if probes.State(others[next]).Kind == PlaceReachable {
			hasFollowing = false
		} else {
			following = label(others[next])
		}
		list.Select(label(others[next]))
	}
	probes = NewPlaceProbes(ctx, func() {
		settle()
		ctx.RequestRender()
	})
	status := ""
	selected := label(others[0])
	var overlay runtime.TuiOverlayHandle
	list = components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title:    func() string { return "Move within " + render.LineText(project.Name) },
		Selected: &selected,
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for i := range project.Places {
				place := project.Places[i]
				if here != nil && &project.Places[i] == here {
					rows = append(rows, components.ListRow{Key: label(place), Label: "• " + label(place) + "  " + place.Path, Detail: "current"})
					continue
				}
				state := probes.State(place)
				row := components.ListRow{Key: label(place), Label: PlaceMark[state.Kind].Text + " " + label(place) + "  " + place.Path}
				if state.Kind == PlaceMissing || state.Kind == PlaceUnreachable {
					row.Detail = state.Reason
				}
				if state.Kind != PlaceReachable {
					row.Color = "muted"
				}
				rows = append(rows, row)
			}
			return rows
		},
		OnSelect: func(row components.ListRow) {
			index := slices.IndexFunc(project.Places, func(each contract.ProjectPlace) bool { return label(each) == row.Key })
			if index >= 0 && here != nil && &project.Places[index] == here {
				overlay.Close()
				ctx.Notify("Already here", runtime.NoticeInfo)
				return
			}
			if index < 0 || probes.State(project.Places[index]).Kind != PlaceReachable {
				status = fmt.Sprintf("%s cannot be chosen: only ✓ places are reachable", row.Key)
				return
			}
			overlay.Close()
			go moveTo(ctx, source, relocate, session, project, row.Key)
		},
		Footer: func() []string {
			if status == "" {
				return nil
			}
			return []string{ctx.Theme().Current().Fg("warning", "  "+render.LineText(status))}
		},
		OnClose: func() { overlay.Close() },
	})
	overlay = ctx.Overlay("move", list, pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	probes.Probe(others)
}
