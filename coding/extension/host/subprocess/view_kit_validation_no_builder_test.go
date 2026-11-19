package subprocess

import (
	"encoding/json"
	"strings"
	"testing"
)

// D107: validation needs no builder: a view that holds a node that is not valid is refused with the validator's own sentence in a program that enabled no builder, as in one that enabled them all; the sentence that names the call is only for a valid node whose builder is missing.
func TestInvalidViewIsRefusedTheSameWayWithoutABuilder(t *testing.T) {
	views := map[string]string{
		"grid columns":   `{"root":{"kind":"grid","id":"g","columns":0}}`,
		"grid no id":     `{"root":{"kind":"grid","columns":2}}`,
		"editor id":      `{"root":{"kind":"editor"}}`,
		"inside a stack": `{"root":{"kind":"vstack","children":[{"kind":"grid","id":"g","columns":17}]}}`,
	}
	refusal := func(view string) string {
		s := newViewSurface("", newViewImageStore())
		defer s.close()
		err := s.accept(json.RawMessage(view), nil)
		if err == nil {
			t.Fatalf("view accepted: %s", view)
		}
		return err.Error()
	}
	enabled := map[string]string{}
	for name, view := range views {
		enabled[name] = refusal(view)
	}
	withoutViewWidgetBuilders(t)
	for name, view := range views {
		got := refusal(view)
		if got != enabled[name] || strings.Contains(got, "are not enabled") {
			t.Errorf("%s: without a builder %q, with them %q; want the same refusal, the validator's", name, got, enabled[name])
		}
	}
	if got := refusal(`{"root":{"kind":"grid","id":"g","columns":1}}`); !strings.Contains(got, "views are not enabled") {
		t.Errorf("a valid grid without its builder: %q, want the sentence that names the call", got)
	}
}
