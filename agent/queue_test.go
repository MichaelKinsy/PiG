package agent

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func userMsg(text string) AgentMessage {
	return AgentMessage{
		User: &UserMessage{
			Role:    RoleUser,
			Content: []ai.UserContentBlock{ai.TextContent{Text: text}},
		},
	}
}

func userMsgText(m AgentMessage) string {
	if m.User == nil || len(m.User.Content) == 0 {
		return ""
	}
	if tc, ok := m.User.Content[0].(ai.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestQueue_DrainAll(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	q.Enqueue(userMsg("a"))
	q.Enqueue(userMsg("b"))
	q.Enqueue(userMsg("c"))

	got := q.Drain()
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if g := userMsgText(got[i]); g != want {
			t.Errorf("got[%d] = %q, want %q", i, g, want)
		}
	}
	// Drain again should return nil.
	if d := q.Drain(); d != nil {
		t.Fatalf("second drain: want nil, got %d", len(d))
	}
}

func TestQueue_DrainOneAtATime(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeOneAtATime)
	q.Enqueue(userMsg("a"))
	q.Enqueue(userMsg("b"))
	q.Enqueue(userMsg("c"))

	got1 := q.Drain()
	if len(got1) != 1 || userMsgText(got1[0]) != "a" {
		t.Fatalf("first drain: want [a], got %v", got1)
	}
	got2 := q.Drain()
	if len(got2) != 1 || userMsgText(got2[0]) != "b" {
		t.Fatalf("second drain: want [b], got %v", got2)
	}
	got3 := q.Drain()
	if len(got3) != 1 || userMsgText(got3[0]) != "c" {
		t.Fatalf("third drain: want [c], got %v", got3)
	}
	if d := q.Drain(); d != nil {
		t.Fatalf("fourth drain: want nil, got %d", len(d))
	}
}

func TestQueue_DrainEmptyReturnsNil(t *testing.T) {
	for _, mode := range []QueueMode{QueueModeAll, QueueModeOneAtATime} {
		q := NewPendingMessageQueue(mode)
		if d := q.Drain(); d != nil {
			t.Errorf("mode %q: want nil, got %v", mode, d)
		}
	}
}

func TestQueue_Clear(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	q.Enqueue(userMsg("a"))
	q.Enqueue(userMsg("b"))

	cleared := q.Clear()
	if len(cleared) != 2 {
		t.Fatalf("want 2 cleared, got %d", len(cleared))
	}
	if q.HasItems() {
		t.Fatal("queue should be empty after Clear")
	}
}

func TestQueue_HasItems(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	if q.HasItems() {
		t.Fatal("new queue should not have items")
	}
	q.Enqueue(userMsg("a"))
	if !q.HasItems() {
		t.Fatal("queue should have items after enqueue")
	}
	q.Drain()
	if q.HasItems() {
		t.Fatal("queue should not have items after drain")
	}
}

func TestQueue_SetMode(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	if q.Mode() != QueueModeAll {
		t.Fatalf("want all, got %q", q.Mode())
	}
	q.SetMode(QueueModeOneAtATime)
	if q.Mode() != QueueModeOneAtATime {
		t.Fatalf("want one-at-a-time, got %q", q.Mode())
	}
}

func TestQueue_Peek(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	q.Enqueue(userMsg("a"))
	q.Enqueue(userMsg("b"))

	peeked := q.Peek()
	if len(peeked) != 2 {
		t.Fatalf("want 2 peeked, got %d", len(peeked))
	}
	// Peek should not drain.
	if q.Len() != 2 {
		t.Fatalf("peek should not drain; len = %d", q.Len())
	}
	// Peek previews what Drain would select (upstream peek); Messages lists all.
	q.SetMode(QueueModeOneAtATime)
	if got := q.Peek(); len(got) != 1 || len(q.Messages()) != 2 {
		t.Fatalf("one-at-a-time peek = %d, messages = %d; want 1 and 2", len(got), len(q.Messages()))
	}
}

func TestQueue_ConcurrentEnqueueDrain(t *testing.T) {
	q := NewPendingMessageQueue(QueueModeAll)
	const N = 100
	var wg sync.WaitGroup

	// Concurrent enqueue.
	wg.Go(func() {
		for range N {
			q.Enqueue(userMsg("x"))
		}
	})

	// Concurrent drain.
	var drained int
	wg.Go(func() {
		for range N * 2 {
			d := q.Drain()
			drained += len(d)
		}
	})

	wg.Wait()
	// Drain any remaining.
	drained += len(q.Drain())
	if drained != N {
		t.Fatalf("want %d total drained, got %d", N, drained)
	}
}
