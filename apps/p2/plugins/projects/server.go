package projects

import (
	"context"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/projects/server.ts: serves `projects.json` and the sessions' projects to the owner: one object
// for the server (`ProjectService`: the file's observer and the persisted memberships), `Projects` per owner with this
// device's label, the server's hostname, and a copy of a session (`/clone`, `/fork`) joining its source's project.

// memberships is the sessions' projects by session ID, in the server's store (`<data>/server/store.sqlite`), not the
// session catalog: without this plugin the catalog stays as it is. Entries of archived sessions stay (a restore finds
// its project).
var memberships = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.project-sessions", documents.StateOptions{
		Schema:  p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("sessions", p2runtime.Record(schema.Str()))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("sessions", delta.NewJsonObject(0)) },
		Persist: true,
		CheckpointWhen: func(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 20
		},
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	if object == nil {
		return delta.NewJsonObject(0)
	}
	return object
}

// projectsValue is the projects as the contract's JSON.
func projectsValue(projects []contract.Project) []any {
	out := make([]any, 0, len(projects))
	for _, project := range projects {
		places := make([]any, 0, len(project.Places))
		for _, place := range project.Places {
			var env any
			if place.Env != nil {
				env = *place.Env
			}
			places = append(places, delta.JsonObjectOf("env", env, "path", place.Path))
		}
		out = append(out, delta.JsonObjectOf("name", project.Name, "places", places))
	}
	return out
}

func serveProjectService(facet host.FacetHost) error {
	return facet.Serve(contract.ProjectService, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		doc := self.State(memberships).(*documents.Singleton)
		opened := self.Open(
			func(ctx context.Context) (any, error) {
				info, err := server.Member("info").Value()
				if err != nil {
					return nil, err
				}
				dataDir, _ := objectOf(info).Value("dataDir").(string)
				store := NewProjectStore(dataDir)
				if err := store.Start(ctx); err != nil {
					store.Close()
					return nil, err
				}
				return store, nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error {
				value.(*ProjectStore).Close()
				return nil
			},
		)
		store := func() *ProjectStore {
			value, err := opened.Value()
			if err != nil {
				panic(err)
			}
			return value.(*ProjectStore)
		}
		snapshot := p2runtime.FollowNative(self,
			func(changed func()) func() { return store().Subscribe(func(ProjectSnapshot) { changed() }) },
			func() (delta.JsonValue, error) {
				read := store().Snapshot()
				var failure any
				if read.Error != "" {
					failure = read.Error
				}
				return delta.JsonObjectOf("projects", projectsValue(read.Projects), "failure", failure), nil
			},
		)
		member := func(key string) chord.Watchable[delta.JsonValue] {
			return self.Derive([]any{snapshot}, func(values []any) (delta.JsonValue, error) {
				return objectOf(values[0]).Value(key), nil
			})
		}
		set := func(ctx context.Context, session, project string) error {
			_, err := doc.Change(ctx, func(draft *delta.Object) (any, error) {
				return nil, draft.Object("sessions").Set(session, project)
			})
			return err
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"projects": member("projects"),
				"failure":  member("failure"),
				"sessions": self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) {
					return objectOf(values[0]).Value("sessions"), nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"assign": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					session, _ := in.Value("session").(string)
					project, _ := in.Value("project").(string)
					found := false
					for _, each := range store().Snapshot().Projects {
						found = found || each.Name == project
					}
					if !found {
						return nil, fmt.Errorf(`No project "%s" in projects.json`, project)
					}
					return nil, set(ctx, session, project)
				},
				"copy": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					source, _ := in.Value("source").(string)
					copied, _ := in.Value("copy").(string)
					current, err := doc.Value()
					if err != nil {
						return nil, err
					}
					if project, ok := objectOf(objectOf(current).Value("sessions")).Value(source).(string); ok {
						return nil, set(ctx, copied, project)
					}
					return nil, nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveProjects(facet host.FacetHost) error {
	return facet.Serve(contract.Projects, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("projects are owner-only")
		}
		service := self.Get(contract.ProjectService, delta.NewJsonObject(0))
		label, _ := os.Hostname()
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"projects": host.Expose(service.Member("projects")),
				"failure":  host.Expose(service.Member("failure")),
				// A constant: a derive of no sources.
				"local":    self.Derive([]any{}, func([]any) (delta.JsonValue, error) { return label, nil }),
				"sessions": host.Expose(service.Member("sessions")),
			},
			Methods: map[string]contracts.MethodFunction{
				"assign": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return service.Call("assign", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// Server is the projects plugin's server facet.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := serveProjectService(facet); err != nil {
		return err
	}
	if err := serveProjects(facet); err != nil {
		return err
	}
	// A copy keeps its source's project; the copy goes on if that cannot be recorded.
	return p2runtime.Contribute(facet, "p2.projects-copied", func(self host.StoreSelf) error {
		service := self.Get(contract.ProjectService, delta.NewJsonObject(0))
		p2runtime.ContributeKeyed(self, sessionscontract.SessionCopied, p2runtime.Entry("projects",
			func(ctx context.Context, source, copied string) error {
				if _, err := service.Call("copy", delta.JsonObjectOf("source", source, "copy", copied), ctx); err != nil {
					fmt.Fprintf(os.Stderr, "p2 projects: session %s kept no project: %v\n", copied, err)
				}
				return nil
			}))
		return nil
	})
})
