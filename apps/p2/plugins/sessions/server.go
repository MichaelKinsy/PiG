package sessions

import (
	"context"
	"errors"
	"fmt"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"os"
	"path/filepath"
	"unicode/utf16"

	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/google/uuid"
)

// Ports apps/p2/plugins/sessions/server.ts: the session catalog and its views (server): the sidebar's rows, creating and
// renaming sessions, and the lifecycle operations (relocate, clone, fork, archive: operations.go), each a method that
// answers once its visible effect committed and a job for its long part, shown in `Sessions.operations` while it runs
// and when it failed.

// newTitleLimit is the length of a title given at creation (Pi Mobile's).
const newTitleLimit = 100

// activeRow derives from the catalog's rows the first unarchived session's `field`, or fallback when there is none.
func activeRow(self host.Self, catalog *host.Reference, field string, fallback any) chord.Watchable[delta.JsonValue] {
	return self.Derive([]any{catalog.Member("sessions")}, func(values []any) (delta.JsonValue, error) {
		rows, _ := values[0].([]any)
		for _, row := range rows {
			if session, _ := row.(*delta.JsonObject); session != nil && session.Value("archivedAt") == nil {
				if value := session.Value(field); value != nil {
					return value, nil
				}
				return fallback, nil
			}
		}
		return fallback, nil
	})
}

// serveSessions serves Sessions: the owner's, a guest's and a worker's view.
func serveSessions(facet host.FacetHost) error {
	return facet.Serve(contract.Sessions, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("sessions are owner-only")
		}
		catalog := self.Get(contract.Catalog, delta.NewJsonObject(0))
		switch typed := who.(type) {
		case p2runtime.WorkerPrincipal:
			id := typed.Session
			session := self.Derive([]any{catalog.Member("sessions")}, func(values []any) (delta.JsonValue, error) {
				rows, _ := values[0].([]any)
				for _, row := range rows {
					if contract.SessionFromJSON(row).ID == id {
						return row, nil
					}
				}
				return nil, nil
			})
			return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"session": session},
				Methods: map[string]contracts.MethodFunction{
					"renameSelf": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						row, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx)
						if err != nil {
							return nil, err
						}
						previous := contract.SessionFromJSON(row).Title
						if _, err := catalog.Call("update", delta.JsonObjectOf("id", id, "title", cleanTitle(stringOf(objectArgs(args).Value("title")))), ctx); err != nil {
							return nil, err
						}
						return delta.JsonObjectOf("previous", previous), nil
					},
					// Either field alone updates the entry; the other keeps its value (Pi Mobile's configure).
					"configureSelf": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						in := objectArgs(args)
						update := delta.JsonObjectOf("id", id)
						if model := in.Value("model"); model != nil {
							update.Set("model", model)
						}
						if level := stringOf(in.Value("thinkingLevel")); level != "" {
							update.Set("thinkingLevel", level)
						}
						_, err := catalog.Call("update", update, ctx)
						return nil, err
					},
					"treeSelf": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						in := objectArgs(args)
						row, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx)
						if err != nil {
							return nil, err
						}
						current := objectArgs(row)
						if !sameJSON(current.Value("main"), in.Value("main")) || !sameJSON(current.Value("branches"), in.Value("branches")) {
							_, err = catalog.Call("update", delta.JsonObjectOf("id", id, "main", in.Value("main"), "branches", in.Value("branches")), ctx)
						}
						return nil, err
					},
				},
			}}, nil
		case p2runtime.GuestPrincipal:
			shared := typed.Sessions
			sessions := self.Derive([]any{catalog.Member("sessions")}, func(values []any) (delta.JsonValue, error) {
				rows, _ := values[0].([]any)
				visible := []any{}
				for _, row := range rows {
					session := contract.SessionFromJSON(row)
					if _, ok := shared[session.ID]; ok && session.ArchivedAt == nil {
						visible = append(visible, row)
					}
				}
				return visible, nil
			})
			return contracts.Implementation{Variant: "guest", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"sessions": sessions},
			}}, nil
		case p2runtime.OwnerPrincipal:
			return ownerSessions(self, catalog)
		}
		return contracts.Implementation{}, p2runtime.Forbidden("sessions are owner-only")
	}), host.ServeOptions{Accepts: sessionsAccepts()})
}

// ownerSessions is the owner's view of Sessions.
func ownerSessions(self host.Self, catalog *host.Reference) (contracts.Implementation, error) {
	models := self.Get(modelscontract.ModelConfig, delta.NewJsonObject(0))
	resources := p2runtime.NativeOf[modelscontract.ModelResourcesValue](self.Registry(modelscontract.ModelResources), "The model configuration")
	self.Get(envcontract.EnvService, delta.NewJsonObject(0))
	envs := envcontract.EnvManagersOf(self)
	lifecycle := self.Get(contract.SessionOperations, delta.NewJsonObject(0))
	home, _ := os.UserHomeDir()
	defaultCwd := activeRow(self, catalog, "cwd", home)
	defaultEnv := activeRow(self, catalog, "env", nil)
	defaultModel := self.Derive([]any{models.Member("summary")}, func(values []any) (delta.JsonValue, error) {
		return objectArgs(values[0]).Value("defaultModel"), nil
	})
	relay := func(method string) contracts.MethodFunction {
		return func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			return lifecycle.Call(method, args, ctx)
		}
	}
	return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"sessions":   host.Expose(catalog.Member("sessions")),
			"defaultCwd": defaultCwd, "defaultEnv": defaultEnv,
			"operations": host.Expose(lifecycle.Member("operations")),
		},
		Methods: map[string]contracts.MethodFunction{
			"create": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				id := stringOf(in.Value("id"))
				// A chosen ID already in the catalog is a retry of the same request (its answer was lost).
				if id != "" {
					rows, _ := catalog.Member("sessions").Value()
					list, _ := rows.([]any)
					for _, row := range list {
						if contract.SessionFromJSON(row).ID == id {
							return row, nil
						}
					}
				}
				cwd := stringOf(in.Value("cwd"))
				env := optionalString(in.Value("env"))
				createDirectory := in.Value("createDirectory") == true
				if env != nil && *env != "" {
					parts, err := envs()
					if err != nil {
						return nil, err
					}
					if cwd, err = parts.Manager.WorkingDirectory(ctx, *env, cwd, createDirectory); err != nil {
						return nil, err
					}
				} else {
					cwd = expandHome(cwd)
					if !filepath.IsAbs(cwd) {
						return nil, errors.New("The working directory must be an absolute path")
					}
					if createDirectory {
						if err := nodeerrno.MkdirAll(cwd, 0o777); err != nil {
							return nil, err
						}
					}
					info, err := os.Stat(cwd)
					if err != nil {
						// As Node's fs: `ENOENT: no such file or directory, stat '<path>'`.
						return nil, nodeerrno.FromPathError(err)
					}
					if !info.IsDir() {
						return nil, errors.New("Working directory is not a directory")
					}
				}
				fallback := objectArgs(defaultModel.Value())
				chosen := in.Value("model")
				level := in.Value("thinkingLevel")
				if chosen == nil {
					if defaultModel.Value() == nil {
						return nil, errors.New("Select a model or configure P2_PROVIDER and P2_MODEL")
					}
					chosen = fallback
				}
				if level == nil {
					level = fallback.Value("thinkingLevel")
				}
				model := modelRefOf(chosen)
				// As Pi Mobile and setDefault: a model the configuration does not know never reaches a worker.
				parts, err := resources()
				if err != nil {
					return nil, err
				}
				if parts.Resources().Models().GetModel(model.Provider, model.ModelId) == nil {
					return nil, errors.New("Unknown model")
				}
				title := cleanTitle(stringOf(in.Value("title")))
				title = utf16Slice(title, newTitleLimit)
				edited := title != ""
				if title == "" {
					title = "New session"
				}
				thinking := "high"
				if text, ok := level.(string); ok && p2runtime.IsThinkingLevel(text) {
					thinking = text
				}
				create := delta.NewJsonObject(8)
				if id != "" {
					create.Set("id", id)
				}
				create.Set("cwd", cwd)
				create.Set("env", in.Value("env"))
				create.Set("title", title)
				create.Set("titleEdited", edited)
				create.Set("model", delta.JsonObjectOf("provider", model.Provider, "modelId", model.ModelId))
				create.Set("thinkingLevel", thinking)
				return catalog.Call("create", create, ctx)
			},
			"relocate": relay("relocate"),
			"clone": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				return lifecycle.Call("clone", delta.JsonObjectOf("id", in.Value("id"), "copy", copyID(in)), ctx)
			},
			"fork": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				return lifecycle.Call("fork", delta.JsonObjectOf("id", in.Value("id"), "conversation", in.Value("conversation"),
					"before", in.Value("before"), "copy", copyID(in)), ctx)
			},
			"rename": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				clean := cleanTitle(stringOf(in.Value("title")))
				if clean == "" || utf16Length(clean) > contract.MaxTitle {
					return nil, fmt.Errorf("The title must be 1–%d characters", contract.MaxTitle)
				}
				_, err := catalog.Call("update", delta.JsonObjectOf("id", in.Value("id"), "title", clean), ctx)
				return nil, err
			},
			"pin": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := objectArgs(args)
				_, err := catalog.Call("update", delta.JsonObjectOf("id", in.Value("id"), "pinned", in.Value("pinned")), ctx)
				return nil, err
			},
			"archive":          relay("archive"),
			"restore":          relay("restore"),
			"dismissOperation": relay("dismiss"),
		},
	}}, nil
}

// copyID is the chosen ID of a clone or fork, or a new one.
func copyID(in *delta.JsonObject) string {
	if copy, ok := in.Value("copy").(string); ok && copy != "" {
		return copy
	}
	return uuid.NewString()
}

func utf16Length(text string) int { return len(utf16.Encode([]rune(text))) }

// Server is the sessions plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.SessionArchived.Serve(facet); err != nil {
		return err
	}
	if err := contract.SessionCopied.Serve(facet); err != nil {
		return err
	}
	for _, serve := range []func(host.FacetHost) error{
		serveLocks, serveCatalog, serveArchivedStarts, serveOperations, serveMoves, serveSessions,
	} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
