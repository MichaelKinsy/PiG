package views

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/views/tool.ts: the agent's `view` tool: lists, opens, reads, changes and closes the session's
// views, saves them to (and loads them from) the owner's library, and explains a custom view's page API on request.

// Backend is what the tool reaches outside the session's documents: the session's files and the owner's library.
type Backend interface {
	// SaveSource saves html as the next version of view id's source; its version.
	SaveSource(ctx context.Context, id, html string) (int, error)
	// ReadSource is version version of view id's source.
	ReadSource(ctx context.Context, id string, version int) (string, error)
	// ReadArtifact is the newest version of HTML artifact name.
	ReadArtifact(ctx context.Context, name string) (string, error)
	ListSaved(ctx context.Context) ([]SavedInfo, error)
	GetSaved(ctx context.Context, name string) (*SavedView, error)
	Save(ctx context.Context, view SavedView) (SavedInfo, error)
}

// maxReadState is how much of a state's JSON a read shows (and says how much there is).
const maxReadState = 40_000

const extensionName = "p2-views"

func toolParameters() schema.Schema {
	return schema.Obj(schema.Properties{
		schema.Prop("action", schema.OneOf([]any{"list", "open", "read", "update", "close", "resolve", "save", "saved", "load", "help", "arrange"})),
		schema.Opt("id", schema.Str(schema.MaxLength(64))),
		schema.Opt("kind", contract.ViewKindSchema),
		schema.Opt("title", schema.Str(schema.MinLength(1), schema.MaxLength(120))),
		schema.Opt("path", schema.Str(schema.MaxLength(4096))),
		schema.Opt("html", schema.Str()),
		schema.Opt("artifact", schema.Str(schema.MaxLength(64))),
		schema.Opt("state", schema.Unknown()),
		schema.Opt("merge", schema.Unknown()),
		schema.Opt("layout", schema.Unknown()),
		schema.Opt("annotations", schema.Arr(schema.Str(), schema.MaxItems(200))),
		schema.Opt("name", schema.Str(schema.MaxLength(64))),
		schema.Opt("description", schema.Str(schema.MaxLength(2000))),
	}, schema.Open())
}

// CreateExtension is the extension with the `view` tool, reaching what it needs through backend.
func CreateExtension(backend Backend) (*p2runtime.P2Extension, error) {
	tool, err := p2runtime.ToolSchemaOf("view", toolDescription, toolParameters())
	if err != nil {
		return nil, err
	}
	registration := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: tool,
		Replay:     durable.ReplayUnsafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			result, err := run(ctx, callOf(fields), api, backend)
			if err != nil {
				action, _ := fields["action"].(string)
				return reply(err.Error(), details(action, nil), true), nil
			}
			return result, nil
		},
	})
	return &p2runtime.P2Extension{
		Extension: &durable.Extension{Name: extensionName},
		Tools:     []p2runtime.P2Tool{{ToolRegistration: registration}},
	}, nil
}

// call is one call's arguments.
type call struct {
	action, id, kind, title, path, html, artifact, name, description string
	has                                                              map[string]bool
	state, merge, layout                                             any
	annotations                                                      []string
}

func callOf(fields map[string]any) call {
	text := func(key string) string { value, _ := fields[key].(string); return value }
	c := call{
		action: text("action"), id: text("id"), kind: text("kind"), title: text("title"), path: text("path"), html: text("html"),
		artifact: text("artifact"), name: text("name"), description: text("description"),
		state: fields["state"], merge: fields["merge"], layout: fields["layout"], has: map[string]bool{},
	}
	for key := range fields {
		c.has[key] = true
	}
	list, _ := fields["annotations"].([]any)
	for _, each := range list {
		if id, ok := each.(string); ok {
			c.annotations = append(c.annotations, id)
		}
	}
	return c
}

func details(action string, view *View) *delta.JsonObject {
	out := delta.JsonObjectOf("action", action)
	if view != nil {
		out.Set("id", view.ID)
		out.Set("title", view.Title)
		out.Set("kind", view.Kind)
	}
	return out
}

func reply(text string, details *delta.JsonObject, isError bool) durable.ToolExecutionResult {
	result := durable.ToolExecutionResult{
		Output:     []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		Details:    details,
		HasDetails: true,
	}
	if isError {
		result.IsError = &isError
	}
	return result
}

func jsonString(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		return "null"
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func required(c call, key string) error {
	if !c.has[key] {
		return fmt.Errorf("%s is required for this action", key)
	}
	return nil
}

// run runs one call; an error is the call's failure.
func run(ctx context.Context, c call, api durable.ToolExecutionApi, backend Backend) (durable.ToolExecutionResult, error) {
	switch c.action {
	case "list":
		text, err := listText(ctx, api)
		return reply(text, details("list", nil), false), err
	case "help":
		return reply(pageAPI, details("help", nil), false), nil
	case "arrange":
		if err := required(c, "layout"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		state, err := ReadViews(ctx, api)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		known := map[string]bool{contract.Transcript: true}
		for _, view := range state.Views {
			known[view.ID] = true
		}
		layout, err := durable.ToJsonValue(c.layout)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if problem := ArrangementError(layout, known); problem != "" {
			return durable.ToolExecutionResult{}, errors.New("layout: " + problem)
		}
		_, err = api.Commit(ctx, func(tx durable.Tx) (any, error) {
			draft, err := durable.TxDoc[ViewsState](tx, ViewsDoc)
			if err != nil {
				return nil, err
			}
			return nil, draft.Set("arrangement", delta.JsonObjectOf("layout", layout, "stamp", api.Now()))
		})
		return reply("Arranged the person's panes; they may rearrange them.", details("arrange", nil), false), err
	case "open":
		return open(ctx, c, api, backend)
	case "read":
		if err := required(c, "id"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return read(ctx, c.id, api)
	case "update":
		return update(ctx, c, api, backend)
	case "close":
		if err := required(c, "id"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		view, err := viewOf(ctx, api, c.id)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if _, err := api.Commit(ctx, func(tx durable.Tx) (any, error) { return RemoveView(tx, c.id) }); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return reply(fmt.Sprintf("Closed view %q.", c.id), details("close", &view), false), nil
	case "resolve":
		return resolve(ctx, c, api)
	case "save":
		if err := ownerOnly(ctx, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		return save(ctx, c, api, backend)
	case "saved":
		if err := ownerOnly(ctx, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		saved, err := backend.ListSaved(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		lines := make([]string, len(saved))
		for i, each := range saved {
			lines[i] = fmt.Sprintf("- %s (%s): %s", each.Name, each.Kind, each.Title)
			if each.Description != "" {
				lines[i] += " — " + each.Description
			}
		}
		text := "No saved views."
		if len(saved) > 0 {
			text = "Saved views:\n" + strings.Join(lines, "\n")
		}
		return reply(text, details("saved", nil), false), nil
	case "load":
		if err := ownerOnly(ctx, api); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if err := required(c, "name"); err != nil {
			return durable.ToolExecutionResult{}, err
		}
		saved, err := backend.GetSaved(ctx, c.name)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if saved == nil {
			return durable.ToolExecutionResult{}, fmt.Errorf("No saved view %q", c.name)
		}
		state, err := ReadViews(ctx, api)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		opened := call{action: "open", kind: saved.Kind, title: saved.Title, state: saved.State, path: saved.Path, id: c.id,
			has: map[string]bool{"kind": true, "title": true, "state": true}}
		if saved.HTML != nil && *saved.HTML != "" {
			opened.html = *saved.HTML
			opened.has["html"] = true
		}
		if saved.Path != "" {
			opened.has["path"] = true
		}
		if c.id == "" {
			opened.id = FreeViewID(state, saved.Name, saved.Kind)
		}
		opened.has["id"] = true
		return open(ctx, opened, api, backend)
	}
	return durable.ToolExecutionResult{}, fmt.Errorf("Unknown action %q", c.action)
}

// listText is every view with its selection, and the annotations nobody resolved.
func listText(ctx context.Context, api durable.ToolExecutionApi) (string, error) {
	state, err := ReadViews(ctx, api)
	if err != nil {
		return "", err
	}
	views := make([]string, len(state.Views))
	for i, view := range state.Views {
		notes := 0
		for _, annotation := range state.Annotations {
			if annotation.View == view.ID {
				notes++
			}
		}
		line := "- " + ViewName(state.Views, view.ID)
		if view.Selection != nil {
			line += "; selected: " + view.Selection.Summary
		}
		if notes > 0 {
			line += "; " + plural(notes, "annotation")
		}
		if len(view.Errors) > 0 {
			line += fmt.Sprintf("; %d script error(s), read it", len(view.Errors))
		}
		views[i] = line
	}
	notes := make([]string, len(state.Annotations))
	for i, annotation := range state.Annotations {
		notes[i] = "- " + DescribeAnnotation(state.Views, annotation)
	}
	parts := []string{"No views are open."}
	if len(views) > 0 {
		parts[0] = "Views:\n" + strings.Join(views, "\n")
	}
	if state.Arrangement != nil {
		parts = append(parts, "Layout you arranged last: "+jsonString(state.Arrangement.Layout))
	}
	if len(notes) > 0 {
		parts = append(parts, "Annotations:\n"+strings.Join(notes, "\n"))
	}
	return strings.Join(parts, "\n"), nil
}

func checkedHTML(html string) (string, error) {
	if len(html) > contract.MaxViewHTML {
		return "", errors.New("html exceeds 2 MiB")
	}
	return html, nil
}

func open(ctx context.Context, c call, api durable.ToolExecutionApi, backend Backend) (durable.ToolExecutionResult, error) {
	for _, key := range []string{"kind", "title"} {
		if err := required(c, key); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	state, err := ReadViews(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if c.has["id"] && (!contract.ViewID.MatchString(c.id) || c.id == contract.Transcript) {
		return durable.ToolExecutionResult{}, errors.New("id: a lowercase slug (a-z, 0-9, - and _), not transcript")
	}
	id := c.id
	if !c.has["id"] {
		id = FreeViewID(state, c.title, c.kind)
	}
	var existing *View
	for i := range state.Views {
		if state.Views[i].ID == id {
			existing = &state.Views[i]
		}
	}
	if existing != nil && existing.Kind != c.kind {
		return durable.ToolExecutionResult{}, fmt.Errorf("View %q is a %s view, not %s", id, existing.Kind, c.kind)
	}
	if c.kind == "markdown" && c.path == "" && (existing == nil || existing.Path == "") {
		return durable.ToolExecutionResult{}, errors.New("A markdown view needs path")
	}
	source := 0
	if c.kind == "custom" {
		html, hasHTML := c.html, c.has["html"]
		if !hasHTML && c.artifact != "" {
			if html, err = backend.ReadArtifact(ctx, c.artifact); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			hasHTML = true
		}
		if !hasHTML && (existing == nil || existing.Source == 0) {
			return durable.ToolExecutionResult{}, errors.New("A custom view needs html or artifact")
		}
		if hasHTML {
			if html, err = checkedHTML(html); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if source, err = backend.SaveSource(ctx, id, html); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
	}
	committed, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		return OpenView(tx, ViewSpec{ID: id, Kind: c.kind, Title: c.title, Path: c.path, Source: source, State: c.state, HasState: c.has["state"]}, api.Now())
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	view := committed.(View)
	verb := "Opened"
	if existing != nil {
		verb = "Updated and showed"
	}
	return reply(fmt.Sprintf("%s %s. The person sees it beside the transcript.", verb, ViewName([]View{view}, view.ID)), details("open", &view), false), nil
}

func read(ctx context.Context, id string, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	state, err := ReadViews(ctx, api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	var notes []string
	for _, annotation := range state.Annotations {
		if annotation.View == id {
			notes = append(notes, "- "+DescribeAnnotation(state.Views, annotation))
		}
	}
	if id == contract.Transcript {
		text := "No annotations on the transcript."
		if len(notes) > 0 {
			text = "Annotations on the transcript:\n" + strings.Join(notes, "\n")
		}
		return reply(text, delta.JsonObjectOf("action", "read", "id", id), false), nil
	}
	var view *View
	ids := make([]string, len(state.Views))
	for i := range state.Views {
		ids[i] = state.Views[i].ID
		if state.Views[i].ID == id {
			view = &state.Views[i]
		}
	}
	if view == nil {
		list := strings.Join(ids, ", ")
		if list == "" {
			list = "none"
		}
		return durable.ToolExecutionResult{}, fmt.Errorf("No view %q. Views: %s", id, list)
	}
	value, err := ReadViewState(ctx, api, id)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	encoded := jsonString(value)
	head := ViewName(state.Views, id)
	if view.Source != 0 {
		head += fmt.Sprintf(", source version %d", view.Source)
	}
	lines := []string{head + "."}
	if view.Selection != nil {
		selected := "Selected: " + view.Selection.Summary
		if view.Selection.Data != nil {
			selected += "\nSelection data: " + jsonString(view.Selection.Data)
		}
		lines = append(lines, selected)
	} else {
		lines = append(lines, "Nothing selected.")
	}
	if units := len(utf16Units(encoded)); units > maxReadState {
		lines = append(lines, fmt.Sprintf("State (%d characters of JSON, the first %d): %s", units, maxReadState, string(unitsPrefix(encoded, maxReadState))))
	} else {
		lines = append(lines, "State: "+encoded)
	}
	if len(view.Errors) > 0 {
		errs := make([]string, len(view.Errors))
		for i, each := range view.Errors {
			errs[i] = "- " + each
		}
		lines = append(lines, "Script errors its page logged:\n"+strings.Join(errs, "\n"))
	}
	if len(notes) > 0 {
		lines = append(lines, "Annotations:\n"+strings.Join(notes, "\n"))
	}
	return reply(strings.Join(lines, "\n"), details("read", view), false), nil
}

func isObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func update(ctx context.Context, c call, api durable.ToolExecutionApi, backend Backend) (durable.ToolExecutionResult, error) {
	if err := required(c, "id"); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	current, err := viewOf(ctx, api, c.id)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	hasState, hasMerge := c.has["state"], c.has["merge"]
	if hasState && hasMerge {
		return durable.ToolExecutionResult{}, errors.New("Give state or merge, not both")
	}
	merge, isMap := isObject(c.merge)
	if hasMerge && !isMap {
		return durable.ToolExecutionResult{}, errors.New("merge: an object")
	}
	source := 0
	if c.has["html"] {
		if current.Kind != "custom" {
			return durable.ToolExecutionResult{}, errors.New("Only custom views have html")
		}
		html, err := checkedHTML(c.html)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if source, err = backend.SaveSource(ctx, c.id, html); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	var base any
	if hasMerge {
		if base, err = ReadViewState(ctx, api, c.id); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}
	_, err = api.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, state, err := draftOf(tx)
		if err != nil {
			return nil, err
		}
		at := -1
		for i, each := range state.Views {
			if each.ID == c.id {
				at = i
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("No view %q", c.id)
		}
		target := draft.Array("views").Object(at)
		if c.has["title"] {
			if err := target.Set("title", c.title); err != nil {
				return nil, err
			}
		}
		if c.has["path"] {
			if err := target.Set("path", c.path); err != nil {
				return nil, err
			}
		}
		if source != 0 {
			if err := target.Set("source", float64(source)); err != nil {
				return nil, err
			}
			// The new page logs its own errors.
			target.Delete("errors")
		}
		if hasState || hasMerge {
			next := c.state
			if hasMerge {
				combined := delta.NewJsonObject(0)
				switch existing := base.(type) {
				case *delta.JsonObject:
					for key, each := range existing.All() {
						combined.Set(key, each)
					}
				case map[string]any:
					for _, key := range slices.Sorted(maps.Keys(existing)) {
						combined.Set(key, existing[key])
					}
				}
				for _, key := range slices.Sorted(maps.Keys(merge)) {
					combined.Set(key, merge[key])
				}
				next = combined
			}
			checked, err := CheckedState(next)
			if err != nil {
				return nil, err
			}
			value, err := jsonOf(checked)
			if err != nil {
				return nil, err
			}
			doc, err := durable.TxDoc[ViewValue](tx, ViewStateDoc, c.id, nil)
			if err != nil {
				return nil, err
			}
			return nil, doc.Set("value", value)
		}
		return nil, nil
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	view, err := viewOf(ctx, api, c.id)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	var changed []string
	if c.has["title"] {
		changed = append(changed, "title")
	}
	if c.has["path"] {
		changed = append(changed, "path")
	}
	if source != 0 {
		changed = append(changed, fmt.Sprintf("html (version %d)", source))
	}
	if hasState {
		changed = append(changed, "state")
	}
	if hasMerge {
		changed = append(changed, "state (merged)")
	}
	text := strings.Join(changed, ", ")
	if text == "" {
		text = "nothing"
	}
	return reply(fmt.Sprintf("Updated %s: %s.", ViewName([]View{view}, c.id), text), details("update", &view), false), nil
}

func resolve(ctx context.Context, c call, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	ids := map[string]bool{}
	for _, id := range c.annotations {
		ids[id] = true
	}
	if len(ids) == 0 && !c.has["id"] {
		return durable.ToolExecutionResult{}, errors.New("Give annotations (ids) or id (all of a view's)")
	}
	removed, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, state, err := draftOf(tx)
		if err != nil {
			return nil, err
		}
		count := 0
		for i := len(state.Annotations) - 1; i >= 0; i-- {
			annotation := state.Annotations[i]
			if ids[annotation.ID] || (c.has["id"] && annotation.View == c.id) {
				if _, err := draft.Array("annotations").Splice(i, 1); err != nil {
					return nil, err
				}
				count++
			}
		}
		return count, nil
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return reply(fmt.Sprintf("Resolved %s.", plural(removed.(int), "annotation")), details("resolve", nil), false), nil
}

func save(ctx context.Context, c call, api durable.ToolExecutionApi, backend Backend) (durable.ToolExecutionResult, error) {
	if err := required(c, "id"); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	view, err := viewOf(ctx, api, c.id)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	name := view.ID
	if c.has["name"] {
		name = c.name
	}
	if !contract.ViewID.MatchString(name) {
		return durable.ToolExecutionResult{}, errors.New("name: a lowercase slug (a-z, 0-9, - and _)")
	}
	var html *string
	if view.Kind == "custom" && view.Source != 0 {
		text, err := backend.ReadSource(ctx, view.ID, view.Source)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		html = &text
	}
	state, err := ReadViewState(ctx, api, view.ID)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	info, err := backend.Save(ctx, SavedView{Name: name, Title: view.Title, Kind: view.Kind, State: state, Saved: api.Now(), Description: c.description, Path: view.Path, HTML: html})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return reply(fmt.Sprintf("Saved %s as %q in the owner's library; load it in any session with load.", ViewName([]View{view}, view.ID), info.Name), details("save", &view), false), nil
}

// viewOf is view id as committed; a missing one fails the call.
func viewOf(ctx context.Context, api durable.ToolExecutionApi, id string) (View, error) {
	state, err := ReadViews(ctx, api)
	if err != nil {
		return View{}, err
	}
	for _, view := range state.Views {
		if view.ID == id {
			return view, nil
		}
	}
	return View{}, fmt.Errorf("No view %q", id)
}

func ownerOnly(ctx context.Context, api durable.ToolExecutionApi) error {
	origin, err := p2runtime.OriginOf(ctx, api)
	if err != nil {
		return err
	}
	if origin != p2runtime.OriginOwner {
		return errors.New("The owner's library needs a turn the owner started; guests and what they start cannot use it.")
	}
	return nil
}
