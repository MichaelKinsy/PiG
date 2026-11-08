package sessions

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// rowsJSON is the catalog's sessions as the `sessions` member carries them.
func rowsJSON(sessions []contract.Session) []any {
	rows := make([]any, len(sessions))
	for i, session := range sessions {
		rows[i] = session.JSON()
	}
	return rows
}

func modelRefOf(value any) durable.ModelRef {
	model, _ := value.(*delta.JsonObject)
	if model == nil {
		return durable.ModelRef{}
	}
	return durable.ModelRef{Provider: stringOf(model.Value("provider")), ModelId: stringOf(model.Value("modelId"))}
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

// optionalString is a string, or nil for null or absent.
func optionalString(value any) *string {
	if text, ok := value.(string); ok {
		return &text
	}
	return nil
}

// changeOf is what `Catalog.update` takes, as the catalog changes it.
func changeOf(args *delta.JsonObject) Change {
	var change Change
	if title, ok := args.Value("title").(string); ok {
		change.Title = &title
	}
	if cwd, ok := args.Value("cwd").(string); ok {
		change.Cwd = &cwd
	}
	if args.Has("env") {
		change.SetEnv, change.Env = true, optionalString(args.Value("env"))
	}
	if pinned, ok := args.Value("pinned").(bool); ok {
		change.Pinned = &pinned
	}
	if archived, ok := args.Value("archived").(bool); ok {
		change.Archived = &archived
	}
	if args.Value("model") != nil {
		model := modelRefOf(args.Value("model"))
		change.Model = &model
	}
	if level, ok := args.Value("thinkingLevel").(string); ok {
		change.Thinking = &level
	}
	if main, ok := args.Value("main").(float64); ok {
		value := int(main)
		change.Main = &value
	}
	if list, ok := args.Value("branches").([]any); ok {
		branches := make([]contract.Branch, len(list))
		for i, each := range list {
			branch, _ := each.(*delta.JsonObject)
			branches[i] = contract.Branch{Conversation: int(numberOf(branch.Value("conversation"))), Label: optionalString(branch.Value("label"))}
		}
		change.Branches = &branches
	}
	return change
}

func numberOf(value any) float64 {
	number, _ := value.(float64)
	return number
}

// serveCatalog serves Catalog: the sessions' rows. Its database is this object's native, and every write sets the rows
// in the same method, before it answers.
func serveCatalog(facet host.FacetHost) error {
	return facet.Serve(contract.Catalog, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		sessions := self.Writable([]any{})
		db := self.Open(func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			opened, err := OpenCatalog(ctx, stringOf(info.(*delta.JsonObject).Value("dataDir")))
			if err != nil {
				return nil, err
			}
			all, err := opened.All(ctx)
			if err != nil {
				_ = opened.Close()
				return nil, err
			}
			if err := sessions.Set(rowsJSON(all)); err != nil {
				_ = opened.Close()
				return nil, err
			}
			return opened, nil
		}, func(value any, _ host.NativeCloseReason, _ context.Context) error {
			return value.(*CatalogDB).Close()
		})
		opened := func() (*CatalogDB, error) {
			value, err := db.Value()
			if err != nil {
				return nil, err
			}
			return value.(*CatalogDB), nil
		}
		// publish sets the rows after a write.
		publish := func(ctx context.Context, catalog *CatalogDB) error {
			all, err := catalog.All(ctx)
			if err != nil {
				return err
			}
			return sessions.Set(rowsJSON(all))
		}
		list := self.Derive([]any{sessions}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"sessions": list},
			Methods: map[string]contracts.MethodFunction{
				"get": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					catalog, err := opened()
					if err != nil {
						return nil, err
					}
					session, err := catalog.Get(ctx, stringOf(objectArgs(args).Value("id")))
					return session.JSON(), err
				},
				"create": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					catalog, err := opened()
					if err != nil {
						return nil, err
					}
					in := objectArgs(args)
					session, err := catalog.Create(ctx, NewSession{
						ID: stringOf(in.Value("id")), Env: optionalString(in.Value("env")), Cwd: stringOf(in.Value("cwd")),
						Title: stringOf(in.Value("title")), TitleEdited: in.Value("titleEdited") == true,
						Model: modelRefOf(in.Value("model")), ThinkingLevel: stringOf(in.Value("thinkingLevel")),
					})
					if err != nil {
						return nil, err
					}
					return session.JSON(), publish(ctx, catalog)
				},
				"activity": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					catalog, err := opened()
					if err != nil {
						return nil, err
					}
					in := objectArgs(args)
					if err := catalog.Activity(ctx, stringOf(in.Value("id")), int64(numberOf(in.Value("timestamp"))), stringOf(in.Value("firstUserText"))); err != nil {
						return nil, err
					}
					return nil, publish(ctx, catalog)
				},
				"update": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					catalog, err := opened()
					if err != nil {
						return nil, err
					}
					in := objectArgs(args)
					session, err := catalog.Update(ctx, stringOf(in.Value("id")), changeOf(in))
					if err != nil {
						return nil, err
					}
					return session.JSON(), publish(ctx, catalog)
				},
			},
		}}, nil
	}), host.ServeOptions{Accepts: catalogAccepts()})
}

// serveArchivedStarts serves ArchivedStarts: the archived sessions whose worker may start anyway.
func serveArchivedStarts(facet host.FacetHost) error {
	return facet.Serve(contract.ArchivedStarts, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		allowed := self.Writable([]any{})
		current := func() []any { value, _ := allowed.Value().([]any); return value }
		list := self.Derive([]any{allowed}, func(values []any) (delta.JsonValue, error) { return values[0], nil })
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"allowed": list},
			Methods: map[string]contracts.MethodFunction{
				"allow": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					session := stringOf(objectArgs(args).Value("session"))
					for _, each := range current() {
						if each == session {
							return nil, nil
						}
					}
					return nil, allowed.Set(append(append([]any{}, current()...), session))
				},
				"end": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					session := stringOf(objectArgs(args).Value("session"))
					kept := []any{}
					for _, each := range current() {
						if each != session {
							kept = append(kept, each)
						}
					}
					return nil, allowed.Set(kept)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}
