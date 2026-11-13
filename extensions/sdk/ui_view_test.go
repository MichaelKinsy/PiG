package sdk

import (
	"encoding/json"
	"errors"
	"testing"
)

// The behavior against the host is the shared conformance row TestConformance_UIViewMounts; these cases pin the SDK's own
// decoding of replicated abilities and its answer to a ui.view.intent request.

func TestViewAbilitiesAreReplacedByEveryStateAndAbsentClearsThem(t *testing.T) {
	var s viewState
	s.applyAbilities(json.RawMessage(`{"slots":["panel"],"nodes":["text"],"markdown":true,"imageFormats":["png"],"limits":{"mounts":7,"intentArgBytes":123},"input":{"keyboard":true}}`))
	if s.abilities == nil || s.abilities.Limits.Mounts != 7 || s.abilities.Limits.IntentArgBytes != 123 || !s.abilities.Input.Keyboard || s.abilities.Input.Pointer || s.abilities.Slots[0] != ViewSlotPanel {
		t.Fatalf("abilities = %+v", s.abilities)
	}
	s.applyAbilities(nil)
	if s.abilities != nil {
		t.Fatalf("an absent value must clear the abilities, got %+v", s.abilities)
	}
	s.applyAbilities(json.RawMessage(`{"slots":["panel"]}`))
	s.applyAbilities(json.RawMessage(`null`))
	if s.abilities != nil {
		t.Fatal("null must clear the abilities")
	}
}

func TestViewIntentWithoutHandlerIsRefusedAndHandlerOutcomeDefaultsToDone(t *testing.T) {
	e := &Extension{}
	got, err := e.handleViewIntent(Context{}, json.RawMessage(`{"mount":{"key":"k","epoch":2},"rev":5,"intent":"x.go","arg":{"n":1},"ticket":"t7"}`))
	if err != nil || got.(ViewIntentResult).Outcome != "refused" {
		t.Fatalf("no handler = %+v, %v", got, err)
	}
	var seen ViewIntentInvocation
	e.OnViewIntent(func(in ViewIntentInvocation) ViewIntentResult {
		seen = in
		return ViewIntentResult{Answer: json.RawMessage(`{"ok":1}`)}
	})
	got, err = e.handleViewIntent(Context{}, json.RawMessage(`{"mount":{"key":"k","epoch":2},"rev":5,"nodeKey":"b","intent":"x.go","arg":{"n":1},"ticket":"t7"}`))
	res := got.(ViewIntentResult)
	if err != nil || res.Outcome != "done" || string(res.Answer) != `{"ok":1}` {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if seen.Mount != (ViewRef{Key: "k", Epoch: 2}) || seen.Rev != 5 || seen.NodeKey != "b" || seen.Intent != "x.go" || seen.Ticket != "t7" || string(seen.Arg) != `{"n":1}` {
		t.Fatalf("invocation = %+v", seen)
	}
	if _, err := e.handleViewIntent(Context{}, json.RawMessage(`[`)); err == nil {
		t.Fatal("a malformed request must fail")
	}
}

func TestViewErrorMatchesWithErrorsAs(t *testing.T) {
	var err error = &ViewError{Code: ViewErrorStale, Message: "rev 1 is not current (2)"}
	var ve *ViewError
	if !errors.As(err, &ve) || ve.Code != ViewErrorStale || err.Error() != "stale: rev 1 is not current (2)" {
		t.Fatalf("error = %v", err)
	}
}
