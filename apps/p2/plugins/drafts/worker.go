// Package drafts is p2's drafts plugin (the worker facet of apps/p2/plugins/drafts/worker.ts): inputs withdrawn by
// someone else (Stop, a queue edit) wait here for their author, and each is claimed once.
package drafts

import (
	"context"

	contract "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// store is the restored drafts by `<conversation>:<author id>`, and the next draft's ID (in memory, for the worker's
// lifetime).
var store = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.drafts", documents.StateOptions{
		Schema: p2runtime.Contract(schema.Obj(schema.Properties{
			schema.Prop("drafts", contract.DraftsByKeySchema), schema.Prop("next", schema.Num()),
		})),
		Version: 1,
		Initial: func() *delta.JsonObject {
			return delta.JsonObjectOf("drafts", delta.NewJsonObject(0), "next", float64(1))
		},
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// Facet serves `DraftStore` (worker, in memory) and `Drafts` (the caller's own restored drafts).
func Facet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := facet.Serve(contract.DraftStore, host.LocalFactory(serveStore), host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(contract.Drafts, host.RemoteFactory(serveDrafts), host.ServeOptions{})
	})
}

func serveStore(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	state := self.State(store).(documents.SingletonHandle)
	drafts := self.Derive([]any{state}, func(values []any) (delta.JsonValue, error) {
		return values[0].(*delta.JsonObject).Value("drafts"), nil
	})
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"drafts": drafts},
		Methods: map[string]contracts.MethodFunction{
			"add": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return nil, add(state, args, ctx)
			},
			"claim": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return claim(state, args, ctx)
			},
		},
	}}, nil
}

// add keeps `items` (their authors' withdrawn inputs) in the order given, each with the next ID.
func add(state documents.SingletonHandle, args delta.JsonValue, ctx context.Context) error {
	object, _ := args.(*delta.JsonObject)
	conversation, _ := object.Value("conversation").(float64)
	items, _ := object.Value("items").([]any)
	if len(items) == 0 {
		return nil
	}
	_, err := state.Change(ctx, func(draft *delta.Object) (any, error) {
		next, _ := draft.Get("next").(float64)
		if err := draft.Set("next", next+float64(len(items))); err != nil {
			return nil, err
		}
		drafts := draft.Object("drafts")
		for index, each := range items {
			item, _ := each.(*delta.JsonObject)
			author, _ := item.Value("author").(*delta.JsonObject)
			authorID, _ := author.Value("id").(string)
			text, _ := item.Value("text").(string)
			key := contract.DraftKey(int(conversation), authorID)
			kept := []any{}
			if existing := drafts.Array(key); existing != nil {
				kept = existing.Snapshot()
			}
			kept = append(kept, delta.JsonObjectOf("id", next+float64(index), "text", text))
			if err := drafts.Set(key, kept); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// claim removes the drafts `ids` of the author's in the conversation and returns their texts: read and removed in one
// change, so two tabs claiming at once get each draft once.
func claim(state documents.SingletonHandle, args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
	object, _ := args.(*delta.JsonObject)
	conversation, _ := object.Value("conversation").(float64)
	authorID, _ := object.Value("authorId").(string)
	ids, _ := object.Value("ids").([]any)
	wanted := make(map[float64]struct{}, len(ids))
	for _, id := range ids {
		if number, ok := id.(float64); ok {
			wanted[number] = struct{}{}
		}
	}
	key := contract.DraftKey(int(conversation), authorID)
	claimed, err := state.Change(ctx, func(draft *delta.Object) (any, error) {
		drafts := draft.Object("drafts")
		existing := drafts.Array(key)
		texts := []any{}
		if existing == nil {
			return texts, nil
		}
		rest := []any{}
		for _, each := range existing.Snapshot() {
			item, _ := each.(*delta.JsonObject)
			id, _ := item.Value("id").(float64)
			if _, taken := wanted[id]; taken {
				text, _ := item.Value("text").(string)
				texts = append(texts, text)
			} else {
				rest = append(rest, item)
			}
		}
		if len(texts) == 0 {
			return texts, nil
		}
		if len(rest) > 0 {
			return texts, drafts.Set(key, rest)
		}
		drafts.Delete(key)
		return texts, nil
	})
	if err != nil {
		return nil, err
	}
	return delta.JsonObjectOf("texts", claimed), nil
}

func serveDrafts(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	session, _ := params.Value("session").(string)
	conversation, _ := params.Value("conversation").(float64)
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.CanSend(who, session) {
		return contracts.Implementation{}, p2runtime.Forbidden("participants only")
	}
	if !p2runtime.IsOwner(who) && !p2runtime.IsGuest(who) {
		return contracts.Implementation{}, p2runtime.Forbidden("people only")
	}
	authorID := p2runtime.AuthorID(who)
	reference := self.Get(contract.DraftStore, delta.NewJsonObject(0))
	key := contract.DraftKey(int(conversation), authorID)
	mine := self.Derive([]any{reference.Member("drafts")}, func(values []any) (delta.JsonValue, error) {
		drafts, _ := values[0].(*delta.JsonObject)
		if items, ok := drafts.Value(key).([]any); ok {
			return items, nil
		}
		return []any{}, nil
	})
	return contracts.Implementation{Variant: "author", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"drafts": mine},
		Methods: map[string]contracts.MethodFunction{
			"claim": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object, _ := args.(*delta.JsonObject)
				return reference.Call("claim", delta.JsonObjectOf("conversation", conversation, "authorId", authorID, "ids", object.Value("ids")), ctx)
			},
		},
	}}, nil
}
