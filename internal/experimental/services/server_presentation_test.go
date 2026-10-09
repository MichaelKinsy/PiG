package services

import (
	"context"
	"errors"
	"testing"
)

type presentationCtxKey struct{}

// presentationRecorder is a RoutedServerPresentation that records each capability call and the context value it ran under.
type presentationRecorder struct {
	events []string
	fail   map[string]error
}

func (r *presentationRecorder) record(ctx context.Context, name string) error {
	tag, _ := ctx.Value(presentationCtxKey{}).(string)
	r.events = append(r.events, name+"@"+tag)
	return r.fail[name]
}
func (r *presentationRecorder) AttachSession(ctx context.Context, id string) error {
	return r.record(ctx, "attach:"+id)
}
func (r *presentationRecorder) DetachSession(ctx context.Context) error {
	return r.record(ctx, "detach")
}
func (r *presentationRecorder) PrepareSessionRemoval(ctx context.Context, id string) error {
	return r.record(ctx, "prepareRemoval:"+id)
}

// packages/server/src/types.ts RoutedServerPresentation: the server's management service reaches the presentation through attachSession(sessionId, context), detachSession(context) and prepareSessionRemoval(sessionId, context). Removal asks the presentation to release before the application deletes the session, and a failure of any capability call fails the member.
func TestRoutedServerPresentationCapabilitiesRunUnderTheCallersContext(t *testing.T) {
	removed := []string{}
	services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
		List:   func(context.Context) ([]SessionSummary, error) { return nil, nil },
		Remove: func(_ context.Context, id string) error { removed = append(removed, id); return nil },
	})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, services.Dispose()) })
	denied := errors.New("presentation refused")
	recorder := &presentationRecorder{fail: map[string]error{}}
	var capability RoutedServerPresentation = recorder
	attachment, err := services.Host.AttachClient(t.Context(), capability)
	requireModelsOK(t, err)

	ctx := context.WithValue(t.Context(), presentationCtxKey{}, "caller")
	for _, step := range []struct {
		member string
		args   []any
	}{{"attach", []any{"a"}}, {"remove", []any{"a"}}, {"detach", nil}} {
		_, err := serverCall(ctx, attachment, SessionManagementDefinition.Id(), step.member, step.args...)
		requireModelsOK(t, err)
	}
	checkModelsEqual(t, recorder.events, []string{"attach:a@caller", "prepareRemoval:a@caller", "detach@caller"})
	checkModelsEqual(t, removed, []string{"a"})

	recorder.events = nil
	recorder.fail["prepareRemoval:b"] = denied
	if _, err := serverCall(ctx, attachment, SessionManagementDefinition.Id(), "remove", "b"); !errors.Is(err, denied) {
		t.Fatalf("remove with a refusing presentation = %v, want %v", err, denied)
	}
	checkModelsEqual(t, removed, []string{"a"})
	recorder.fail["attach:c"], recorder.fail["detach"] = denied, denied
	if _, err := serverCall(ctx, attachment, SessionManagementDefinition.Id(), "attach", "c"); !errors.Is(err, denied) {
		t.Fatalf("attach error = %v, want %v", err, denied)
	}
	if _, err := serverCall(ctx, attachment, SessionManagementDefinition.Id(), "detach"); !errors.Is(err, denied) {
		t.Fatalf("detach error = %v, want %v", err, denied)
	}
	if err := capability.AttachSession(ctx, "direct"); err != nil || recorder.events[len(recorder.events)-1] != "attach:direct@caller" {
		t.Fatalf("direct capability call = %v, events %v", err, recorder.events)
	}
}
