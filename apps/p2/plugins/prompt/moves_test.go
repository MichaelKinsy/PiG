package prompt

import (
	"context"
	"errors"
	"sync"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// What Pi's relocate.test.ts ("moves a viewed idle worker in place: ... prompt and workspace follow", "moves following
// subagents with the session") shows through a whole server, the cases below pin on the loads themselves: the session's
// prompt resources are loaded anew for a newer move only (never again for an older or the same one), and a conversation
// that works where the session does shares the session's loads, any other location has its own.

// loadsOpened opens prompt loads that fail at once (no environments), and records the locations it was asked for.
type loadsOpened struct {
	mu        sync.Mutex
	locations []p2runtime.Location
	dataDir   string
}

func (o *loadsOpened) open(at p2runtime.Location) *promptLoads {
	o.mu.Lock()
	o.locations = append(o.locations, at)
	o.mu.Unlock()
	return newPromptLoads(o.dataDir, at, func() (p2runtime.ExecutionEnvironments, error) {
		return nil, errors.New("no environments in this test")
	})
}

func (o *loadsOpened) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.locations)
}

func TestThePromptLoadsMoveOnlyForANewerMove(t *testing.T) {
	opened := &loadsOpened{dataDir: t.TempDir()}
	location := p2runtime.NewSessionLocation(p2runtime.Location{Cwd: "/start"})
	moving := newMovingLoads(location, opened.open)
	defer moving.close()
	first := moving.loads()
	version := location.Version()
	if opened.count() != 1 {
		t.Fatalf("opened %d loads at the start, want 1", opened.count())
	}
	// A newer move loads anew where the session is now, and closes the old loads.
	moving.move(p2runtime.Location{Cwd: "/second"}, version+1)
	second := moving.loads()
	if second == first || opened.count() != 2 {
		t.Fatalf("a newer move: loads replaced = %v, opened %d, want replaced and 2", second != first, opened.count())
	}
	select {
	case <-first.finished:
	default:
		t.Fatal("the old location's loads were not closed by the move")
	}
	// The same move again, and an older one (delivered late), change nothing: an older one would put the session's prompt
	// back where it was.
	moving.move(p2runtime.Location{Cwd: "/second"}, version+1)
	moving.move(p2runtime.Location{Cwd: "/start"}, version)
	if moving.loads() != second || opened.count() != 2 {
		t.Fatalf("the same and an older move: loads replaced = %v, opened %d, want kept and 2", moving.loads() != second, opened.count())
	}
	moving.move(p2runtime.Location{Cwd: "/third"}, version+2)
	if moving.loads() == second || opened.count() != 3 {
		t.Fatalf("a still newer move: loads replaced = %v, opened %d, want replaced and 3", moving.loads() != second, opened.count())
	}
}

// fixedEnvironments are environments of a session at `session` where each conversation works at its own place.
type fixedEnvironments struct {
	session p2runtime.Location
	at      map[durable.ConversationId]p2runtime.Location
}

func (e fixedEnvironments) Session() p2runtime.Location { return e.session }
func (e fixedEnvironments) Locate(_ context.Context, id durable.ConversationId, _ durable.DocumentReader) (p2runtime.Location, error) {
	if at, ok := e.at[id]; ok {
		return at, nil
	}
	return e.session, nil
}
func (fixedEnvironments) At(context.Context, p2runtime.Location) (durableenv.ExecutionEnv, error) {
	return nil, errors.New("no environments in this test")
}
func (fixedEnvironments) Sandbox(context.Context, *string) (string, error) { return "", nil }

func TestConversationsWhereTheSessionWorksShareItsLoadsAndOtherLocationsHaveTheirOwn(t *testing.T) {
	opened := &loadsOpened{dataDir: t.TempDir()}
	session := p2runtime.Location{Cwd: "/session"}
	own := p2runtime.Location{Cwd: "/own"}
	environments := fixedEnvironments{session: session, at: map[durable.ConversationId]p2runtime.Location{3: own, 4: own, 5: {Cwd: "/other"}}}
	moving := newMovingLoads(p2runtime.NewSessionLocation(session), opened.open)
	files := &sessionFiles{
		session:      moving,
		environments: func() (p2runtime.ExecutionEnvironments, error) { return environments, nil },
		open:         opened.open,
		own:          map[string]*promptLoads{},
	}
	defer files.close()
	of := func(id durable.ConversationId) loads {
		t.Helper()
		found, err := files.of(context.Background(), id, nil)
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	// The main conversation, and a subagent that follows the session (it locates at the session's location).
	if of(1) != loads(moving) || of(2) != loads(moving) {
		t.Fatal("a conversation working where the session does has loads of its own")
	}
	if opened.count() != 1 {
		t.Fatalf("opened %d loads for the session's conversations, want the session's one", opened.count())
	}
	// A subagent with its own location: its own loads, shared by the conversations working at the same place.
	mine := of(3)
	if mine == loads(moving) {
		t.Fatal("a conversation with its own location got the session's loads")
	}
	if of(4) != mine || of(3) != mine {
		t.Fatal("conversations at one location do not share its loads")
	}
	if other := of(5); other == mine || other == loads(moving) {
		t.Fatal("another location shares loads with a different one")
	}
	if opened.count() != 3 {
		t.Fatalf("opened %d loads, want 3 (the session's and two locations')", opened.count())
	}
}
