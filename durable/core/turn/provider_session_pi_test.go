package turn_test

// pi: packages/durable/src/harness/provider.ts (ensureProviderSessionId :23-39)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// provider.ts:23-39: a legacy conversation without `pi.provider` gets its session identity in one migration commit before the request starts; when the commit yields no identity the generation throws
// `Conversation <id> has no provider session ID` and the model is never asked.
// mutation-checked: dropping the empty-identity check starts the request with an empty session ID and fails it.
func TestGenerationFailsWhenTheMigrationCommitYieldsNoProviderSessionID(t *testing.T) {
	r := newRig(t)
	r.host.NewSessionID = func() string { return "" }
	r.start("hi")
	r.resume()
	r.run()
	if len(r.requests) != 0 {
		t.Fatalf("the model was asked %d time(s) without a provider session ID", len(r.requests))
	}
	tasks := r.tasks(turn.KindGeneration)
	if len(tasks) != 1 {
		t.Fatalf("generation tasks = %d", len(tasks))
	}
	outcome := outcomeOf(t, tasks[0])
	if !strings.Contains(outcome, "Conversation 1 has no provider session ID") {
		t.Errorf("outcome = %s", outcome)
	}
}
