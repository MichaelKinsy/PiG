package history

import (
	"context"
	"fmt"

	searchcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/history/worker.ts: the history tools (tools.go), contributed to every conversation of the session's
// worker; searches go to the server's index through the search plugin's worker feed, which scopes them to this worker's
// session.

// Worker is the history plugin's worker facet.
var Worker = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.history-tools", func(self host.StoreSelf) error {
		worker := p2runtime.WorkerOf(self)
		search := func(ctx context.Context, query Query) (Hits, error) {
			parts, err := worker()
			if err != nil {
				return Hits{}, err
			}
			answered, err := parts.Root(func(root host.Ctx) (any, error) {
				feed, err := root.Get(searchcontract.SearchFeed, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				args := delta.JsonObjectOf("query", query.Query, "conversation", float64(query.Conversation), "limit", float64(query.Limit))
				if query.Before != 0 {
					args.Set("before", float64(query.Before))
				}
				return feed.Call("history", args, root)
			}, ctx)
			if err != nil {
				return Hits{}, err
			}
			return hitsOf(answered)
		}
		label := func(conversation int) string {
			session, err := worker()
			if err != nil {
				return fmt.Sprintf("#%d", conversation)
			}
			id, _ := session.Init.Session()
			return fmt.Sprintf("%s#%d", id.ID, conversation)
		}
		extension, err := CreateHistoryExtension(search, label)
		if err != nil {
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extension.Extension.Name, extension))
		return nil
	})
})

// hitsOf reads the feed's `HistoryHits` answer.
func hitsOf(answered any) (Hits, error) {
	object, _ := answered.(*delta.JsonObject)
	if object == nil {
		return Hits{}, fmt.Errorf("history: no hits object in %v", answered)
	}
	hits := Hits{More: object.Value("more") == true}
	hits.IndexedThrough, _ = object.Value("indexedThrough").(float64)
	list, _ := object.Value("hits").([]any)
	for _, each := range list {
		hit, _ := each.(*delta.JsonObject)
		if hit == nil {
			continue
		}
		entry, _ := hit.Value("entryId").(float64)
		role, _ := hit.Value("role").(string)
		snippet, _ := hit.Value("snippet").(string)
		hits.Hits = append(hits.Hits, Hit{EntryID: entry, Role: role, Snippet: snippet})
	}
	return hits, nil
}
