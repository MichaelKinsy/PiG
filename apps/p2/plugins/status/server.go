// Package status ports apps/p2/plugins/status (server and worker parts): every session's live status, reported by its
// worker and indexed on the server, and the owner's retry of a failed worker.
package status

import (
	"cmp"
	"context"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// reportsState is the latest report of each session's worker, with the generation that sent it (in memory). 2: `work`.
var reportsState = func() *documents.StateDefinition {
	definition, err := documents.DefineState("p2.status-reports", documents.StateOptions{
		Schema: p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("reports", p2runtime.Record(schema.Obj(
			append(schema.Properties{schema.Prop("generation", schema.Num())}, contract.ReportFields()...))))})),
		Version: 2,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("reports", delta.NewJsonObject(0)) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

// statusesOf is what each session's status is: whether a worker runs comes from the workers component, what it does
// from its own generation's latest report.
func statusesOf(workers, reports *delta.JsonObject) *delta.JsonObject {
	statuses := delta.NewJsonObject(workers.Len())
	for session, value := range workers.All() {
		worker := objectOf(value)
		if running, _ := worker.Value("running").(bool); !running {
			status := delta.JsonObjectOf("running", false, "tasks", float64(0), "busy", []any{}, "work", delta.NewJsonObject(0))
			if message, ok := worker.Value("error").(string); ok && message != "" {
				status.Set("error", message)
			}
			statuses.Set(session, status)
			continue
		}
		report := objectOf(reports.Value(session))
		if report == nil || report.Value("generation") != worker.Value("generation") {
			statuses.Set(session, delta.JsonObjectOf("running", true, "tasks", float64(0), "busy", []any{}, "work", delta.NewJsonObject(0)))
			continue
		}
		status := delta.JsonObjectOf("running", true)
		for key, field := range report.All() {
			if key != "generation" {
				status.Set(key, field)
			}
		}
		statuses.Set(session, status)
	}
	return statuses
}

// reportOf is the work a report carries, in the order the contract has it.
func reportOf(args *delta.JsonObject, generation float64) *delta.JsonObject {
	busy, _ := args.Value("busy").([]any)
	sorted := slices.Clone(busy)
	slices.SortFunc(sorted, func(a, b any) int {
		left, _ := a.(float64)
		right, _ := b.(float64)
		return cmp.Compare(left, right)
	})
	report := delta.JsonObjectOf("generation", generation, "tasks", args.Value("tasks"), "busy", sorted, "work", args.Value("work"))
	if usage := args.Value("usage"); usage != nil {
		report.Set("usage", usage)
	}
	return report
}

// serveStatusStore serves StatusStore.
func serveStatusStore(facet host.FacetHost) error {
	return facet.Serve(contract.StatusStore, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		reports := self.State(reportsState).(*documents.Singleton)
		hosts := self.Get(p2runtime.Hosts, delta.NewJsonObject(0))
		workersMember := hosts.Member("workers")
		statuses := self.Derive([]any{workersMember, reports}, func(values []any) (delta.JsonValue, error) {
			workers, reportsValue := objectOf(values[0]), objectOf(values[1])
			if workers == nil {
				workers = delta.NewJsonObject(0)
			}
			return statusesOf(workers, objectOf(reportsValue.Value("reports"))), nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"statuses": statuses},
			Methods: map[string]contracts.MethodFunction{
				"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					session, _ := in.Value("session").(string)
					generation, _ := in.Value("generation").(float64)
					// A report of an older worker (one that exited while its report travelled) is dropped.
					current, err := workersMember.Value()
					if err != nil {
						return nil, err
					}
					worker := objectOf(objectArgs(current).Value(session))
					if worker == nil || worker.Value("running") != true || worker.Value("generation") != generation {
						return nil, nil
					}
					_, err = reports.Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Object("reports").Set(session, reportOf(in, generation))
					})
					return nil, err
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func objectArgs(args delta.JsonValue) *delta.JsonObject {
	if object := objectOf(args); object != nil {
		return object
	}
	return delta.NewJsonObject(0)
}

// serveWorkerControl serves WorkerControl: the owner's retry of a failed session worker.
func serveWorkerControl(facet host.FacetHost) error {
	return facet.Serve(contract.WorkerControl, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("Only owners may retry session workers")
		}
		session, _ := params.Value("session").(string)
		server := p2runtime.ServerOf(self)
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			Methods: map[string]contracts.MethodFunction{
				// Answers with the start's outcome: the worker runs, or the call fails with why it could not start.
				"restart": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					parts, err := server()
					if err != nil {
						return nil, err
					}
					outcome, err := parts.Workers.Retry(ctx, session)
					if err != nil {
						return nil, err
					}
					if outcome.Error != nil {
						return nil, data.NewPluginError("worker_failed", *outcome.Error)
					}
					return nil, nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// serveSessionMetrics serves SessionMetrics; who looks at a session is one entry per reference (its params name the
// session and the viewer).
func serveSessionMetrics(facet host.FacetHost) error {
	metrics := facet.References(contract.SessionMetrics)
	return facet.Serve(contract.SessionMetrics, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, _ := params.Value("session").(string)
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		store := self.Get(contract.StatusStore, delta.NewJsonObject(0))
		clients := self.Derive([]any{metrics}, func(values []any) (delta.JsonValue, error) {
			viewers := map[string]struct{}{}
			references, _ := values[0].([]any)
			for _, reference := range references {
				held := objectOf(objectOf(reference).Value("params"))
				if held != nil && held.Value("session") == session {
					viewer, _ := held.Value("viewer").(string)
					viewers[viewer] = struct{}{}
				}
			}
			return float64(len(viewers)), nil
		})
		usage := self.Derive([]any{store.Member("statuses")}, func(values []any) (delta.JsonValue, error) {
			if status := objectOf(objectOf(values[0]).Value(session)); status != nil {
				if usage := status.Value("usage"); usage != nil {
					return usage, nil
				}
			}
			return nil, nil
		})
		return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"clients": clients, "usage": usage},
		}}, nil
	}), host.ServeOptions{})
}

// statusAccepts: `SessionStatus` 1: pages read statuses without `work`; a worker built then reports none (its
// conversations show as busy without a step).
func statusAccepts() contracts.AcceptsTable {
	withoutWork := func(value delta.JsonValue) (delta.JsonValue, error) {
		statuses := objectOf(value)
		if statuses == nil {
			return value, nil
		}
		out := delta.NewJsonObject(statuses.Len())
		for session, each := range statuses.All() {
			status := objectOf(each)
			if status == nil {
				out.Set(session, each)
				continue
			}
			kept := delta.NewJsonObject(status.Len())
			for key, field := range status.All() {
				if key != "work" {
					kept.Set(key, field)
				}
			}
			out.Set(session, kept)
		}
		return out, nil
	}
	withEmptyWork := func(value delta.JsonValue) (delta.JsonValue, error) {
		report := objectArgs(value).Clone()
		report.Set("work", delta.NewJsonObject(0))
		return report, nil
	}
	return contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
		VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "reader", Value: []contracts.Entry[contracts.StateMapping]{
			{Name: "statuses", Value: contracts.StateMapping{Func: withoutWork}},
		}}},
		Methods: []contracts.Entry[contracts.MethodMapping]{{Name: "report", Value: contracts.MethodMapping{Args: withEmptyWork}}},
	}}}
}

// serveSessionStatus serves SessionStatus: the owner reads every status, a guest its sessions' without the worker's
// error text, a worker reports its own session's.
func serveSessionStatus(facet host.FacetHost) error {
	return facet.Serve(contract.SessionStatus, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
		}
		store := self.Get(contract.StatusStore, delta.NewJsonObject(0))
		switch typed := who.(type) {
		case p2runtime.WorkerPrincipal:
			return contracts.Implementation{Variant: "reporter", Members: contracts.VariantImplementation{
				Methods: map[string]contracts.MethodFunction{
					"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						full := objectArgs(args).Clone()
						full.Set("session", typed.Session)
						full.Set("generation", float64(typed.Generation))
						return store.Call("report", full, ctx)
					},
				},
			}}, nil
		case p2runtime.GuestPrincipal:
			// As in Pi Mobile: whether it runs, its work and spend, never the worker's error text (paths, stacks).
			shared := slices.Sorted(maps.Keys(typed.Sessions))
			statuses := self.Derive([]any{store.Member("statuses")}, func(values []any) (delta.JsonValue, error) {
				all := objectArgs(values[0])
				visible := delta.NewJsonObject(len(shared))
				for _, session := range shared {
					status := objectOf(all.Value(session))
					if status == nil {
						continue
					}
					entry := delta.JsonObjectOf("running", status.Value("running"), "tasks", status.Value("tasks"), "busy", status.Value("busy"), "work", status.Value("work"))
					if usage := status.Value("usage"); usage != nil {
						entry.Set("usage", usage)
					}
					visible.Set(session, entry)
				}
				return visible, nil
			})
			return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"statuses": statuses},
			}}, nil
		case p2runtime.OwnerPrincipal:
			return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"statuses": host.Expose(store.Member("statuses"))},
			}}, nil
		}
		return contracts.Implementation{}, p2runtime.Forbidden("owners and guests only")
	}), host.ServeOptions{Accepts: statusAccepts()})
}

// Server is the status plugin's server facet.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveSessionMetrics, serveStatusStore, serveWorkerControl, serveSessionStatus} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
