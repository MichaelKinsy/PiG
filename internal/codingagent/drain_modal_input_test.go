package codingagent

import "testing"

func TestDrainModalInputAppliesAllQueuedChunks(t *testing.T) {
	ch := make(chan []byte, 8)
	ch <- []byte("a")
	ch <- []byte("bc")
	ch <- []byte("d")

	var got []string
	drainModalInput(newTestTree(), ch, func(chunk string) bool {
		got = append(got, chunk)
		return false
	})

	want := []string{"a", "bc", "d"}
	if len(got) != len(want) {
		t.Fatalf("drain applied %d chunks, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
	if len(ch) != 0 {
		t.Fatalf("channel should be fully drained, %d left", len(ch))
	}
}

func TestDrainModalInputStopsWhenDone(t *testing.T) {
	ch := make(chan []byte, 8)
	ch <- []byte("x")
	ch <- []byte("y")
	ch <- []byte("z")

	var got []string
	drainModalInput(newTestTree(), ch, func(chunk string) bool {
		got = append(got, chunk)
		return chunk == "y" // signal done on second chunk
	})

	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("drain should stop at done marker, got %v", got)
	}
	if len(ch) != 1 {
		t.Fatalf("remaining input must stay queued after done, %d left", len(ch))
	}
}

func TestDrainModalInputReturnsWhenEmpty(t *testing.T) {
	ch := make(chan []byte, 1)
	called := false
	drainModalInput(newTestTree(), ch, func(string) bool { called = true; return false })
	if called {
		t.Fatal("handler must not be called when channel is empty")
	}
}
