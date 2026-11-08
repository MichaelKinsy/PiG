package clienttest

// test/p2/completion.test.ts "in the chat editor, @ completes from the session's FileIndex and inserts the path".

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	fileindex "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

func TestInTheChatEditorCompletesFromTheSessionsFileIndexAndInsertsThePath(t *testing.T) {
	chat := tuitest.NewChatStub(t, tuitest.ChatStubOptions{
		Views: func(int) delta.JsonValue { return tuitest.View(t, 1, nil, harness.ViewDocs{}, 0) },
	})
	index := p2test.Stub{Contract: p2runtime.FileIndex, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"changes": self.Cell(float64(0))}, Methods: map[string]p2test.StubMethod{
			"complete": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				query, _ := args.(*delta.JsonObject).Value("query").(string)
				var items []any
				for _, item := range fileindex.RankFiles(fileindex.WithDirectories([]string{"src/parser.ts", "docs/guide.md"}), query, 20, fileindex.KindAny) {
					items = append(items, delta.JsonObjectOf("path", item.Path, "dir", item.Dir))
				}
				return delta.JsonObjectOf("items", items, "truncated", false), nil
			},
			"list": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
				return delta.JsonObjectOf("entries", []any{}, "truncated", false), nil
			},
		}}
	}}
	s := tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: []p2test.Stub{tuitest.SessionsStub(tuitest.Session)}, Worker: []p2test.Stub{chat.Stub, tuitest.DraftsStub(), index},
		Facets: []host.Facet{tui.CoreFacet, chattui.Facet}, Columns: 100,
	})
	s.Do(func() { s.Runtime.Navigate("chat", "s", true, nil) })
	pollScreen(t, s.Fixture, func(screen string) bool { return !matches(`Connecting conversation`)(screen) })
	typed(s.Fixture, "fix @pars")
	pollContains(t, s.Fixture, "src/parser.ts")
	s.Send(tab)
	tuitest.Poll(t, func() bool {
		var draft string
		s.Do(func() { draft = s.Runtime.Drafts["s#1"] })
		return draft == "fix @src/parser.ts "
	})
}
