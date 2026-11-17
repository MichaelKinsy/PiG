package tui

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// The session of the fullscreen scroll-frame measurements (internal/codingagent/scroll_frame_bench_test.go for PiG, tui/testdata/
// scroll_frame_bench.mjs for Pi's make-scroll-frames-O(viewport) commit b7bb7ca905b8): a pure function of the turn index, written once to
// a spec file both sides read, so the two renderers draw the same content.
//
//	PIG_SCROLL_FRAME_SPEC=/path/spec.json go test ./tui -run TestWriteScrollFrameSpec
//
// A turn is what interactive mode mounts for one model turn: a user message every twentieth turn, an assistant message with a thinking
// run and a Markdown answer (headings, bullet lists with bold and inline code, code fences, paragraphs), and the finished bash call with
// 15 lines of output.

type scrollFrameTurn struct {
	User     string `json:"user,omitempty"`
	Thinking string `json:"thinking"`
	Answer   string `json:"answer"`
	Command  string `json:"command"`
	Output   string `json:"output"`
}

// scrollFrameTurns returns the first n turns of the benchmark session.
func scrollFrameTurns(n int) []scrollFrameTurn {
	rng := rand.New(rand.NewSource(1))
	turns := make([]scrollFrameTurn, n)
	for i := range turns {
		turn := &turns[i]
		if i%20 == 0 {
			turn.User = "Next task: " + benchSentence(rng, 30)
		}
		turn.Thinking = benchSentence(rng, 436)
		turn.Answer = benchMarkdown(rng, 2620)
		turn.Command = fmt.Sprintf("echo history %d", i)
		lines := make([]string, 15)
		for j := range lines {
			lines[j] = benchSentence(rng, 12)
		}
		turn.Output = strings.Join(lines, "\n")
	}
	return turns
}

// TestWriteScrollFrameSpec writes the benchmark session for both sides of the measurement; it does nothing unless
// PIG_SCROLL_FRAME_SPEC names the file. PIG_SCROLL_FRAME_TURNS (default 1600) is the number of turns written.
func TestWriteScrollFrameSpec(t *testing.T) {
	path := os.Getenv("PIG_SCROLL_FRAME_SPEC")
	if path == "" {
		t.Skip("PIG_SCROLL_FRAME_SPEC is not set")
	}
	turns := 1600
	if v := os.Getenv("PIG_SCROLL_FRAME_TURNS"); v != "" {
		if _, err := fmt.Sscan(v, &turns); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(scrollFrameTurns(turns))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
