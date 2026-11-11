package sqlhost

import "testing"

// The mailbox is a FIFO that keeps its backing array while it drains and refills, whatever the interleaving of put and take.
func TestMailboxKeepsOrderAndItsBackingArray(t *testing.T) {
	m := newMailbox()
	var order []int
	push := func(n int) { m.put(func(error) { order = append(order, n) }) }
	pop := func() {
		job, ok := m.take()
		if !ok {
			t.Fatal("take on an open mailbox with queued work returned closed")
		}
		job(nil)
	}
	next, most := 0, 0
	for round := range 200 {
		for range 5 + round%7 {
			push(next)
			next++
		}
		most = max(most, len(m.items)-m.head)
		for range 3 + round%5 {
			if len(m.items) > m.head {
				pop()
			}
		}
	}
	for len(m.items) > m.head {
		pop()
	}
	most = max(most, 1)
	for i, n := range order {
		if n != i {
			t.Fatalf("item %d ran at position %d", n, i)
		}
	}
	if len(order) != next || m.head != 0 || len(m.items) != 0 {
		t.Fatalf("ran %d of %d, head %d len %d", len(order), next, m.head, len(m.items))
	}
	if c := cap(m.items); c > 2*most+8 {
		t.Fatalf("the queue grew its backing array to %d for at most %d queued", c, most)
	}
	push(1)
	m.close()
	if rest := m.drain(); len(rest) != 1 {
		t.Fatalf("drain returned %d items", len(rest))
	}
}
