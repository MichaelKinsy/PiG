package search

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/search/server.ts: the message index, the workers' feed into it, and owner search over sessions and
// messages.

func mustState(name string, options documents.StateOptions) *documents.StateDefinition {
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

func mustJob(name string, options host.JobOptions) *host.JobDefinition {
	definition, err := host.DefineJob(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	c = p2runtime.Contract
	// markers is the newest root answer and the owner's read marker per session, mirrored from the database.
	markers = mustState("p2.search-index", documents.StateOptions{
		Schema: c(schema.Obj(schema.Properties{
			schema.Prop("answers", p2runtime.Record(schema.Num())), schema.Prop("reads", p2runtime.Record(schema.Num())),
		})),
		Version: 1,
		Initial: func() *delta.JsonObject {
			return delta.JsonObjectOf("answers", delta.NewJsonObject(0), "reads", delta.NewJsonObject(0))
		},
	})
	// rebuildJob is Pi Mobile's rebuildIndex as a job: a rebuild interrupted by a restart runs again (it starts over).
	rebuildJob = mustJob("p2.search.rebuild", host.JobOptions{
		Version: 1,
		Args:    c(schema.Obj(schema.Properties{schema.Prop("session", schema.Str())})),
		Result:  c(schema.Obj(schema.Properties{})),
		Persist: &host.JobPersistence{Resume: host.ResumeRerun},
	})
	// rebuildFailures is the latest failed rebuild per session, until it is rebuilt again.
	rebuildFailures = mustState("p2.search.rebuild-failures", documents.StateOptions{
		Schema:  c(schema.Obj(schema.Properties{schema.Prop("list", schema.Arr(contract.RebuildSchema))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("list", []any{}) },
	})
)

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

func intOf(value any, fallback int) int {
	if number, ok := value.(float64); ok {
		return int(number)
	}
	return fallback
}

// sidebarOrder is the order sessions show in: by directory, pinned first, most recently updated first.
func sidebarOrder(a, b sessioncontract.Session) int {
	if byCwd := localecompare.Compare(a.Cwd, b.Cwd); byCwd != 0 {
		return byCwd
	}
	if a.Pinned != b.Pinned {
		if a.Pinned {
			return -1
		}
		return 1
	}
	switch {
	case a.UpdatedAt > b.UpdatedAt:
		return -1
	case a.UpdatedAt < b.UpdatedAt:
		return 1
	}
	return 0
}

// cached keeps the first success of a native getter: the registry value behind it may be read only inside a section of
// the host, so the first call is made in one (a factory, a handler's synchronous part).
func cached[T any](get func() (*T, error)) func() (*T, error) {
	var mu sync.Mutex
	var kept *T
	return func() (*T, error) {
		mu.Lock()
		defer mu.Unlock()
		if kept != nil {
			return kept, nil
		}
		value, err := get()
		if err == nil {
			kept = value
		}
		return value, err
	}
}

func recordOf(counts map[string]int, order []string) *delta.JsonObject {
	record := delta.NewJsonObject(len(order))
	for _, session := range order {
		record.Set(session, float64(counts[session]))
	}
	return record
}

func serveIndex(facet host.FacetHost) error {
	return facet.Serve(contract.SearchIndex, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		archivedStarts := self.Get(sessioncontract.ArchivedStarts, delta.NewJsonObject(0))
		natives := cached(p2runtime.ServerOf(self))
		if _, err := natives(); err != nil {
			return contracts.Implementation{}, err
		}
		answers := self.State(markers).(documents.SingletonHandle)
		failures := self.State(rebuildFailures).(documents.SingletonHandle)
		database := self.Open(
			func(ctx context.Context) (any, error) {
				info, err := server.Member("info").Value()
				if err != nil {
					return nil, err
				}
				opened, err := Open(ctx, stringOf(objectOf(info).Value("dataDir")))
				if err != nil {
					return nil, err
				}
				latest, order, err := opened.Answers(ctx)
				if err != nil {
					_ = opened.Close()
					return nil, err
				}
				read, readOrder, err := opened.Reads(ctx)
				if err != nil {
					_ = opened.Close()
					return nil, err
				}
				if _, err := answers.Change(ctx, func(draft *delta.Object) (any, error) {
					if err := draft.Set("answers", recordOf(latest, order)); err != nil {
						return nil, err
					}
					return nil, draft.Set("reads", recordOf(read, readOrder))
				}); err != nil {
					_ = opened.Close()
					return nil, err
				}
				return opened, nil
			},
			func(value any, _ host.NativeCloseReason, _ context.Context) error { return value.(*DB).Close() },
		)
		db := func() (*DB, error) {
			value, err := database.Value()
			if err != nil {
				return nil, err
			}
			return value.(*DB), nil
		}
		catalogRow := func(ctx context.Context, id string) (sessioncontract.Session, error) {
			row, err := catalog.Call("get", delta.JsonObjectOf("id", id), ctx)
			return sessioncontract.SessionFromJSON(row), err
		}
		// Stop the worker, reset the index, scan from a fresh worker (an archived one without resuming its work), and stop an
		// archived session's worker again. This local object's `ctx.get` acts as the server, whom `IndexScan` serves.
		rebuilds := self.Job(rebuildJob, host.JobImplementation{
			Run: func(args any, ctx host.JobCtx) (any, error) {
				session := stringOf(objectOf(args).Value("session"))
				row, err := catalogRow(ctx, session)
				if err != nil {
					return nil, err
				}
				archived := row.ArchivedAt != nil
				parts, err := natives()
				if err != nil {
					return nil, err
				}
				if err := parts.Workers.Stop(ctx, session); err != nil {
					return nil, err
				}
				index, err := db()
				if err != nil {
					return nil, err
				}
				if err := index.Reset(ctx, session); err != nil {
					return nil, err
				}
				if archived {
					if _, err := archivedStarts.Call("allow", delta.JsonObjectOf("session", session), ctx); err != nil {
						return nil, err
					}
				}
				scanErr := func() error {
					scan, err := ctx.Get(contract.IndexScan, delta.JsonObjectOf("session", session), host.GetOptions{})
					if err != nil {
						return err
					}
					_, err = scan.Call("scanned", nil, ctx)
					return err
				}()
				if archived {
					if _, err := archivedStarts.Call("end", delta.JsonObjectOf("session", session), ctx); err != nil && scanErr == nil {
						scanErr = err
					}
					if err := parts.Workers.Suspend(ctx, session); err != nil && scanErr == nil {
						scanErr = err
					}
				}
				if scanErr != nil {
					return nil, scanErr
				}
				return delta.NewJsonObject(0), nil
			},
			Finish: func(outcome host.Outcome, tx documents.Tx) error {
				session := stringOf(objectOf(outcome.Args).Value("session"))
				draft, err := tx.Draft(failures)
				if err != nil {
					return err
				}
				kept := []any{}
				for _, each := range draft.Array("list").Snapshot() {
					if stringOf(objectOf(each).Value("session")) != session {
						kept = append(kept, each)
					}
				}
				if outcome.Kind == host.OutcomeFailed {
					text := "The rebuild was interrupted"
					if outcome.Error.Kind == host.JobThrew && outcome.Error.Error != nil {
						text = outcome.Error.Error.Error()
					}
					kept = append(kept, delta.JsonObjectOf("session", session, "phase", "failed", "error", text))
				}
				return draft.Set("list", kept)
			},
		})
		rebuildList := self.Derive([]any{rebuilds.Running(), failures}, func(values []any) (delta.JsonValue, error) {
			list := []any{}
			running, _ := values[0].([]any)
			for _, each := range running {
				run := objectOf(each)
				if run.Value("phase") == "blocked" {
					continue
				}
				list = append(list, delta.JsonObjectOf("session", stringOf(objectOf(run.Value("args")).Value("session")), "phase", "running"))
			}
			failed, _ := objectOf(values[1]).Value("list").([]any)
			return append(list, failed...), nil
		})
		answerOf := func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("answers"), nil }
		readOf := func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("reads"), nil }
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"rebuilds": rebuildList,
				"answers":  self.Derive([]any{answers}, answerOf),
				"reads":    self.Derive([]any{answers}, readOf),
			},
			Methods: map[string]contracts.MethodFunction{
				"rebuild": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					session := stringOf(objectOf(args).Value("session"))
					// Refused at once for a session the catalog does not know.
					if _, err := catalogRow(ctx, session); err != nil {
						return nil, err
					}
					_, err := rebuilds.Start(delta.JsonObjectOf("session", session), ctx, host.StartOptions{Key: &session})
					return nil, err
				},
				"markRead": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					session, through := stringOf(in.Value("session")), intOf(in.Value("through"), 0)
					current, err := answers.Value()
					if err != nil {
						return nil, err
					}
					if through <= intOf(objectOf(objectOf(current).Value("reads")).Value(session), 0) {
						return nil, nil
					}
					index, err := db()
					if err != nil {
						return nil, err
					}
					if err := index.MarkRead(ctx, session, through); err != nil {
						return nil, err
					}
					_, err = answers.Change(ctx, func(draft *delta.Object) (any, error) {
						return nil, draft.Object("reads").Set(session, float64(through))
					})
					return nil, err
				},
				"cursor": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					index, err := db()
					if err != nil {
						return nil, err
					}
					cursor, err := index.Cursor(ctx, stringOf(in.Value("session")), intOf(in.Value("conversation"), 1))
					return float64(cursor), err
				},
				"index": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					session, conversation, through := stringOf(in.Value("session")), intOf(in.Value("conversation"), 1), intOf(in.Value("through"), 0)
					index, err := db()
					if err != nil {
						return nil, err
					}
					var messages []IndexedMessage
					answer, newest := 0, intOf(in.Value("activityAt"), 0)
					timestamp := float64(newest)
					rows, _ := in.Value("messages").([]any)
					for _, each := range rows {
						row := objectOf(each)
						message := IndexedMessage{
							ConversationID: intOf(row.Value("conversationId"), 0), EntryID: intOf(row.Value("entryId"), 0),
							Role: stringOf(row.Value("role")), Text: stringOf(row.Value("text")),
						}
						message.Timestamp, _ = row.Value("timestamp").(float64)
						messages = append(messages, message)
						if message.Role == "assistant" {
							answer = max(answer, message.EntryID)
						}
						timestamp = max(timestamp, message.Timestamp)
					}
					previous, err := index.Cursor(ctx, session, conversation)
					if err != nil {
						return nil, err
					}
					completed := through > previous
					if err := index.Index(ctx, session, conversation, messages, through); err != nil {
						return nil, err
					}
					// Unread answers are the top-level conversations' (the main one and the other `/tree` branches): a branch
					// left while running answers in the background. `branches` is empty until the worker first reports it.
					current, err := answers.Value()
					if err != nil {
						return nil, err
					}
					var row sessioncontract.Session
					if answer > intOf(objectOf(objectOf(current).Value("answers")).Value(session), 0) {
						if row, err = catalogRow(ctx, session); err != nil {
							return nil, err
						}
						if conversation == row.Main || slices.ContainsFunc(row.Branches, func(branch sessioncontract.Branch) bool { return branch.Conversation == conversation }) {
							if _, err := answers.Change(ctx, func(draft *delta.Object) (any, error) {
								return nil, draft.Object("answers").Set(session, float64(answer))
							}); err != nil {
								return nil, err
							}
						}
					}
					activity := delta.JsonObjectOf("id", session, "timestamp", timestamp)
					if completed {
						if text, found, err := index.FirstUserText(ctx, session); err != nil {
							return nil, err
						} else if found {
							activity.Set("firstUserText", text)
						}
					}
					_, err = catalog.Call("activity", activity, ctx)
					return nil, err
				},
				"reset": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					index, err := db()
					if err != nil {
						return nil, err
					}
					return nil, index.Reset(ctx, stringOf(objectOf(args).Value("session")))
				},
				"messages": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					var sessions []string
					listed, _ := in.Value("sessions").([]any)
					for _, each := range listed {
						sessions = append(sessions, stringOf(each))
					}
					index, err := db()
					if err != nil {
						return nil, err
					}
					hits, err := index.Messages(ctx, stringOf(in.Value("query")), sessions)
					if err != nil {
						return nil, err
					}
					out := make([]any, len(hits))
					for at, hit := range hits {
						out[at] = delta.JsonObjectOf("session", hit.Session, "conversationId", float64(hit.ConversationID), "entryId", float64(hit.EntryID), "role", hit.Role, "text", hit.Text)
					}
					return out, nil
				},
				"mentions": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					match := FTSQuery(stringOf(in.Value("query")))
					if match == "" {
						return delta.NewJsonObject(0), nil
					}
					since, _ := in.Value("since").(float64)
					index, err := db()
					if err != nil {
						return nil, err
					}
					latest, order, err := index.Mentions(ctx, match, since)
					if err != nil {
						return nil, err
					}
					out := delta.NewJsonObject(len(order))
					for _, session := range order {
						out.Set(session, latest[session])
					}
					return out, nil
				},
				"history": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					session, conversation := stringOf(in.Value("session")), intOf(in.Value("conversation"), 0)
					index, err := db()
					if err != nil {
						return nil, err
					}
					indexedThrough, err := index.Cursor(ctx, session, conversation)
					if err != nil {
						return nil, err
					}
					match := FTSQuery(stringOf(in.Value("query")))
					if match == "" {
						return delta.JsonObjectOf("hits", []any{}, "more", false, "indexedThrough", float64(indexedThrough)), nil
					}
					var before *int
					if given, ok := in.Value("before").(float64); ok {
						bound := int(given)
						before = &bound
					}
					hits, more, err := index.ConversationMessages(ctx, session, conversation, match, before, intOf(in.Value("limit"), 20))
					if err != nil {
						return nil, err
					}
					out := make([]any, len(hits))
					for at, hit := range hits {
						out[at] = delta.JsonObjectOf("entryId", float64(hit.EntryID), "role", hit.Role, "snippet", hit.Snippet)
					}
					return delta.JsonObjectOf("hits", out, "more", more, "indexedThrough", float64(indexedThrough)), nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveSessionReads(facet host.FacetHost) error {
	return facet.Serve(contract.SessionReads, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		// Guests never read for the owner, nor see what the owner has read.
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("read markers are owner-only")
		}
		index := self.Get(contract.SearchIndex, delta.NewJsonObject(0))
		same := func(values []any) (delta.JsonValue, error) { return values[0], nil }
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"answers": self.Derive([]any{index.Member("answers")}, same),
				"reads":   self.Derive([]any{index.Member("reads")}, same),
			},
			Methods: map[string]contracts.MethodFunction{
				"markRead": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return index.Call("markRead", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveFeed(facet host.FacetHost) error {
	return facet.Serve(contract.SearchFeed, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		// Only a session's own worker feeds it; the session comes from the principal, never from arguments.
		who, err := p2runtime.FromChord(principal)
		worker, isWorker := who.(p2runtime.WorkerPrincipal)
		if err != nil || !isWorker {
			return contracts.Implementation{}, p2runtime.Forbidden("the search feed is for session workers")
		}
		session := worker.Session
		index := self.Get(contract.SearchIndex, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"cursor": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				conversation := 1
				if object := objectOf(args); object != nil {
					conversation = intOf(object.Value("conversation"), 1)
				}
				return index.Call("cursor", delta.JsonObjectOf("session", session, "conversation", float64(conversation)), ctx)
			},
			"index": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				batch := objectOf(args)
				conversation := intOf(batch.Value("conversation"), 1)
				messages, _ := batch.Value("messages").([]any)
				for _, each := range messages {
					if intOf(objectOf(each).Value("conversationId"), 0) != conversation {
						return nil, p2runtime.Forbidden("index messages must belong to the batch conversation")
					}
				}
				forwarded := delta.JsonObjectOf("session", session)
				for _, key := range batch.Keys() {
					forwarded.Set(key, batch.Value(key))
				}
				forwarded.Set("conversation", float64(conversation))
				return index.Call("index", forwarded, ctx)
			},
			// The session is the worker's own, from its principal: a worker searches only its session.
			"history": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				query := objectOf(args)
				forwarded := delta.JsonObjectOf("session", session)
				for _, key := range query.Keys() {
					forwarded.Set(key, query.Value(key))
				}
				return index.Call("history", forwarded, ctx)
			},
		}}}, nil
	}), host.ServeOptions{})
}

func serveSearch(facet host.FacetHost) error {
	return facet.Serve(contract.Search, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || !p2runtime.IsOwner(who) {
			return contracts.Implementation{}, p2runtime.Forbidden("search is owner-only")
		}
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		index := self.Get(contract.SearchIndex, delta.NewJsonObject(0))
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"rebuilds": self.Derive([]any{index.Member("rebuilds")}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			},
			Methods: map[string]contracts.MethodFunction{
				"rebuildIndex": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return index.Call("rebuild", args, ctx)
				},
				"search": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectOf(args)
					query := stringOf(in.Value("query"))
					archived := in.Value("archived") == true
					within, _ := in.Value("sessions").([]any)
					var listed map[string]bool
					if in.Has("sessions") {
						listed = make(map[string]bool, len(within))
						for _, id := range within {
							listed[stringOf(id)] = true
						}
					}
					rows, err := catalog.Member("sessions").Value()
					if err != nil {
						return nil, err
					}
					var scope []sessioncontract.Session
					list, _ := rows.([]any)
					for _, row := range list {
						if session := sessioncontract.SessionFromJSON(row); (session.ArchivedAt != nil) == archived && (listed == nil || listed[session.ID]) {
							scope = append(scope, session)
						}
					}
					needle := lower(query)
					var matching []sessioncontract.Session
					for _, session := range scope {
						if strings.Contains(lower(session.Title+"\n"+session.Cwd), needle) {
							matching = append(matching, session)
						}
					}
					slices.SortStableFunc(matching, sidebarOrder)
					sessions := make([]any, len(matching))
					for at, session := range matching {
						sessions[at] = session.ID
					}
					titles := make(map[string]string, len(scope))
					ids := make([]any, len(scope))
					for at, session := range scope {
						titles[session.ID] = session.Title
						ids[at] = session.ID
					}
					messages := []any{}
					if strings.TrimSpace(query) != "" {
						found, err := index.Call("messages", delta.JsonObjectOf("query", query, "sessions", ids), ctx)
						if err != nil {
							return nil, err
						}
						hits, _ := found.([]any)
						for _, each := range hits {
							hit := objectOf(each)
							withTitle := delta.NewJsonObject(6)
							for _, key := range hit.Keys() {
								withTitle.Set(key, hit.Value(key))
							}
							withTitle.Set("title", titles[stringOf(hit.Value("session"))])
							messages = append(messages, withTitle)
						}
					}
					return delta.JsonObjectOf("sessions", sessions, "messages", messages), nil
				},
			},
		}}, nil
	}), host.ServeOptions{Accepts: searchAccepts()})
}

// searchAccepts: older pages search every session of the scope: their arguments are the same without `sessions`.
func searchAccepts() contracts.AcceptsTable {
	same := func(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }
	versions := contracts.VersionMappings{Methods: []contracts.Entry[contracts.MethodMapping]{
		{Name: "search", Value: contracts.MethodMapping{Args: same}},
	}}
	return contracts.AcceptsTable{{Name: "1", Value: versions}, {Name: "2", Value: versions}}
}

// ServerFacet serves the index, the workers' feed, owner search and unread answers.
var ServerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveIndex, serveSessionReads, serveFeed, serveSearch} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})

func lower(text string) string { return jsstring.ToLower(text) }
