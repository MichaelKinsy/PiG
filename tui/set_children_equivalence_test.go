package tui

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// TestContainerSetChildrenMatchesFreshRender: a container whose children are replaced, reordered, edited and invalidated renders
// what rendering each child afresh renders, at every step: SetChildren keeps only the cached lines of children it keeps.
func TestContainerSetChildrenMatchesFreshRender(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 0))
	pool := make([]Component, 0, 24)
	texts := make([]*Text, 0, 12)
	mds := make([]*Markdown, 0, 12)
	for i := range 12 {
		text := NewText(fmt.Sprintf("text %d", i))
		md := NewMarkdown(fmt.Sprintf("**markdown** %d with `code`", i))
		texts, mds = append(texts, text), append(mds, md)
		pool = append(pool, text, md, NewMouseRegion(NewText(fmt.Sprintf("region %d", i)), nil))
	}
	pool = append(pool, NewSpacer(1), NewSpacer(2))
	c := NewContainer()
	var children []Component
	for step := range 3000 {
		switch rng.IntN(5) {
		case 0: // replace the middle, keep a prefix and a suffix
			head, tail := rng.IntN(len(children)+1), 0
			if head < len(children) {
				tail = rng.IntN(len(children) - head + 1)
			}
			next := slices.Clone(children[:head])
			for range rng.IntN(4) {
				next = append(next, pool[rng.IntN(len(pool))])
			}
			children = append(next, children[len(children)-tail:]...)
		case 1: // a whole new list
			children = children[:0:0]
			for range rng.IntN(8) {
				children = append(children, pool[rng.IntN(len(pool))])
			}
		case 2:
			texts[rng.IntN(len(texts))].SetText(fmt.Sprintf("edited text %d", step))
		case 3:
			mds[rng.IntN(len(mds))].SetText(fmt.Sprintf("edited *markdown* %d", step))
		case 4:
			pool[rng.IntN(len(pool))].Invalidate()
		}
		c.SetChildren(children...)
		width := []int{20, 40}[rng.IntN(2)]
		var want []string
		for _, child := range children {
			want = append(want, child.Render(width)...)
		}
		if got := c.Render(width); !slices.Equal(got, want) {
			t.Fatalf("step %d width %d:\n got %q\nwant %q", step, width, got, want)
		}
	}
}

// TestStreamedAssistantMessageMatchesFreshBlock: an assistant block updated delta by delta (text, thinking, tool calls, hidden
// thinking, a terminal error) renders, after every delta, what a block built from that message renders.
func TestStreamedAssistantMessageMatchesFreshBlock(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 0))
	words := strings.Fields("plan the change **bold** `code` and\n\n- a list item\n- another one\n\nthen the answer ends here")
	for round := range 40 {
		hidden := round%3 == 0
		streamed := NewAssistantMessageComponent(nil, hidden, nil, "", nil, nil)
		var message AssistantMessage
		for range 60 {
			switch r := rng.IntN(10); {
			case r < 5 && len(message.Content) > 0 && message.Content[len(message.Content)-1].Type != "toolCall":
				last := &message.Content[len(message.Content)-1]
				word := words[rng.IntN(len(words))] + " "
				if last.Type == "text" {
					last.Text += word
				} else {
					last.Thinking += word
				}
			case r < 7:
				message.Content = append(message.Content, AssistantContentBlock{Type: "text", Text: words[rng.IntN(len(words))]})
			case r < 9:
				message.Content = append(message.Content, AssistantContentBlock{Type: "thinking", Thinking: words[rng.IntN(len(words))]})
			default:
				message.Content = append(message.Content, AssistantContentBlock{Type: "toolCall"})
			}
			if rng.IntN(15) == 0 {
				message.StopReason, message.ErrorMessage = []string{"length", "error", "aborted", ""}[rng.IntN(4)], "boom"
			}
			streamed.UpdateContent(message, true)
			hasToolCalls := slices.ContainsFunc(message.Content, func(block AssistantContentBlock) bool { return block.Type == "toolCall" })
			streamed.SetHasToolCalls(hasToolCalls)
			fresh := NewAssistantMessageComponent(&message, hidden, nil, "", nil, nil)
			for _, width := range []int{30, 80} {
				if got, want := streamed.Render(width), fresh.Render(width); !slices.Equal(got, want) {
					t.Fatalf("round %d message %+v width %d:\n got %q\nwant %q", round, message, width, got, want)
				}
			}
		}
	}
}
