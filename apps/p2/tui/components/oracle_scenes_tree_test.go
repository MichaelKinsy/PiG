package components

import (
	"fmt"
	"testing"
)

func treeNodes() []TreeSelectorNode {
	nodes := []TreeSelectorNode{
		{Entry: 1, Conversation: 1, Role: "user", Text: "start the work"},
		{Entry: 2, Parent: new(1), Conversation: 1, Role: "assistant", Text: "Sure, here is the plan\nwith a second line\x1b]0;x\x07"},
		{Entry: 3, Parent: new(2), Conversation: 1, Role: "user", Text: "continue with the first option"},
		{Entry: 4, Parent: new(3), Conversation: 1, Role: "assistant", Text: "done with the first option"},
		{Entry: 5, Parent: new(2), Conversation: 2, Role: "user", Text: "try the second option instead 日本語"},
		{Entry: 6, Parent: new(5), Conversation: 2, Role: "assistant", Text: "second option answer"},
		{Entry: 7, Parent: new(5), Conversation: 3, Role: "user", Text: "or a third thing entirely"},
		{Entry: 8, Parent: new(7), Conversation: 3, Role: "assistant", Text: "a very long answer that has to be truncated at narrow widths because it keeps going and going"},
		{Entry: 9, Parent: new(4), Conversation: 1, Role: "user", Text: "last on main"},
	}
	for i := range 20 {
		previous := 9 + i
		if i == 0 {
			previous = 9
		}
		role := "assistant"
		if i%2 == 1 {
			role = "user"
		}
		nodes = append(nodes, TreeSelectorNode{Entry: 10 + i, Parent: new(previous), Conversation: 1, Role: role, Text: fmt.Sprintf("chain %d", i)})
	}
	return nodes
}

func userItems() []UserMessageItem {
	items := []UserMessageItem{
		{ID: 3, Text: "first message"},
		{ID: 5, Text: "a second message that is quite long and goes beyond the narrow widths of the viewport at hand"},
		{ID: 8, Text: "multi\nline\tmessage\x1b]0;t\x07 with 日本語"},
	}
	for i := range 12 {
		items = append(items, UserMessageItem{ID: 20 + i, Text: fmt.Sprintf("later message %d", i)})
	}
	return items
}

func init() {
	noop := func() {}
	registerScene("tree-selector", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewTreeSelector(ctx, treeNodes(), new(6), map[int]string{1: "main", 2: "alt branch", 3: "日本語"}, func(int) {}, noop, func(int, string) {}),
			focus:     new(true),
			inputs: []string{keyDownArrow, keyDownArrow, keyDownArrow, keyDownArrow, "\x1b[C", "\x1b[C", "\x1b[D", keyUpArrow, "\x14", "x", "y", "\x7f", keyEnter,
				keyDownArrow, "\x14", keyEsc, keyPageDown, keyPageUp, keyUpArrow, keyUpArrow, keyEnter},
		}
	})
	registerScene("tree-selector-roots", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewTreeSelector(ctx, []TreeSelectorNode{
				{Entry: 1, Conversation: 1, Role: "user", Text: "first root"},
				{Entry: 2, Parent: new(1), Conversation: 1, Role: "assistant", Text: "answer one"},
				{Entry: 3, Conversation: 2, Role: "user", Text: "second root"},
				{Entry: 4, Parent: new(3), Conversation: 2, Role: "assistant", Text: "answer two"},
				{Entry: 5, Parent: new(3), Conversation: 3, Role: "user", Text: "fork"},
				{Entry: 6, Parent: new(99), Conversation: 4, Role: "user", Text: "orphan"},
			}, nil, map[int]string{2: "second"}, func(int) {}, noop, func(int, string) {}),
			focus:  new(false),
			inputs: []string{keyDownArrow, keyDownArrow, "\x14", "a", keyEsc, keyUpArrow},
		}
	})
	registerScene("user-message-selector", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewUserMessageSelector(ctx, userItems(), func(int) {}, noop, new(5)),
			focus:     new(true),
			inputs:    []string{keyDownArrow, keyDownArrow, keyDownArrow, keyDownArrow, keyUpArrow, keyPageUp, keyPageDown, keyUpArrow, keyEnter, keyEsc},
		}
	})
	registerScene("user-message-selector-empty", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewUserMessageSelector(ctx, nil, func(int) {}, noop, nil),
			focus:     new(false), inputs: []string{keyDownArrow, keyEnter},
		}
	})
	registerScene("user-message-selector-single", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewUserMessageSelector(ctx, userItems()[:1], func(int) {}, noop, nil),
			focus:     new(false), inputs: []string{keyDownArrow},
		}
	})
}
