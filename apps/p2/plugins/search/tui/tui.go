// Package tui is the session selector's message search in the TUI (p2-tui-port.md section 1.17): `Search` is literal and bounded, and the owner's; the selector
// shows its hits as a separate group. `/reindex` rebuilds the viewed session's index.
//
// Ports apps/p2/plugins/search/tui.ts.
package tui

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

type object interface{ Value(string) any }

func number(value any) int {
	n, _ := value.(float64)
	return int(n)
}

func text(value any) string {
	s, _ := value.(string)
	return s
}

// searchSource searches the sessions' messages (a blocking call: the selector runs it off the owner loop).
func searchSource(rt *runtime.TuiRuntime, query string, archived bool) ([]runtime.SessionHit, error) {
	ref, err := rt.Host.Get(context.Background(), contract.Search, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	if variant, _, _ := ref.Variant(); variant != "owner" {
		return nil, nil
	}
	found, err := ref.Call("search", delta.JsonObjectOf("query", query, "archived", archived), context.Background())
	if err != nil {
		return nil, err
	}
	results, _ := found.(object)
	var hits []runtime.SessionHit
	messages, _ := results.Value("messages").([]any)
	for _, each := range messages {
		hit, ok := each.(object)
		if !ok {
			continue
		}
		hits = append(hits, runtime.SessionHit{
			Session: text(hit.Value("session")), ConversationID: number(hit.Value("conversationId")),
			Role: text(hit.Value("role")), Text: text(hit.Value("text")), Title: text(hit.Value("title")),
		})
	}
	return hits, nil
}

// reindex is `/reindex`: re-indexes the viewed session's history (`Search.rebuildIndex`). Long work (a job on the server): a line says it runs at once, another
// when it is done or why it failed (`Search.rebuilds`).
func reindex(ctx runtime.TuiContext) error {
	session := ctx.Identity().Session
	if session == "" {
		return nil
	}
	ref, err := ctx.Host().Hold(context.Background(), contract.Search, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		return err
	}
	started := false
	finished := false
	var stop func()
	finish := func() {
		if finished {
			return
		}
		finished = true
		if stop != nil {
			stop()
		}
		ref.Release()
	}
	if rebuilds := ref.Member("rebuilds"); rebuilds != nil {
		stop = p2tui.FollowMember(rebuilds, func(value delta.JsonValue) {
			ctx.Post(func() {
				if finished || !started {
					return
				}
				list, _ := value.([]any)
				var rebuild object
				for _, each := range list {
					if entry, ok := each.(object); ok && text(entry.Value("session")) == session {
						rebuild = entry
						break
					}
				}
				if rebuild != nil && text(rebuild.Value("phase")) == "running" {
					return
				}
				if rebuild != nil && text(rebuild.Value("phase")) == "failed" {
					ctx.Notify("Search index rebuild failed: "+render.LineText(text(rebuild.Value("error"))), runtime.NoticeError)
				} else {
					ctx.Notify("Search index rebuilt", runtime.NoticeInfo)
				}
				finish()
			})
		})
	}
	ctx.Notify("Rebuilding the search index…", runtime.NoticeInfo)
	go func() {
		err := p2runtime.WhenLive(context.Background(), ref)
		if err == nil {
			if variant, _, _ := ref.Variant(); variant != "owner" {
				err = errOnlyOwner
			}
		}
		if err == nil {
			_, err = ref.Call("rebuildIndex", delta.JsonObjectOf("session", session), context.Background())
		}
		ctx.Post(func() {
			if err != nil {
				ctx.Fail(err)
				finish()
				return
			}
			started = true
		})
	}()
	return nil
}

type onlyOwner struct{}

func (onlyOwner) Error() string { return "Only the owner rebuilds the search index" }

var errOnlyOwner error = onlyOwner{}

// Facet is the plugin's TUI facet.
var Facet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-search", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiSessionSources, p2runtime.Entry("search", runtime.TuiSessionSource{Search: searchSource}))
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, p2runtime.Entry("p2.search.reindex", runtime.TuiCommand{
			Title:    "Rebuild the session's search index",
			Category: "Owner",
			When:     "chat && owner && !overlay",
			Slash:    &runtime.TuiCommandSlash{Name: "reindex"},
			Run:      func(ctx runtime.TuiContext, _ string) error { return reindex(ctx) },
		}))
		return nil
	})
})
