package harness

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// TestAdvanceViewFollowsOnlyTheMountedIncarnation pins view.ts:196-210 for publications the harness itself never
// produces: a retirement or an update of an incarnation other than the mounted one. Pi removes the document only when
// the retired incarnation is the mounted one, applies ops only when incarnation and definition version both match,
// and otherwise sets the published value whole.
func TestAdvanceViewFollowsOnlyTheMountedIncarnation(t *testing.T) {
	const conversation = durable.ConversationId(7)
	const mountedID, otherID = durable.DocumentId(10), durable.DocumentId(11)
	kind := LiveDoc.AnyDefinition().Kind
	version := func(v int) *int { return &v }
	newMount := func() *viewMount {
		document := delta.JsonObjectOf("tools", []any{"a"})
		return &viewMount{
			value: ConversationView{Docs: ViewDocsOf(ViewDocs{}, kind, document)},
			docs:  map[string]docIncarnation{kind: {id: mountedID, version: 1}},
		}
	}
	change := func(id durable.DocumentId, v *int, value durable.JsonObject, ops ...durable.Op) durable.CommitPublication {
		owner := conversation
		return durable.CommitPublication{Seq: 2, Changes: []durable.CommitChange{durable.DocumentChange{
			Record:         durable.DocumentRecord{Id: id, Kind: kind},
			ConversationId: &owner,
			Version:        v,
			Value:          value,
			Ops:            ops,
		}}}
	}
	setTools := durable.Op{"s", []any{"tools"}, []any{"b"}}
	cases := []struct {
		name        string
		publication durable.CommitPublication
		ops         any
		docs        any
		incarnation *docIncarnation
	}{
		{
			name:        "a retirement of another incarnation leaves the mounted document",
			publication: change(otherID, nil, nil),
			ops:         nil,
			docs:        map[string]any{kind: map[string]any{"tools": []any{"a"}}},
			incarnation: &docIncarnation{id: mountedID, version: 1},
		},
		{
			name:        "a retirement of the mounted incarnation unmounts it",
			publication: change(mountedID, nil, nil),
			ops:         []any{[]any{"d", []any{"docs", kind}}},
			docs:        map[string]any{},
		},
		{
			name:        "an update of another incarnation at the mounted version is set whole",
			publication: change(otherID, version(1), delta.JsonObjectOf("tools", []any{"c"}), setTools),
			ops:         []any{[]any{"s", []any{"docs", kind}, map[string]any{"tools": []any{"c"}}}},
			docs:        map[string]any{kind: map[string]any{"tools": []any{"c"}}},
			incarnation: &docIncarnation{id: otherID, version: 1},
		},
		{
			name:        "an update of the mounted incarnation at another version is set whole",
			publication: change(mountedID, version(2), delta.JsonObjectOf("tools", []any{"c"}), setTools),
			ops:         []any{[]any{"s", []any{"docs", kind}, map[string]any{"tools": []any{"c"}}}},
			docs:        map[string]any{kind: map[string]any{"tools": []any{"c"}}},
			incarnation: &docIncarnation{id: mountedID, version: 2},
		},
		{
			name:        "an update of the mounted incarnation and version applies its ops",
			publication: change(mountedID, version(1), delta.JsonObjectOf("tools", []any{"c"}), setTools),
			ops:         []any{[]any{"s", []any{"docs", kind, "tools"}, []any{"b"}}},
			docs:        map[string]any{kind: map[string]any{"tools": []any{"b"}}},
			incarnation: &docIncarnation{id: mountedID, version: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mount := newMount()
			var advanced []durable.Op
			observer := &ViewObserver{Advance: func(_ context.Context, _ ConversationView, ops []durable.Op) { advanced = ops }}
			advanceView(t.Context(), conversation, mount, []*ViewObserver{observer}, tc.publication, func(err error) { t.Errorf("observer reported %v", err) })
			expectSameJSON(t, advanced, tc.ops)
			expectSameJSON(t, mount.value.Docs, tc.docs)
			got, mounted := mount.docs[kind]
			switch {
			case tc.incarnation == nil && mounted:
				t.Fatalf("incarnation %+v is still mounted", got)
			case tc.incarnation != nil && (!mounted || got != *tc.incarnation):
				t.Fatalf("mounted incarnation %+v (present %v), want %+v", got, mounted, *tc.incarnation)
			}
		})
	}
}
