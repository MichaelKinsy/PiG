package session_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
)

// buildSessionContext awaits each projector; rejection must prevent subsequent
// callbacks and must not expose the successfully projected prefix.
func TestContextProjectorFailureStopsLaterEffects(t *testing.T) {
	t.Parallel()
	failure := errors.New("projection failed")
	var called []string
	entries := []session.Entry{
		{ID: "first", Type: session.EntryTypeCustom, CustomType: "effect"},
		{ID: "broken", Type: session.EntryTypeCustom, CustomType: "effect"},
		{ID: "never", Type: session.EntryTypeCustom, CustomType: "effect"},
	}
	got, err := session.BuildSessionContext(context.Background(), entries, &session.SessionContextBuildOptions{
		EntryProjectors: map[string]session.EntryProjector{"effect": func(_ context.Context, entry session.Entry) ([]agent.AgentMessage, error) {
			called = append(called, entry.ID)
			if entry.ID == "broken" {
				return nil, failure
			}
			return []agent.AgentMessage{contextUser(entry.ID)}, nil
		}},
	})
	if !errors.Is(err, failure) || got != nil || !reflect.DeepEqual(called, []string{"first", "broken"}) {
		t.Fatalf("context = %v, error = %v, callbacks = %v", got, err, called)
	}
}
