package coding_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// A program that shows one page of a long session lists the branch by reference, then reads only the entries of the page.
func ExampleSessionManager_ReadEntries() {
	manager, _ := coding.NewInMemorySessionManager(".")
	for _, text := range []string{"one", "two", "three", "four"} {
		_, _ = manager.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}})
	}
	refs := manager.BranchRefs() // the whole branch, in order, with no entry body read
	entries, err := manager.ReadEntries(context.Background(), []string{refs[2].ID, refs[1].ID})
	if changed, ok := errors.AsType[*coding.RecordChangedError](err); ok {
		fmt.Println("cannot read back", changed.ID)
	}
	fmt.Println(len(refs), len(entries), err, entries[0].Base().ID == refs[2].ID)
	// Output: 4 2 <nil> true
}
