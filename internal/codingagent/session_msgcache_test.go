package codingagent

import (
	"reflect"
	"testing"
)

// TestMessageFor_MatchesAsMessage is the oracle for the parse memo: the
// memoized parse must equal the pure AsMessage for every entry, and a second
// (cache-hit) call must be equal to the first.
func TestMessageFor_MatchesAsMessage(t *testing.T) {
	s, _ := buildLinearSession(t, 200)
	for _, e := range s.Entries() {
		if e.Base.Type != "message" {
			continue
		}
		want, wantOK := e.AsMessage()
		got1, ok1 := s.messageFor(e)
		got2, ok2 := s.messageFor(e) // hit
		if ok1 != wantOK || ok2 != wantOK {
			t.Fatalf("entry %s: ok mismatch memo=(%v,%v) pure=%v", e.Base.ID, ok1, ok2, wantOK)
		}
		if !reflect.DeepEqual(got1, want) || !reflect.DeepEqual(got2, want) {
			t.Fatalf("entry %s: memoized parse != AsMessage", e.Base.ID)
		}
	}
}

// TestBuildContext_MemoDoesNotChangeOutput proves the memo (and the
// WireToMessage content clone) leave BuildContext's LLM payload byte-identical
// whether the cache is cold or warm.
func TestBuildContext_MemoDoesNotChangeOutput(t *testing.T) {
	s, leaf := buildLinearSession(t, 200)

	cold := s.BuildContext(&leaf) // populates the memo
	warm := s.BuildContext(&leaf) // all hits

	if !reflect.DeepEqual(cold, warm) {
		t.Fatalf("BuildContext output differs cold vs warm (memo changed output)")
	}
	if len(cold) != 200 {
		t.Fatalf("BuildContext len = %d, want 200", len(cold))
	}
}

// BenchmarkBuildContextWarm measures a BuildContext whose memo was pre-warmed
// by a prior walk (the resume-then-turn / repeated-compaction pattern). Compare
// to BenchmarkBuildContext (cold) to see the re-parse the memo removes.
func BenchmarkBuildContextWarm(b *testing.B) {
	s, leaf := buildLinearSession(b, 3000)
	_ = s.BuildContext(&leaf) // warm the memo once
	b.ResetTimer()
	for range b.N {
		_ = s.BuildContext(&leaf)
	}
}
