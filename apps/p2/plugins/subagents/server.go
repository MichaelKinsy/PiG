// Package subagents is p2's subagents plugin: the server's `ChildStore`, `Subagents` and the worker init contribution
// (server.go), the worker's `SubagentControl`, tools and registry reporting (worker.go, tools.go).
package subagents

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"

	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	_ "modernc.org/sqlite" // the store's driver
)

// Ports apps/p2/plugins/subagents/server.ts.

func number(value any) float64 { f, _ := value.(float64); return f }

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// placed is a stored child in the current form: an own cwd stored before locations existed is on the session's
// environment (a session could not change machines while it had subagents with their own cwd).
func placed(child *delta.JsonObject, env any) *delta.JsonObject {
	cwd, has := child.Get("cwd")
	if !has {
		return child
	}
	rest := chordjson.NewObject(child.Len())
	for key, value := range child.All() {
		if key != "cwd" {
			rest.Set(key, value)
		}
	}
	if child.Value("location") != nil || cwd == nil {
		return rest
	}
	rest.Set("location", delta.JsonObjectOf("cwd", cwd, "env", env))
	return rest
}

var serverFacet = p2runtime.DefineFacet(func(facet host.FacetHost, self host.FacetSelf) error {
	if err := facet.Serve(contract.ChildStore, host.LocalFactory(serveChildStore), host.ServeOptions{}); err != nil {
		return err
	}
	if err := facet.Serve(contract.Subagents, host.RemoteFactory(serveSubagents), host.ServeOptions{}); err != nil {
		return err
	}
	// A worker places own cwds stored before locations existed on the environment recorded here.
	return p2runtime.Contribute(facet, "p2.subagents-init", func(self host.StoreSelf) error {
		store := self.Get(contract.ChildStore, delta.NewJsonObject(0))
		p2runtime.ContributeKeyed(self, p2runtime.WorkerInitValues, p2runtime.Entry("subagentLocations", p2runtime.WorkerInitContribution(
			func(session string, _ context.Context) (delta.JsonValue, error) {
				value, err := store.Member("children").Value()
				if err != nil {
					return nil, err
				}
				locations := delta.NewJsonObject(0)
				children, _ := object(value).Value(session).([]any)
				for _, each := range children {
					child := object(each)
					if location := child.Value("location"); location != nil {
						locations.Set(strconv.FormatFloat(number(child.Value("conversationId")), 'f', -1, 64), location)
					}
				}
				return locations, nil
			})))
		return nil
	})
})

func serveChildStore(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
	hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
	catalog := self.Get(sessionscontract.Catalog, delta.NewJsonObject(0))
	children := self.Writable(delta.JsonValue(delta.NewJsonObject(0)))
	opened := self.Open(
		func(ctx context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			dataDir, _ := object(info).Value("dataDir").(string)
			path := filepath.Join(dataDir, "subagents.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				return nil, err
			}
			db.SetMaxOpenConns(1)
			if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; CREATE TABLE IF NOT EXISTS children(session TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;"); err != nil {
				_ = db.Close()
				return nil, err
			}
			if err := os.Chmod(path, 0o600); err != nil {
				_ = db.Close()
				return nil, err
			}
			// Every session's children, read when the database opens: rows stored with an own cwd (version 1) are
			// placed, and saved so, at that first read.
			sessions, err := catalog.Member("sessions").Value()
			if err != nil {
				_ = db.Close()
				return nil, err
			}
			envs := map[string]any{}
			rows, _ := sessions.([]any)
			for _, row := range rows {
				envs[object(row).Value("id").(string)] = object(row).Value("env")
			}
			read := delta.NewJsonObject(0)
			stored, err := db.QueryContext(ctx, "SELECT session,value FROM children")
			if err != nil {
				_ = db.Close()
				return nil, err
			}
			type update struct{ session, value string }
			var updates []update
			for stored.Next() {
				var session, value string
				if err := stored.Scan(&session, &value); err != nil {
					_ = stored.Close()
					_ = db.Close()
					return nil, err
				}
				var decoded any
				if decoded, err = chordjson.Decode([]byte(value)); err != nil {
					_ = stored.Close()
					_ = db.Close()
					return nil, err
				}
				list, _ := decoded.([]any)
				converted := make([]any, len(list))
				migrate := false
				for i, each := range list {
					child := object(each)
					migrate = migrate || child.Has("cwd")
					converted[i] = placed(child, envs[session])
				}
				read.Set(session, converted)
				if migrate {
					encoded, _ := chordjson.Marshal(converted)
					updates = append(updates, update{session, string(encoded)})
				}
			}
			if err := stored.Err(); err != nil {
				_ = stored.Close()
				_ = db.Close()
				return nil, err
			}
			if err := stored.Close(); err != nil {
				_ = db.Close()
				return nil, err
			}
			for _, each := range updates {
				if _, err := db.ExecContext(ctx, "UPDATE children SET value=? WHERE session=?", each.value, each.session); err != nil {
					_ = db.Close()
					return nil, err
				}
			}
			if err := children.Set(read); err != nil {
				_ = db.Close()
				return nil, err
			}
			return db, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error { return value.(*sql.DB).Close() },
	)
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"children": self.Derive([]any{children}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
		},
		Methods: map[string]contracts.MethodFunction{
			"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				in := object(args)
				session, _ := in.Value("session").(string)
				workers, err := hosts.Member("workers").Value()
				if err != nil {
					return nil, err
				}
				worker := object(object(workers).Value(session))
				if worker == nil || worker.Value("running") != true || worker.Value("generation") != in.Value("generation") {
					return nil, nil
				}
				db, err := opened.Value()
				if err != nil {
					return nil, err
				}
				encoded, err := chordjson.Marshal(in.Value("children"))
				if err != nil {
					return nil, err
				}
				if _, err := db.(*sql.DB).ExecContext(ctx,
					"INSERT INTO children(session,value) VALUES(?,?) ON CONFLICT(session) DO UPDATE SET value=excluded.value", session, string(encoded)); err != nil {
					return nil, err
				}
				current, _ := children.Value().(*delta.JsonObject)
				next := current.Clone()
				next.Set(session, in.Value("children"))
				return nil, children.Set(next)
			},
		},
	}}, nil
}

// serveSubagents serves `Subagents`: a worker reports its own session's children, the owner reads every session's, a guest
// those of the sessions shared with them.
func serveSubagents(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil {
		return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
	}
	store := self.Get(contract.ChildStore, delta.NewJsonObject(0))
	switch typed := who.(type) {
	case p2runtime.WorkerPrincipal:
		return contracts.Implementation{Variant: "reporter", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return store.Call("report", delta.JsonObjectOf("session", typed.Session, "generation", float64(typed.Generation), "children", object(args).Value("children")), ctx)
			},
		}}}, nil
	case p2runtime.GuestPrincipal:
		children := self.Derive([]any{store.Member("children")}, func(values []any) (delta.JsonValue, error) {
			all := object(values[0])
			visible := delta.NewJsonObject(len(typed.Sessions))
			for session := range typed.Sessions {
				if list := all.Value(session); list != nil {
					visible.Set(session, list)
				} else {
					visible.Set(session, []any{})
				}
			}
			return visible, nil
		})
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"children": children},
		}}, nil
	case p2runtime.OwnerPrincipal:
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"children": self.Derive([]any{store.Member("children")}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
		}}, nil
	}
	return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
}
