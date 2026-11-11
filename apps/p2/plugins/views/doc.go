// Package views is p2's views plugin (apps/p2/plugins/views): panes beside the transcript that the person and the agent
// share. The session's worker owns the views and annotations (Durable documents), the agent's `view` tool changes them,
// the server keeps the owner's library of saved views and serves a custom view's page into its sandboxed frame.
package views

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"golang.org/x/text/unicode/norm"
)

// Ports apps/p2/plugins/views/doc.ts: the views' Durable documents (session scope, worker) and the changes and texts the
// `view` tool, the `Views` object and the send router share.

// Selection is what the person selected in a view.
type Selection struct {
	Summary string `json:"summary"`
	Data    any    `json:"data,omitempty"`
	Sent    bool   `json:"sent,omitempty"`
}

// View is one view of a session (contract.ViewSchema).
type View struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Path      string     `json:"path,omitempty"`
	Source    int        `json:"source,omitempty"`
	Selection *Selection `json:"selection,omitempty"`
	Errors    []string   `json:"errors,omitempty"`
	Focus     float64    `json:"focus"`
	Created   float64    `json:"created"`
}

// Annotation is an annotation as the session keeps it (contract.AnnotationSchema).
type Annotation struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	View    string      `json:"view"`
	Quote   string      `json:"quote,omitempty"`
	Note    string      `json:"note,omitempty"`
	Strokes [][]float64 `json:"strokes,omitempty"`
	Aspect  float64     `json:"aspect,omitempty"`
	Under   string      `json:"under,omitempty"`
	Author  string      `json:"author"`
	Created float64     `json:"created"`
	Sent    bool        `json:"sent,omitempty"`
}

// Arrangement is how the agent last asked the person's panes to be laid out, and when.
type Arrangement struct {
	Layout any     `json:"layout"`
	Stamp  float64 `json:"stamp"`
}

// ViewsState is the session's views and annotations: the worker's `p2.views` document.
type ViewsState struct {
	Views       []View       `json:"views"`
	Annotations []Annotation `json:"annotations"`
	Arrangement *Arrangement `json:"arrangement,omitempty"`
}

// ViewValue is one view's JSON state.
type ViewValue struct {
	Value any `json:"value"`
}

// ViewsDoc is the session's views and annotations.
var ViewsDoc = durable.DefineDoc(durable.DocDefinition[ViewsState]{
	CommonDocDefinition: durable.CommonDocDefinition[ViewsState]{
		Kind:    "p2.views",
		Version: 1,
		Initial: func() ViewsState { return ViewsState{Views: []View{}, Annotations: []Annotation{}} },
		CheckpointWhen: func(_ ViewsState, _ []durable.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 32
		},
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

// ViewStateDoc is each view's JSON state, keyed by the view's ID; retired with the view. A state change replaces the whole
// value: a delta is as large as a base.
var ViewStateDoc = durable.DefineDocFamily(durable.DocFamilyDefinition[ViewValue, any]{
	Kind:              "p2.view-state",
	Version:           1,
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
	Family:            true,
	Initial:           func(any) ViewValue { return ViewValue{} },
	CheckpointWhen: func(_ ViewValue, _ []durable.Op, info durable.CheckpointInfo) bool {
		return info.DeltasSinceBase >= 8
	},
})

// ReadViews is the session's views and annotations as committed (empty before the first view).
func ReadViews(ctx context.Context, reader durable.DocumentReader) (ViewsState, error) {
	state, err := durable.Snapshot(ctx, reader, ViewsDoc)
	if err != nil || state == nil {
		return ViewsState{Views: []View{}, Annotations: []Annotation{}}, err
	}
	return *state, nil
}

// ReadViewState is a view's state as committed (nil when nothing set it).
func ReadViewState(ctx context.Context, reader durable.DocumentReader, id string) (any, error) {
	state, err := durable.Snapshot(ctx, reader, ViewStateDoc, id, nil)
	if err != nil || state == nil {
		return nil, err
	}
	return state.Value, nil
}

var (
	slugAway = regexp.MustCompile(`[^a-z0-9]+`)
	slugEnds = regexp.MustCompile(`^-+|-+$`)
)

// FreeViewID is a free view ID made from title (or kind): its slug, numbered when taken.
func FreeViewID(state ViewsState, title, kind string) string {
	slug := strings.ToLower(norm.NFKD.String(title))
	slug = slugEnds.ReplaceAllString(slugAway.ReplaceAllString(slug, "-"), "")
	if units := utf16.Encode([]rune(slug)); len(units) > 48 {
		slug = string(utf16.Decode(units[:48]))
	}
	if slug == "" {
		slug = kind
	}
	base := slug
	if !contract.ViewID.MatchString(slug) || slug == contract.Transcript {
		base = "view-" + slug
		if units := utf16.Encode([]rune(base)); len(units) > 48 {
			base = string(utf16.Decode(units[:48]))
		}
	}
	taken := map[string]bool{}
	for _, view := range state.Views {
		taken[view.ID] = true
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		if candidate := base + "-" + strconv.Itoa(n); !taken[candidate] {
			return candidate
		}
	}
}

// CheckedState is a state value as JSON, refused past MaxViewState characters.
func CheckedState(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if size := len(utf16.Encode([]rune(string(encoded)))); size > contract.MaxViewState {
		return nil, fmt.Errorf("View state is %d characters of JSON; at most %d", size, contract.MaxViewState)
	}
	return value, nil
}

// ViewSpec is what opening a view sets: a new view, or the one with its ID updated and brought to the front.
type ViewSpec struct {
	ID, Kind, Title, Path string
	Source                int
	State                 any
	HasState              bool
}

func jsonOf(value any) (delta.JsonValue, error) { return durable.ToJsonValue(value) }

func draftOf(tx durable.Tx) (*delta.Object, ViewsState, error) {
	draft, err := durable.TxDoc[ViewsState](tx, ViewsDoc)
	if err != nil {
		return nil, ViewsState{}, err
	}
	state, err := durable.FromJsonValue[ViewsState](draft.Snapshot())
	return draft, state, err
}

// OpenView opens a view: adds it, or updates the one with its ID (same kind; its selection, errors and unset fields stay)
// and brings it to the front. spec.State replaces its state.
func OpenView(tx durable.Tx, spec ViewSpec, now float64) (View, error) {
	draft, state, err := draftOf(tx)
	if err != nil {
		return View{}, err
	}
	at := -1
	for i, each := range state.Views {
		if each.ID == spec.ID {
			at = i
		}
	}
	view := View{Created: now}
	if at >= 0 {
		view = state.Views[at]
		if view.Kind != spec.Kind {
			return View{}, fmt.Errorf("View %q is a %s view", spec.ID, view.Kind)
		}
	}
	view.ID, view.Kind, view.Title, view.Focus = spec.ID, spec.Kind, spec.Title, now
	if spec.Path != "" {
		view.Path = spec.Path
	}
	if spec.Source != 0 {
		view.Source = spec.Source
		view.Errors = nil
	}
	encoded, err := jsonOf(view)
	if err != nil {
		return View{}, err
	}
	if at < 0 {
		_, err = draft.Array("views").Push(encoded)
	} else {
		err = draft.Array("views").Set(at, encoded)
	}
	if err != nil {
		return View{}, err
	}
	if spec.HasState {
		value, err := CheckedState(spec.State)
		if err != nil {
			return View{}, err
		}
		doc, err := durable.TxDoc[ViewValue](tx, ViewStateDoc, spec.ID, nil)
		if err != nil {
			return View{}, err
		}
		if err := doc.Set("value", value); err != nil {
			return View{}, err
		}
	}
	return view, nil
}

// RemoveView removes view id with its state and annotations; false when there is none.
func RemoveView(tx durable.Tx, id string) (bool, error) {
	draft, state, err := draftOf(tx)
	if err != nil {
		return false, err
	}
	at := -1
	for i, each := range state.Views {
		if each.ID == id {
			at = i
		}
	}
	if at < 0 {
		return false, nil
	}
	if _, err := draft.Array("views").Splice(at, 1); err != nil {
		return false, err
	}
	for i := len(state.Annotations) - 1; i >= 0; i-- {
		if state.Annotations[i].View == id {
			if _, err := draft.Array("annotations").Splice(i, 1); err != nil {
				return false, err
			}
		}
	}
	return true, durable.TxRetireDoc(tx, ViewStateDoc, id, nil)
}

// AddAnnotation adds an annotation by author on a view of the session or the transcript; returns its ID.
func AddAnnotation(tx durable.Tx, input *delta.JsonObject, author string, now float64) (string, error) {
	draft, state, err := draftOf(tx)
	if err != nil {
		return "", err
	}
	view, _ := input.Value("view").(string)
	known := view == contract.Transcript
	for _, each := range state.Views {
		known = known || each.ID == view
	}
	if !known {
		return "", fmt.Errorf("No view %q", view)
	}
	id := "a" + strconv.FormatInt(int64(now), 36) + strconv.FormatInt(int64(rand.IntN(1296)), 36)
	annotation := input.Clone()
	annotation.Set("id", id)
	annotation.Set("author", author)
	annotation.Set("created", now)
	if _, err := draft.Array("annotations").Push(annotation); err != nil {
		return "", err
	}
	if over := len(state.Annotations) + 1 - contract.MaxAnnotations; over > 0 {
		if _, err := draft.Array("annotations").Splice(0, over); err != nil {
			return "", err
		}
	}
	return id, nil
}

// ViewName is where a view is, as the agent reads it: `view "notes" (markdown docs/notes.md, "Notes")`.
func ViewName(views []View, id string) string {
	if id == contract.Transcript {
		return "the transcript"
	}
	for _, view := range views {
		if view.ID == id {
			where := ""
			if view.Path != "" {
				where = " " + view.Path
			}
			return fmt.Sprintf("view %q (%s%s, %q)", id, view.Kind, where, view.Title)
		}
	}
	return fmt.Sprintf("view %q (closed)", id)
}

var whitespace = regexp.MustCompile(`\s+`)

// Quoted is text quoted on one line, shortened to max UTF-16 units.
func Quoted(text string, max int) string {
	line := strings.TrimSpace(whitespace.ReplaceAllString(text, " "))
	if units := utf16.Encode([]rune(line)); len(units) > max {
		line = string(utf16.Decode(units[:max])) + "…"
	}
	return "«" + line + "»"
}

// DescribeAnnotation is one annotation as the agent reads it.
func DescribeAnnotation(views []View, annotation Annotation) string {
	where := ViewName(views, annotation.View)
	sent := ""
	if annotation.Sent {
		sent = ", sent before"
	}
	by := fmt.Sprintf(" (%s%s, id %s)", annotation.Author, sent, annotation.ID)
	switch annotation.Kind {
	case "comment":
		return fmt.Sprintf("Comment in %s on %s: %s%s", where, Quoted(annotation.Quote, 600), annotation.Note, by)
	case "delete":
		return fmt.Sprintf("Remove from %s: %s%s", where, Quoted(annotation.Quote, 600), by)
	}
	under := ""
	if annotation.Under != "" {
		under = " over " + Quoted(annotation.Under, 600)
	}
	note := ""
	if annotation.Note != "" {
		note = ": " + annotation.Note
	}
	return fmt.Sprintf("Drawing in %s%s%s%s%s", where, under, inkBox(annotation.Strokes), note, by)
}

// inkBox is where a drawing lies in its pane, in percent of the pane's width and height.
func inkBox(strokes [][]float64) string {
	left, top, right, bottom := 1.0, 1.0, 0.0, 0.0
	for _, stroke := range strokes {
		for i := 0; i+1 < len(stroke); i += 2 {
			left, right = min(left, stroke[i]), max(right, stroke[i])
			top, bottom = min(top, stroke[i+1]), max(bottom, stroke[i+1])
		}
	}
	if right < left {
		return ""
	}
	pct := func(value float64) int { return int(roundHalfUp(min(1, max(0, value)) * 100)) }
	return fmt.Sprintf(" (strokes at %d–%d%% across, %d–%d%% down the pane)", pct(left), pct(right), pct(top), pct(bottom))
}

// roundHalfUp is JavaScript's Math.round.
func roundHalfUp(value float64) float64 { return float64(int64(value + 0.5)) }

// Carried is what one message carries: its text, and which selections and annotations it describes (MarkSent).
type Carried struct {
	Text        string
	Annotations []string
	Selections  []CarriedSelection
}

// CarriedSelection is a selection a message carried.
type CarriedSelection struct{ View, Summary string }

// ViewContext is what a person's next message carries to the agent: the selections and annotations it has not carried yet,
// as one block after the message; nil when there is nothing new.
func ViewContext(state ViewsState) *Carried {
	var selections []View
	for _, view := range state.Views {
		if view.Selection != nil && !view.Selection.Sent {
			selections = append(selections, view)
		}
	}
	var annotations []Annotation
	for _, annotation := range state.Annotations {
		if !annotation.Sent {
			annotations = append(annotations, annotation)
		}
	}
	if len(selections) == 0 && len(annotations) == 0 {
		return nil
	}
	lines := []string{"<views>", "From the views beside the transcript (the view tool reads and changes them):"}
	carried := &Carried{}
	for _, view := range selections {
		lines = append(lines, fmt.Sprintf("Selected in %s: %s", ViewName(state.Views, view.ID), view.Selection.Summary))
		carried.Selections = append(carried.Selections, CarriedSelection{View: view.ID, Summary: view.Selection.Summary})
	}
	if len(annotations) > 0 {
		lines = append(lines, "Annotations the person made:")
		for i, annotation := range annotations {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, DescribeAnnotation(state.Views, annotation)))
			carried.Annotations = append(carried.Annotations, annotation.ID)
		}
	}
	carried.Text = strings.Join(append(lines, "</views>"), "\n")
	return carried
}

// MarkSent marks what a message carried as sent: those annotations, and those selections unless they changed meanwhile (a
// selection made while the message was on its way waits for the next one).
func MarkSent(tx durable.Tx, carried *Carried) error {
	draft, state, err := draftOf(tx)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, id := range carried.Annotations {
		ids[id] = true
	}
	for i, annotation := range state.Annotations {
		if ids[annotation.ID] {
			if err := draft.Array("annotations").Object(i).Set("sent", true); err != nil {
				return err
			}
		}
	}
	for _, picked := range carried.Selections {
		for i, view := range state.Views {
			if view.ID == picked.View && view.Selection != nil && view.Selection.Summary == picked.Summary {
				if err := draft.Array("views").Object(i).Object("selection").Set("sent", true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
