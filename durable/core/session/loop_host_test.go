package session_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// hopClock runs a Loop's hops chains on a virtual tick clock, in the order they started.
type hopClock struct {
	loop *session.Loop
	now  int
	due  []hopDue
}

type hopDue struct {
	at int
	r  session.Resumer
}

func newHopClock(l *session.Loop) *hopClock {
	c := &hopClock{loop: l}
	l.Hops = func(n int, r session.Resumer) bool { c.due = append(c.due, hopDue{c.now + n, r}); return true }
	return c
}

func (c *hopClock) run(t *testing.T) {
	t.Helper()
	for {
		if err := c.loop.Run(); err != nil {
			t.Fatal(err)
		}
		if len(c.due) == 0 {
			return
		}
		next := slices.IndexFunc(c.due, func(d hopDue) bool {
			return d.at == slices.MinFunc(c.due, func(a, b hopDue) int { return a.at - b.at }).at
		})
		d := c.due[next]
		c.due = slices.Delete(c.due, next, next+1)
		c.now = d.at
		d.r.Resume()
	}
}

// A host job's done is called lead ticks before the tick its caller would resume, and its outcome is still delivered at its own tick.
func TestLoopHostJobAnswersItsCallerAheadOfItsResumption(t *testing.T) {
	at := func(lead int) int {
		loop := &session.Loop{}
		c := newHopClock(loop)
		answered := -1
		h := loop.LineHostLead(func() error { return nil }, func(error) { answered = c.now }, lead)
		c.run(t)
		start := c.now
		h.End(nil)
		c.run(t)
		return answered - start
	}
	full := at(0)
	if got := at(2); got != full-2 {
		t.Fatalf("a lead of 2 answers at %d, want %d (the caller resumes at %d)", got, full-2, full)
	}
	if got := at(1); got != full-1 {
		t.Fatalf("a lead of 1 answers at %d, want %d", got, full-1)
	}
}

// The writer publishes each part inside the Storage's scope when the host has one: it waits for the host to run the publication, and goes on after.
func TestWriterPublishesInTheHostStoragesScope(t *testing.T) {
	mem := sessiontest.New()
	sess := session.New(mem, mem.NextID(), func() float64 { return 1 })
	loop := bound(sess)
	var scopes []func()
	sess.Publishing = func(publish func()) bool { scopes = append(scopes, publish); return true }
	var published []int
	for i := range 2 {
		sess.Writer().Admit(&session.Part{
			Writes:  []session.Write{{Kind: session.WriteConversation, Record: obj("id", float64(i+1))}},
			Keys:    []string{session.ConversationKey(int64(i + 1))},
			Publish: func(session.Seq) error { published = append(published, i); return nil },
		})
	}
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || len(published) != 0 {
		t.Fatalf("the first part waits for the host's scope: %d scopes, %d published", len(scopes), len(published))
	}
	scopes[0]()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 || !slices.Equal(published, []int{0}) {
		t.Fatalf("the first part is published and the second, in the next call, asks for its scope: %d scopes, published %v", len(scopes), published)
	}
	scopes[1]()
	if err := loop.Run(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, []int{0, 1}) || !loop.Idle() {
		t.Fatalf("published %v", published)
	}
}
