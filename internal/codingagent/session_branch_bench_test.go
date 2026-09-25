package codingagent

import (
	"strconv"
	"testing"
)

// buildLinearSession creates a session with a single linear branch of n
// messages (alternating user/assistant). Each message's parentId chains to
// the prior, mirroring a long real conversation where Branch/BuildContext
// walk the full leaf→root ancestry.
func buildLinearSession(tb testing.TB, n int) (*Session, string) {
	tb.Helper()
	s := NewSession("bench", "/tmp")
	var leaf string
	for i := range n {
		var (
			id  string
			err error
		)
		if i%2 == 0 {
			id, err = s.AppendMessage(mkUserMsg("u" + strconv.Itoa(i)))
		} else {
			id, err = s.AppendMessage(mkAssistantMsg("a" + strconv.Itoa(i)))
		}
		if err != nil {
			tb.Fatalf("append %d: %v", i, err)
		}
		leaf = id
	}
	return s, leaf
}

// TestBranch_OrderRootToLeaf pins the observable contract the O(N^2)→O(N)
// walk optimization must preserve: Branch returns entries in append
// (root→leaf) order. This is the black-box oracle. The internal walk
// strategy may change; this order may not.
func TestBranch_OrderRootToLeaf(t *testing.T) {
	const n = 200
	s, leaf := buildLinearSession(t, n)

	got := s.Branch(leaf)
	if len(got) != n {
		t.Fatalf("Branch len = %d, want %d", len(got), n)
	}
	// entries[i] was appended i-th; on a linear branch Branch must return
	// them in that exact order.
	want := s.Entries()
	for i := range got {
		if got[i].Base.ID != want[i].Base.ID {
			t.Fatalf("Branch[%d].ID = %q, want %q (root→leaf order not preserved)",
				i, got[i].Base.ID, want[i].Base.ID)
		}
	}
}

// TestBuildContext_OrderRootToLeaf pins the same contract for the LLM
// context builder: messages are emitted oldest→newest.
func TestBuildContext_OrderRootToLeaf(t *testing.T) {
	const n = 200
	s, leaf := buildLinearSession(t, n)

	msgs := s.BuildContext(&leaf)
	if len(msgs) != n {
		t.Fatalf("BuildContext len = %d, want %d", len(msgs), n)
	}
	// The linear session alternates user (even index) / assistant (odd).
	// Confirm the sequence is chronological (oldest first), i.e. not reversed.
	for i := range msgs {
		if i%2 == 0 && msgs[i].User == nil {
			t.Fatalf("msg[%d] want user, got non-user (order/shape changed)", i)
		}
		if i%2 == 1 && msgs[i].Assistant == nil {
			t.Fatalf("msg[%d] want assistant, got non-assistant (order/shape changed)", i)
		}
	}
}

const branchBenchN = 3000

func BenchmarkBranch(b *testing.B) {
	s, leaf := buildLinearSession(b, branchBenchN)
	b.ResetTimer()
	for range b.N {
		_ = s.Branch(leaf)
	}
}

func BenchmarkBuildContext(b *testing.B) {
	s, leaf := buildLinearSession(b, branchBenchN)
	b.ResetTimer()
	for range b.N {
		_ = s.BuildContext(&leaf)
	}
}
