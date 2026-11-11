// SPDX-License-Identifier: MIT

package ai

import (
	"fmt"
	"testing"
)

// A delivered handle is an independent copy of the cell's publication: the content blocks, the usage counters and the content slice's capacity belong to the handle, whatever the number of blocks the handle keeps in its own allocation.
func TestDeliveredMessageOwnsItsData(t *testing.T) {
	for _, blocks := range []int{0, 1, deliveredInlineBlocks, deliveredInlineBlocks + 1} {
		t.Run(fmt.Sprintf("%d blocks", blocks), func(t *testing.T) {
			cacheWrite, reasoning := 1, 2
			content := make([]AssistantContentBlock, 0, blocks)
			for i := range blocks {
				content = append(content, TextContent{Text: fmt.Sprint("t", i)})
			}
			message := AssistantMessage{Content: content, Usage: Usage{Input: 3, CacheWrite1h: &cacheWrite, Reasoning: &reasoning}}
			cell := newAssistantMessageCell(&message)
			view := cell.view()
			message.Usage.Input = 4
			cell.publish(&message, assistantMessageReplacements{})

			delivered := view.delivered()
			if delivered == view {
				t.Fatal("a handle copied before the latest publication was returned as it is")
			}
			if delivered.Usage.Input != 4 || delivered.Usage.CacheWrite1h == nil || *delivered.Usage.CacheWrite1h != 1 || delivered.Usage.Reasoning == nil || *delivered.Usage.Reasoning != 2 {
				t.Fatalf("delivered usage = %+v", delivered.Usage)
			}
			if delivered.Content == nil || len(delivered.Content) != blocks || cap(delivered.Content) != blocks {
				t.Fatalf("delivered content has length %d and capacity %d (nil %t), want %d", len(delivered.Content), cap(delivered.Content), delivered.Content == nil, blocks)
			}
			if delivered.observation == nil || delivered.observation.cell != cell || delivered.observation.seq != cell.seq {
				t.Fatalf("delivered observation = %+v", delivered.observation)
			}

			*delivered.Usage.CacheWrite1h, *delivered.Usage.Reasoning = 99, 99
			if blocks > 0 {
				delivered.Content[0] = TextContent{Text: "changed"}
			}
			_ = append(delivered.Content, TextContent{Text: "appended"})
			if cell.current.Usage.CacheWrite1h == delivered.Usage.CacheWrite1h || *cell.current.Usage.CacheWrite1h != 1 || *cell.current.Usage.Reasoning != 2 {
				t.Errorf("writing the delivered usage reached the cell: %+v", cell.current.Usage)
			}
			if cacheWrite != 1 || reasoning != 2 {
				t.Errorf("writing the delivered usage reached the producer's counters: %d, %d", cacheWrite, reasoning)
			}
			if blocks > 0 && cell.current.Content[0].(TextContent).Text != "t0" {
				t.Errorf("writing the delivered content reached the cell: %v", cell.current.Content[0])
			}
		})
	}
}

// A published snapshot keeps the previous snapshot's copy of an optional usage counter that did not change, and copies one that did.
func TestSharedCounterKeepsUnchangedCopy(t *testing.T) {
	five, other := 5, 5
	earlier := &five
	if got := sharedCounter(&other, earlier); got != earlier {
		t.Error("an unchanged counter was copied again")
	}
	other = 6
	if got := sharedCounter(&other, earlier); got == earlier || *got != 6 {
		t.Errorf("a changed counter kept the earlier copy: %v", got)
	}
	if got := sharedCounter[int](nil, earlier); got != nil {
		t.Errorf("an absent counter became %v", *got)
	}
	if got := sharedCounter(&other, nil); got == nil || got == &other || *got != 6 {
		t.Errorf("a first counter was not copied: %v", got)
	}
}
