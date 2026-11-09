// pig additive (D91): a container borrows the rows of a streamed message; what a public Render returns stays its own, unchanged by later renders.

package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

type borrowSpec struct {
	kind   int // 0 markdown, 1 assistant, 2 text, 3 spacer
	deltas []string
	pad    int
}

func borrowBuild(s borrowSpec) Component {
	switch s.kind {
	case 0:
		return NewMarkdown(strings.Join(s.deltas, ""))
	case 1:
		pad := s.pad
		b := NewAssistantMessageComponent(nil, false, nil, "", &pad, nil, "", "")
		for _, d := range s.deltas {
			b.SetTextDelta(d)
		}
		return b
	case 2:
		return NewText(strings.Join(s.deltas, ""))
	}
	return NewSpacer(len(s.deltas))
}

var borrowChunks = []string{"## H\n\n", "text **bold** `c` that wraps around a narrow width for sure ok ", "\n\n- a\n- b\n\n", "```go\nfunc x() {\n", "\treturn\n}\n```\n\n", "| a | b |\n|--|--|\n| 1 | 2 |\n\n", "> quote\n\n", "plain tail ", "é日本語 "}

func TestBorrowedRowsEqualFreshComponents(t *testing.T) {
	for seed := uint64(0); seed < 120; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		var specs []borrowSpec
		chat := NewContainer()
		var comps []Component
		add := func() {
			s := borrowSpec{kind: rng.IntN(4), pad: rng.IntN(3)}
			for range 1 + rng.IntN(3) {
				s.deltas = append(s.deltas, borrowChunks[rng.IntN(len(borrowChunks))])
			}
			specs = append(specs, s)
			c := borrowBuild(s)
			comps = append(comps, c)
			chat.Add(c)
		}
		for range 1 + rng.IntN(6) {
			add()
		}
		width := 50
		var retained [][]string
		var want [][]string
		for step := 0; step < 60; step++ {
			switch op := rng.IntN(10); {
			case op < 4: // stream into a markdown/assistant
				i := rng.IntN(len(comps))
				d := borrowChunks[rng.IntN(len(borrowChunks))]
				switch c := comps[i].(type) {
				case *Markdown:
					specs[i].deltas = append(specs[i].deltas, d)
					c.SetText(strings.Join(specs[i].deltas, ""))
				case *AssistantMessageComponent:
					specs[i].deltas = append(specs[i].deltas, d)
					c.SetTextDelta(d)
				}
			case op < 5:
				add()
			case op < 7:
				width = 20 + rng.IntN(50)
			case op < 9: // public renders of random children in between: must not disturb the container
				i := rng.IntN(len(comps))
				r := comps[i].Render(width)
				retained, want = append(retained, r), append(want, slices.Clone(r))
			}
			var got []string
			if rng.IntN(2) == 0 {
				got = chat.Render(width)
				retained, want = append(retained, got), append(want, slices.Clone(got))
			} else {
				got = slices.Clone(chat.renderBorrowed(width)) // borrowed: read before the next render only
			}
			fresh := NewContainer()
			for _, s := range specs {
				fresh.Add(borrowBuild(s))
			}
			wantRows := fresh.Render(width)
			if !slices.Equal(got, wantRows) {
				t.Fatalf("seed %d step %d width %d: container rows differ from fresh components\n got %q\nwant %q", seed, step, width, got, wantRows)
			}
		}
		for i := range retained {
			if !slices.Equal(retained[i], want[i]) {
				t.Fatalf("seed %d: rows returned by an owned Render (#%d) changed afterwards", seed, i)
			}
		}
	}
}
