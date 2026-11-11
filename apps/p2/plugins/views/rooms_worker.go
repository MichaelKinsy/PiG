package views

import (
	"context"
	"fmt"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/views/worker.ts (Pi 682c7b93c): the worker's side of a custom view's room (apps/p2/docs/rooms.md): the views'
// current openings and one opening's state for the server (`ViewOpenings`, `ViewBacking`), and the agent's way to its views' rooms.

// openingsOf is each custom view's current opening.
func openingsOf(state ViewsState) map[string]string {
	openings := map[string]string{}
	for _, view := range state.Views {
		if view.Kind != "custom" {
			continue
		}
		if opening, ok := state.Opened[view.ID]; ok {
			openings[view.ID] = opening
		} else {
			openings[view.ID] = contract.LegacyOpening
		}
	}
	return openings
}

func openingsJSON(state ViewsState) *delta.JsonObject {
	openings := delta.NewJsonObject(0)
	for _, view := range state.Views {
		if opening, ok := openingsOf(state)[view.ID]; ok {
			openings.Set(view.ID, opening)
		}
	}
	return openings
}

func serverOnly(principal data.Principal) error {
	if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
		return p2runtime.Forbidden("server only")
	}
	return nil
}

func invalidParams(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonParams, Message: message}, nil)
}

// followViewDoc is view id's document of family, followed; created when absent if the views document has a view id that admits
// accepts, else the activation fails.
func followViewDoc[T any](self host.Self, harnessOf func() (*p2runtime.HarnessParts, error), family durable.DocTokenOf[T], id string, admits func(ViewsState) bool) chord.Watchable[delta.JsonValue] {
	return self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[T]) (durable.Observation[T], error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		opened, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		views, err := ReadViews(ctx, opened)
		if err != nil {
			return nil, err
		}
		known := false
		for _, view := range views.Views {
			known = known || view.ID == id
		}
		if !known || !admits(views) {
			return nil, invalidParams(fmt.Sprintf("No view %q", id))
		}
		observed, err := durable.ObserveDoc(ctx, opened, family, observer, id, nil)
		if err != nil || observed != nil {
			return observed, err
		}
		if _, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
			_, err := durable.TxDoc[T](tx, family, id, nil)
			return nil, err
		}); err != nil {
			return nil, err
		}
		return durable.ObserveDoc(ctx, opened, family, observer, id, nil)
	}))
}

// serveViewOpenings serves the server's view of the views' current openings.
func serveViewOpenings(facet host.FacetHost) error {
	return facet.Serve(contract.ViewOpenings, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if err := serverOnly(principal); err != nil {
			return contracts.Implementation{}, err
		}
		harnessOf := p2runtime.HarnessOf(self)
		index := viewsState(self, harnessOf)
		openings := self.Derive([]any{index}, func(values []any) (delta.JsonValue, error) {
			state, err := durable.FromJsonValue[ViewsState](values[0])
			if err != nil {
				return nil, err
			}
			return openingsJSON(state), nil
		})
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"openings": openings},
		}}, nil
	}), host.ServeOptions{})
}

// serveViewBacking serves one opening of a custom view to its room: the view's state, and the room's own document.
func serveViewBacking(facet host.FacetHost) error {
	return facet.Serve(contract.ViewBacking, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if err := serverOnly(principal); err != nil {
			return contracts.Implementation{}, err
		}
		id, opening := text(params, "view"), text(params, "opening")
		harnessOf := p2runtime.HarnessOf(self)
		worker := p2runtime.WorkerOf(self)
		current := func(state ViewsState) bool { return openingsOf(state)[id] == opening && openingsOf(state)[id] != "" }
		state := followViewDoc[ViewValue](self, harnessOf, ViewStateDoc, id, current)
		room := followViewDoc[RoomValue](self, harnessOf, ViewRoomDoc, id, current)
		return suspended(contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"current": self.Derive([]any{state}, func(values []any) (delta.JsonValue, error) {
					doc := objectOf(values[0])
					revision, _ := doc.Value("revision").(float64)
					by, _ := doc.Value("by").(string)
					if by == "" {
						by = "other"
					}
					return delta.JsonObjectOf("value", doc.Value("value"), "revision", revision, "by", by), nil
				}),
				"room": self.Derive([]any{room}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("value"), nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					input := objectOf(args)
					revision, _ := input.Value("revision").(float64)
					parts, err := worker()
					if err != nil {
						return nil, err
					}
					harness, err := harnessOf()
					if err != nil {
						return nil, err
					}
					opened, err := harness.Durable()
					if err != nil {
						return nil, err
					}
					var written bool
					err = parts.Liveness.Call(func() error {
						result, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
							_, views, err := draftOf(tx)
							if err != nil {
								return nil, err
							}
							if !current(views) {
								return false, nil
							}
							doc, err := durable.TxDoc[ViewValue](tx, ViewStateDoc, id, nil)
							if err != nil {
								return nil, err
							}
							if seen, _ := doc.Get("revision").(float64); seen != revision {
								return false, nil
							}
							if input.Has("room") {
								roomDoc, err := durable.TxDoc[RoomValue](tx, ViewRoomDoc, id, nil)
								if err != nil {
									return nil, err
								}
								if err := roomDoc.Set("value", input.Value("room")); err != nil {
									return nil, err
								}
							}
							if input.Has("value") {
								if err := SetViewState(doc, input.Value("value"), "room"); err != nil {
									return nil, err
								}
							}
							return true, nil
						})
						written, _ = result.(bool)
						return err
					})
					return written, err
				},
			},
		}}), nil
	}), host.ServeOptions{})
}

// roomsBackend is the agent's way to its views' rooms on the server, in roots of the worker's own (it acts as the worker there).
type roomsBackend struct {
	session func() string
	worker  func() (*p2runtime.WorkerParts, error)
}

func (backend roomsBackend) room(ctx context.Context, view, opening string, fn func(root host.Ctx, room *host.Reference) (delta.JsonValue, error)) (delta.JsonValue, error) {
	parts, err := backend.worker()
	if err != nil {
		return nil, err
	}
	value, err := parts.Root(func(root host.Ctx) (any, error) {
		room, err := root.Get(contract.ViewRoom, delta.JsonObjectOf("session", backend.session(), "view", view, "opening", opening), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return fn(root, room)
	}, ctx)
	if err != nil {
		return nil, err
	}
	result, _ := value.(delta.JsonValue)
	return result, nil
}

// Methods are the methods the view's leader offers.
func (backend roomsBackend) Methods(ctx context.Context, view, opening string) ([]any, error) {
	value, err := backend.room(ctx, view, opening, func(root host.Ctx, room *host.Reference) (delta.JsonValue, error) {
		return room.Call("methods", nil, root)
	})
	list, _ := value.([]any)
	return list, err
}

// Call runs a method in the leader's frame; its result.
func (backend roomsBackend) Call(ctx context.Context, view, opening, method string, args delta.JsonValue) (delta.JsonValue, error) {
	return backend.room(ctx, view, opening, func(root host.Ctx, room *host.Reference) (delta.JsonValue, error) {
		return room.Call("call", delta.JsonObjectOf("method", method, "args", args), root)
	})
}
