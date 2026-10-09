package durable

import "testing"

// pi: packages/durable/src/ids.ts

// idFromNumber and seqFromNumber (ids.ts:4-12) apply an erased brand to a trusted number: the value is unchanged, only the kind differs.
func TestIdFromNumberAndSeqFromNumberKeepTheNumberLikePi(t *testing.T) {
	for _, n := range []int64{0, 1, 42, 1 << 40, -1} {
		if got := IdFromNumber[TaskId](n); int64(got) != n {
			t.Errorf("IdFromNumber[TaskId](%d) = %d", n, got)
		}
		if got := IdFromNumber[ConversationId](n); int64(got) != n {
			t.Errorf("IdFromNumber[ConversationId](%d) = %d", n, got)
		}
		if got := SeqFromNumber(n); int64(got) != n {
			t.Errorf("SeqFromNumber(%d) = %d", n, got)
		}
	}
}
