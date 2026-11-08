package runtime

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// A relocation lifts its admission fence inside the commit that makes the move visible (the agent cwds'), after the
// SessionLocation is committed: a client that sees the move is admitted at once, and what is admitted reads the new
// location. Pi lifts its fence before a client can observe the commit.
func TestARelocationLiftsItsFenceBeforeTheMovesCommitIsVisible(t *testing.T) {
	opened, err := harness.OpenHarness(t.Context(), storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   ai.CreateModels(ai.CreateModelsOptions{}),
		Registry: harness.CreateRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(t.Context()) })
	if _, err := opened.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Cwd: harness.SetTo("/old")}}); err != nil {
		t.Fatal(err)
	}
	// In sequence: the second move finds the conversations already at the destination.
	for _, name := range []string{"with the agent cwds to commit", "with nothing to commit"} {
		t.Run(name, func(t *testing.T) {
			commits := NewCommitSignal(opened, func(durable.CommitPublication) bool { return true })
			defer commits.Close()
			var order []string
			published := -1
			_, err := followSession(t.Context(), nil, opened, Location{Cwd: "/new"},
				func() { order = append(order, "location") },
				func() { order = append(order, "unfence"); published = commits.Version() })
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(order, []string{"location", "unfence"}) {
				t.Fatalf("order = %v, want the location committed, then the fence lifted", order)
			}
			if name == "with the agent cwds to commit" && commits.Version() <= published {
				t.Fatalf("the fence was lifted after the move's commit was published (%d commits seen at the lift, %d after)", published, commits.Version())
			}
			if name == "with nothing to commit" && commits.Version() != published {
				t.Fatalf("a move with nothing to commit committed (%d at the lift, %d after)", published, commits.Version())
			}
		})
	}
}
