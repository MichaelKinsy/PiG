package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestSessionModelSelectIgnoresEquivalentMetadataOnlyModel(t *testing.T) {
	// Pi agent-session.ts:_emitModelSelect suppresses the event for an equal ID/provider tuple, even when metadata or transport realization changes.
	var events []string
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"model_select": {func(args ...any) (any, error) {
		event := args[0].(extension.ModelSelectEvent)
		previous := event.PreviousModel
		model := event.Model
		events = append(events, previous.ID+"->"+model.ID+":"+event.Source)
		return nil, nil
	}}}}
	h := newModelExtensionHarness(t, []bool{true, true}, "", true, ext, nil)
	current := *h.session.Model()
	current.Provider = nil
	h.session.Agent().SetModel(&current)
	replacement := *h.models[0]
	replacement.DisplayName = "Updated metadata"
	if err := h.session.SetModel(&replacement); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || h.session.Model().DisplayName != "Updated metadata" {
		t.Fatalf("equivalent model emitted selection or lost metadata: events=%v model=%+v", events, h.session.Model())
	}
	next := *h.models[1]
	if err := h.session.SetModel(&next); err != nil {
		t.Fatal(err)
	}
	if err := h.session.SetModel(&next); err != nil {
		t.Fatal(err)
	}
	if want := []string{"faux-1->faux-2:set"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
}
