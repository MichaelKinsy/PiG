package views

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	filescontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/files/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/views/worker.ts: the session's views and annotations as browsers hold them (`Views`, `ViewState`), a
// custom view's HTML for the server's frame route (`ViewSource`), the agent's `view` tool, and the send router that
// adds what the person selected and annotated to their next message.

// maxSelectionData is the most a selection's data may hold, as JSON: the agent reads it with the view.
const maxSelectionData = 16 * 1024

func invalid(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: message}, nil)
}

func text(object *delta.JsonObject, key string) string {
	value, _ := object.Value(key).(string)
	return value
}

// sessionBackend is the tool's and the Views sheet's way to the session's files and the owner's library, in roots of
// the worker's own.
type sessionBackend struct {
	worker func() (*p2runtime.WorkerParts, error)
}

func (backend sessionBackend) root(ctx context.Context, fn func(root host.Ctx) (any, error)) (any, error) {
	parts, err := backend.worker()
	if err != nil {
		return nil, err
	}
	return parts.Root(fn, ctx)
}

// sourceOf is version version of view id's HTML from the session's files.
func sourceOf(files *host.Reference, id string, version int, ctx context.Context) (string, error) {
	found, err := files.Call("get", delta.JsonObjectOf("space", contract.ViewSources, "path", id, "version", float64(version)), ctx)
	if err != nil {
		return "", err
	}
	file, _ := found.(*delta.JsonObject)
	if file == nil {
		return "", errors.New("View not found")
	}
	if size, _ := file.Value("size").(float64); size > contract.MaxViewHTML {
		return "", errors.New("View not found")
	}
	bytes, err := p2runtime.ReadWhole(files, file, ctx)
	return string(bytes), err
}

func (backend sessionBackend) SaveSource(ctx context.Context, id, html string) (int, error) {
	version, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		files, err := root.Get(app.Files, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		file, err := files.Call("put", delta.JsonObjectOf("space", contract.ViewSources, "path", id, "mime", "text/html", "source", "agent", "bytes", []byte(html)), root)
		if err != nil {
			return nil, err
		}
		number, _ := objectOf(file).Value("version").(float64)
		return int(number), nil
	})
	if err != nil {
		return 0, err
	}
	return version.(int), nil
}

func (backend sessionBackend) ReadSource(ctx context.Context, id string, version int) (string, error) {
	html, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		files, err := root.Get(app.Files, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return sourceOf(files, id, version, root)
	})
	if err != nil {
		return "", err
	}
	return html.(string), nil
}

func (backend sessionBackend) ReadArtifact(ctx context.Context, name string) (string, error) {
	html, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		files, err := root.Get(app.Files, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		newest, err := files.Call("get", delta.JsonObjectOf("space", p2runtime.ArtifactsSpace, "path", name), root)
		if err != nil {
			return nil, err
		}
		version, _ := objectOf(newest).Value("version").(float64)
		if newest == nil || objectOf(newest) == nil {
			return nil, fmt.Errorf("No artifact %q", name)
		}
		info, content, err := artifacts.ReadArtifact(files, name, int(version), root)
		if err != nil {
			return nil, err
		}
		if info.Type != p2runtime.ArtifactHTML {
			return nil, fmt.Errorf("Artifact %q is %s, not HTML", name, info.Type)
		}
		return content, nil
	})
	if err != nil {
		return "", err
	}
	return html.(string), nil
}

func (backend sessionBackend) library(root host.Ctx) (*host.Reference, error) {
	return root.Get(contract.SavedViews, delta.NewJsonObject(0), host.GetOptions{})
}

func (backend sessionBackend) ListSaved(ctx context.Context) ([]SavedInfo, error) {
	list, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		library, err := backend.library(root)
		if err != nil {
			return nil, err
		}
		return library.Call("list", nil, root)
	})
	if err != nil {
		return nil, err
	}
	var infos []SavedInfo
	encoded, err := jsonText(list)
	if err != nil {
		return nil, err
	}
	return infos, jsonUnmarshal(encoded, &infos)
}

func (backend sessionBackend) GetSaved(ctx context.Context, name string) (*SavedView, error) {
	found, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		library, err := backend.library(root)
		if err != nil {
			return nil, err
		}
		return library.Call("get", delta.JsonObjectOf("name", name), root)
	})
	if err != nil || found == nil {
		return nil, err
	}
	view, err := savedOf(found)
	return &view, err
}

func (backend sessionBackend) Save(ctx context.Context, view SavedView) (SavedInfo, error) {
	encoded, err := savedJSON(view)
	if err != nil {
		return SavedInfo{}, err
	}
	saved, err := backend.root(ctx, func(root host.Ctx) (any, error) {
		library, err := backend.library(root)
		if err != nil {
			return nil, err
		}
		return library.Call("save", encoded, root)
	})
	if err != nil {
		return SavedInfo{}, err
	}
	var info SavedInfo
	text, err := jsonText(saved)
	if err != nil {
		return SavedInfo{}, err
	}
	return info, jsonUnmarshal(text, &info)
}

func resolveFiles(ctx host.Ctx, ids []string, conversation int) ([]any, error) {
	files := []any{}
	if len(ids) == 0 {
		return files, nil
	}
	attachments, err := ctx.Get(filescontract.Attachments, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(ids); start += filescontract.MaxAttachments {
		chunk := ids[start:min(start+filescontract.MaxAttachments, len(ids))]
		list := make([]any, len(chunk))
		for i, id := range chunk {
			list[i] = id
		}
		resolved, err := attachments.Call("resolve", delta.JsonObjectOf("ids", list, "conversation", float64(conversation)), ctx)
		if err != nil {
			return nil, err
		}
		more, _ := resolved.([]any)
		files = append(files, more...)
	}
	return files, nil
}

// route is the send router: a message with new selections or annotations carries them after its text (the model's copy
// only: the transcript shows what the person wrote, and a note). Others go on to the next router, or become plain inputs.
func route(harnessOf func() (*p2runtime.HarnessParts, error)) chatcontract.SendRouter {
	return func(ctx host.Ctx, input chatcontract.RoutedSend) (*float64, error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		opened, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		chordctx.Suspend(ctx)
		state, err := ReadViews(ctx, opened)
		if err != nil {
			return nil, err
		}
		carried := ViewContext(state)
		if carried == nil {
			return nil, nil
		}
		files, err := resolveFiles(ctx, input.Attachments, input.Conversation)
		if err != nil {
			return nil, err
		}
		attachments := make([]any, len(files))
		for i, file := range files {
			attachments[i] = delta.JsonObjectOf("id", objectOf(file).Value("id"), "name", objectOf(file).Value("name"))
		}
		author := delta.JsonObjectOf("kind", string(input.Author.Kind), "id", input.Author.ID, "name", input.Author.Name)
		message := delta.JsonObjectOf("author", author, "text", input.Text, "attachments", attachments,
			"views", delta.JsonObjectOf("selections", float64(len(carried.Selections)), "annotations", float64(len(carried.Annotations))))
		chat, err := ctx.Get(chatcontract.Conversation, delta.JsonObjectOf("conversation", float64(input.Conversation)), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		body := carried.Text
		if strings.TrimSpace(input.Text) != "" {
			body = input.Text + "\n\n" + carried.Text
		}
		submit := delta.JsonObjectOf("text", body, "requestId", input.RequestID, "author", author, "files", files,
			"entry", delta.JsonObjectOf("kind", contract.ViewInput, "data", message))
		if input.WhenBusy != "" {
			submit.Set("whenBusy", input.WhenBusy)
		}
		submitted, err := chat.Call("submit", submit, ctx)
		if err != nil {
			return nil, err
		}
		if _, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) { return nil, MarkSent(tx, carried) }); err != nil {
			return nil, err
		}
		id, _ := objectOf(submitted).Value("submissionId").(float64)
		return &id, nil
	}
}

func serveTools(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.view-tools", func(self host.StoreSelf) error {
		harnessOf := p2runtime.Cached(p2runtime.HarnessOf(self))
		// The Harness is read in a section of the host first; the router, run off the loop by `Chat.send`, takes the kept one.
		_, _ = harnessOf()
		self.OnLive(func(host.Ctx) *host.Handled {
			_, _ = harnessOf()
			return nil
		})
		extension, err := CreateExtension(sessionBackend{worker: p2runtime.WorkerOf(self)})
		if err != nil {
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(extensionName, extension))
		p2runtime.ContributeKeyed(self, chatcontract.SendRouters, p2runtime.Entry("views", route(harnessOf)))
		return nil
	})
}

func viewsState(self host.Self, harnessOf func() (*p2runtime.HarnessParts, error)) chord.Watchable[delta.JsonValue] {
	return self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[ViewsState]) (durable.Observation[ViewsState], error) {
		parts, err := harnessOf()
		if err != nil {
			return nil, err
		}
		opened, err := parts.Durable()
		if err != nil {
			return nil, err
		}
		observed, err := durable.ObserveDoc(ctx, opened, ViewsDoc, observer)
		if err != nil || observed != nil {
			return observed, err
		}
		// No view yet: the document is absent, and an absent address cannot be followed. Create it empty.
		if _, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
			_, err := durable.TxDoc[ViewsState](tx, ViewsDoc)
			return nil, err
		}); err != nil {
			return nil, err
		}
		return durable.ObserveDoc(ctx, opened, ViewsDoc, observer)
	}))
}

func serveViews(facet host.FacetHost) error {
	return facet.Serve(contract.Views, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session := text(params, "session")
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		harnessOf := p2runtime.HarnessOf(self)
		worker := p2runtime.WorkerOf(self)
		index := viewsState(self, harnessOf)
		views := self.Derive([]any{index}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("views"), nil })
		annotations := self.Derive([]any{index}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("annotations"), nil })
		arrangement := self.Derive([]any{index}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("arrangement"), nil })
		state := map[string]chord.Watchable[delta.JsonValue]{"views": views, "annotations": annotations, "arrangement": arrangement}
		if !p2runtime.CanSend(who, session) || (!p2runtime.IsOwner(who) && !p2runtime.IsGuest(who)) {
			return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{State: state}}, nil
		}
		settings := self.Get(modelscontract.WorkerModels, delta.NewJsonObject(0))
		backend := sessionBackend{worker: worker}
		author := func() string {
			value, _ := settings.Member("settings").Value()
			name, _ := objectOf(value).Value("ownerName").(string)
			if name == "" {
				name = p2runtime.DefaultOwnerName
			}
			return p2runtime.AuthorOf(who, name).Name
		}
		// A person's call: refused while the session moves, which waits for running calls (as the chat's).
		commit := func(ctx context.Context, change func(tx durable.Tx) (any, error)) (any, error) {
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
			var result any
			err = parts.Liveness.Call(func() error {
				var callErr error
				result, callErr = opened.Commit(ctx, change)
				return callErr
			})
			return result, err
		}
		editView := func(id string, edit func(target *delta.Object) error) func(tx durable.Tx) (any, error) {
			return func(tx durable.Tx) (any, error) {
				draft, current, err := draftOf(tx)
				if err != nil {
					return nil, err
				}
				for i, view := range current.Views {
					if view.ID == id {
						return nil, edit(draft.Array("views").Object(i))
					}
				}
				return nil, invalid(fmt.Sprintf("No view %q", id))
			}
		}
		return suspended(contracts.Implementation{Variant: "editor", Members: contracts.VariantImplementation{
			State: state,
			Methods: map[string]contracts.MethodFunction{
				"open": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					input := objectOf(args)
					now := float64(nowMillis())
					if input.Has("saved") {
						if !p2runtime.IsOwner(who) {
							return nil, p2runtime.Forbidden("saved views are the owner's")
						}
						name := text(input, "saved")
						saved, err := backend.GetSaved(ctx, name)
						if err != nil {
							return nil, err
						}
						if saved == nil {
							return nil, invalid(fmt.Sprintf("No saved view %q", name))
						}
						current, err := readViewsOf(ctx, harnessOf)
						if err != nil {
							return nil, err
						}
						id := FreeViewID(current, saved.Name, saved.Kind)
						source := 0
						if saved.HTML != nil {
							if source, err = backend.SaveSource(ctx, id, *saved.HTML); err != nil {
								return nil, err
							}
						}
						spec := ViewSpec{ID: id, Kind: saved.Kind, Title: saved.Title, Path: saved.Path, Source: source, State: saved.State, HasState: true}
						if _, err := commit(ctx, func(tx durable.Tx) (any, error) { return OpenView(tx, spec, now) }); err != nil {
							return nil, err
						}
						return delta.JsonObjectOf("id", id), nil
					}
					kind, path := text(input, "kind"), text(input, "path")
					title := text(input, "title")
					if title == "" {
						switch {
						case kind == "markdown":
							title = path
						case path != "":
							title = "Changes in " + path
						default:
							title = "Changes"
						}
					}
					current, err := readViewsOf(ctx, harnessOf)
					if err != nil {
						return nil, err
					}
					id := FreeViewID(current, title, kind)
					if _, err := commit(ctx, func(tx durable.Tx) (any, error) {
						return OpenView(tx, ViewSpec{ID: id, Kind: kind, Title: title, Path: path}, now)
					}); err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("id", id), nil
				},
				"close": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					id := text(objectOf(args), "id")
					_, err := commit(ctx, func(tx durable.Tx) (any, error) { return RemoveView(tx, id) })
					return nil, err
				},
				"select": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					input := objectOf(args)
					id := text(input, "id")
					selection := objectOf(input.Value("selection"))
					if selection != nil && len(jsonString(selection.Value("data"))) > maxSelectionData {
						return nil, invalid("Selection data is too large")
					}
					_, err := commit(ctx, editView(id, func(target *delta.Object) error {
						if selection == nil {
							target.Delete("selection")
							return nil
						}
						next := delta.JsonObjectOf("summary", selection.Value("summary"))
						if selection.Has("data") {
							next.Set("data", selection.Value("data"))
						}
						return target.Set("selection", next)
					}))
					return nil, err
				},
				"annotate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					name := author()
					id, err := commit(ctx, func(tx durable.Tx) (any, error) { return AddAnnotation(tx, objectOf(args), name, float64(nowMillis())) })
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("id", id), nil
				},
				"unannotate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					input := objectOf(args)
					ids, restricted := input.Value("ids").([]any)
					remove := map[string]bool{}
					for _, id := range ids {
						if text, ok := id.(string); ok {
							remove[text] = true
						}
					}
					_, err := commit(ctx, func(tx durable.Tx) (any, error) {
						draft, current, err := draftOf(tx)
						if err != nil {
							return nil, err
						}
						for i := len(current.Annotations) - 1; i >= 0; i-- {
							if !restricted || remove[current.Annotations[i].ID] {
								if _, err := draft.Array("annotations").Splice(i, 1); err != nil {
									return nil, err
								}
							}
						}
						return nil, nil
					})
					return nil, err
				},
				"discard": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := commit(ctx, func(tx durable.Tx) (any, error) {
						draft, current, err := draftOf(tx)
						if err != nil {
							return nil, err
						}
						for i := len(current.Annotations) - 1; i >= 0; i-- {
							if !current.Annotations[i].Sent {
								if _, err := draft.Array("annotations").Splice(i, 1); err != nil {
									return nil, err
								}
							}
						}
						for i, view := range current.Views {
							if view.Selection != nil {
								if err := draft.Array("views").Object(i).Object("selection").Set("sent", true); err != nil {
									return nil, err
								}
							}
						}
						return nil, nil
					})
					return nil, err
				},
				"report": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					input := objectOf(args)
					id := text(input, "id")
					errs, _ := input.Value("errors").([]any)
					_, err := commit(ctx, func(tx durable.Tx) (any, error) {
						draft, current, err := draftOf(tx)
						if err != nil {
							return nil, err
						}
						for i, view := range current.Views {
							if view.ID != id || view.Kind != "custom" {
								continue
							}
							target := draft.Array("views").Object(i)
							if len(errs) > 0 {
								return nil, target.Set("errors", errs)
							}
							target.Delete("errors")
						}
						return nil, nil
					})
					return nil, err
				},
			},
		}}), nil
	}), host.ServeOptions{})
}

func readViewsOf(ctx context.Context, harnessOf func() (*p2runtime.HarnessParts, error)) (ViewsState, error) {
	parts, err := harnessOf()
	if err != nil {
		return ViewsState{}, err
	}
	opened, err := parts.Durable()
	if err != nil {
		return ViewsState{}, err
	}
	return ReadViews(ctx, opened)
}

func serveViewState(facet host.FacetHost) error {
	return facet.Serve(contract.ViewState, host.RemoteFactory(func(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		session, id := text(params, "session"), text(params, "view")
		who, err := p2runtime.FromChord(principal)
		if err != nil || !p2runtime.CanView(who, session) {
			return contracts.Implementation{}, p2runtime.Forbidden("viewers only")
		}
		harnessOf := p2runtime.HarnessOf(self)
		doc := self.Follow(p2runtime.FollowDurable(func(ctx context.Context, observer durable.Observer[ViewValue]) (durable.Observation[ViewValue], error) {
			parts, err := harnessOf()
			if err != nil {
				return nil, err
			}
			opened, err := parts.Durable()
			if err != nil {
				return nil, err
			}
			observed, err := durable.ObserveDoc(ctx, opened, ViewStateDoc, observer, id, nil)
			if err != nil || observed != nil {
				return observed, err
			}
			current, err := ReadViews(ctx, opened)
			if err != nil {
				return nil, err
			}
			known := false
			for _, view := range current.Views {
				known = known || view.ID == id
			}
			if !known {
				return nil, data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonParams, Message: fmt.Sprintf("No view %q", id)}, nil)
			}
			if _, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
				_, err := durable.TxDoc[ViewValue](tx, ViewStateDoc, id, nil)
				return nil, err
			}); err != nil {
				return nil, err
			}
			return durable.ObserveDoc(ctx, opened, ViewStateDoc, observer, id, nil)
		}))
		value := self.Derive([]any{doc}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("value"), nil })
		state := map[string]chord.Watchable[delta.JsonValue]{"value": value}
		if !p2runtime.CanSend(who, session) || (!p2runtime.IsOwner(who) && !p2runtime.IsGuest(who)) {
			return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{State: state}}, nil
		}
		worker := p2runtime.WorkerOf(self)
		return suspended(contracts.Implementation{Variant: "editor", Members: contracts.VariantImplementation{
			State: state,
			Methods: map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
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
					next := objectOf(args).Value("value")
					checked, err := CheckedState(next)
					if err != nil {
						return nil, err
					}
					return nil, parts.Liveness.Call(func() error {
						_, err := opened.Commit(ctx, func(tx durable.Tx) (any, error) {
							_, current, err := draftOf(tx)
							if err != nil {
								return nil, err
							}
							known := false
							for _, view := range current.Views {
								known = known || view.ID == id
							}
							if !known {
								return nil, invalid(fmt.Sprintf("No view %q", id))
							}
							doc, err := durable.TxDoc[ViewValue](tx, ViewStateDoc, id, nil)
							if err != nil {
								return nil, err
							}
							return nil, doc.Set("value", checked)
						})
						return err
					})
				},
			},
		}}), nil
	}), host.ServeOptions{})
}

func serveViewSource(facet host.FacetHost) error {
	return facet.Serve(contract.ViewSource, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != (p2runtime.ServerPrincipal{}) {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		harnessOf := p2runtime.HarnessOf(self)
		files := self.Get(app.Files, delta.NewJsonObject(0))
		return suspended(contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				input := objectOf(args)
				id := text(input, "view")
				number, _ := input.Value("version").(float64)
				version := int(number)
				current, err := readViewsOf(ctx, harnessOf)
				if err != nil {
					return nil, err
				}
				for _, view := range current.Views {
					if view.ID == id && view.Kind == "custom" && view.Source != 0 && version <= view.Source {
						html, err := sourceOf(files, id, version, ctx)
						if err != nil {
							return nil, err
						}
						return delta.JsonObjectOf("title", view.Title, "html", html), nil
					}
				}
				return nil, invalid("View not found")
			},
		}}}), nil
	}), host.ServeOptions{})
}

// Worker is the plugin's worker facet.
var Worker = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveTools, serveViews, serveViewState, serveViewSource} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})

// suspended parks the host loop's section before each method runs: the Durable reads and commits below need the loop.
func suspended(impl contracts.Implementation) contracts.Implementation {
	for name, method := range impl.Members.Methods {
		impl.Members.Methods[name] = func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			chordctx.Suspend(ctx)
			return method(args, ctx)
		}
	}
	return impl
}
