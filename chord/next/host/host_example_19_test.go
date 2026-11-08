package host

// pi: packages/chord/test/examples/19-jobs.ts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// --- 19-jobs.ts ----------------------------------------------------------------------------------------------------

func TestExample19Jobs(t *testing.T) {
	shellState := exDefineState("example.shell.state", documents.StateOptions{
		Schema:  exObj(exProp("output", exStr())),
		Initial: func() *delta.JsonObject { return jsonObject("output", "") },
	})
	one := 1
	// 1. The job: closed args (checked at every start), a result, a state document per run, one run at a time.
	shellJob, err := DefineJob("example.shell", JobOptions{
		Version:     1,
		Args:        exObj(exProp("command", exStr()), exProp("lines", exInt())),
		Result:      exObj(exProp("exit", exInt()), exProp("output", exStr())),
		State:       shellState,
		Concurrency: &one,
	})
	must(t, err)
	// A second job `finish` chains: it announces a finished command.
	notifyJob, err := DefineJob("example.notify", JobOptions{Version: 1, Args: exObj(exProp("text", exStr())), Result: exStr()})
	must(t, err)
	entriesDoc := exStringListDoc("example.entries")
	shell := exObj(exProp("id", exStr()), exProp("phase", exStr()), exProp("command", exStr()), exProp("output", exStr()))
	guestShell := exObj(exProp("id", exStr()), exProp("phase", exStr()), exProp("command", exStr()))
	runArgs := exObj(exProp("command", exStr()), exProp("lines", exInt()), exProp("requestId", exStr()))
	chatStore := exLocal("example.chat-store", contracts.Params{},
		contracts.Mem("entries", exArr(exStr())), contracts.Mem("shells", exArr(shell)),
		contracts.Mem("runShell", contracts.NewMethod(runArgs, exStr())),
		contracts.Mem("abortShell", contracts.NewMethod(exObj(exProp("id", exStr())))))
	chat := exRemote("example.chat", contracts.Params{}, exServerAddress,
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("entries", exArr(exStr())), exProp("shells", exArr(shell))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("runShell", contracts.NewMethod(runArgs, exStr())),
				contracts.Meth("abortShell", contracts.NewMethod(exObj(exProp("id", exStr())))),
			},
		}),
		contracts.Var("guest", contracts.VariantDeclaration{
			State: []contracts.Property{exProp("entries", exArr(exStr())), exProp("shells", exArr(guestShell))},
		}))
	// A fake process: one line every 30 ms; `Sleep` returns when the run is aborted.
	runProcess := func(lines int, ctx context.Context, onLine func(string)) (int, error) {
		for n := 1; n <= lines; n++ {
			if err := Sleep(30*time.Millisecond, ctx); err != nil {
				return 0, err
			}
			onLine(fmt.Sprintf("line %d\n", n))
		}
		return 0, nil
	}
	errs := &hostErrors{}
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(chatStore, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			entries := stateOf(self, entriesDoc)
			notify := self.Job(notifyJob, JobImplementation{
				Run: func(args any, _ JobCtx) (any, error) {
					return "notified: " + exParam(args.(*delta.JsonObject), "text"), nil
				},
				Finish: func(outcome Outcome, tx documents.Tx) error {
					if outcome.Kind == OutcomeDone {
						draft, err := tx.Draft(entries.s)
						if err != nil {
							return err
						}
						_, err = draft.Array("list").Push(outcome.Result)
						return err
					}
					return nil
				},
			})
			shellHandle := self.Job(shellJob, JobImplementation{
				Run: func(args any, ctx JobCtx) (any, error) {
					lines := int(args.(*delta.JsonObject).Value("lines").(float64))
					// The output's bounds are required: a tail of at most 2,000 lines and 64 KiB.
					out := output.NewOutputBuffer(output.OutputLimits{MaxBytes: 64 * 1024, MaxLines: 2000, Retain: "tail"})
					// The buffer is single-threaded in Pi; Progress writes on a task of its own here.
					var outMu sync.Mutex
					write := func() (int, error) {
						outMu.Lock()
						text := out.Snapshot().Text
						outMu.Unlock()
						_, err := ctx.State().Change(ctx, func(draft *delta.Object) (any, error) {
							return nil, draft.Set("output", text)
						}, ChangeOptions{})
						return len(text), err
					}
					progress := output.NewProgress(write, func(err error) { errs.push(HostError{Kind: "progress_failed", Error: err}) }, 50)
					exit, runErr := runProcess(lines, ctx, func(line string) {
						outMu.Lock()
						out.PushString(line)
						outMu.Unlock()
						progress.Mark()
					})
					// The last output reaches `outcome.state`, also after an abort (changes are taken until run settles).
					output.SettleAndAwait(progress.Stop(), nil)
					_, _ = write()
					if runErr != nil {
						return nil, runErr
					}
					outMu.Lock()
					defer outMu.Unlock()
					return jsonObject("exit", float64(exit), "output", out.Snapshot().Text), nil
				},
				Finish: func(outcome Outcome, tx documents.Tx) error {
					args := outcome.Args.(*delta.JsonObject)
					command := exParam(args, "command")
					lines := strings.Count(outcome.State.(*delta.JsonObject).Value("output").(string), "\n")
					var text string
					if outcome.Kind == OutcomeDone {
						text = fmt.Sprintf("%s: exit %d, %d lines", command, int(outcome.Result.(*delta.JsonObject).Value("exit").(float64)), lines)
					} else {
						text = fmt.Sprintf("%s: interrupted (%s), %d lines kept", command, outcome.Error.Kind, lines)
					}
					draft, err := tx.Draft(entries.s)
					if err != nil {
						return err
					}
					if _, err := draft.Array("list").Push(text); err != nil {
						return err
					}
					// Chaining: the next job starts atomically with this outcome (both commit or neither).
					_, err = tx.Start(notify.(documents.JobHandle), jsonObject("text", command))
					return err
				},
			})
			// What the views read: one object per run.
			toShell := func(running any) any {
				out := []any{}
				for _, entry := range running.([]any) {
					object := entry.(*delta.JsonObject)
					if object.Value("phase") == "blocked" {
						out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"), "command", "?", "output", ""))
						continue
					}
					out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"),
						"command", object.Value("args").(*delta.JsonObject).Value("command"),
						"output", object.Value("state").(*delta.JsonObject).Value("output")))
				}
				return out
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"entries": self.Derive([]any{entries.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("list"), nil
				}),
				"shells": self.Derive([]any{shellHandle.Running()}, func(values []any) (delta.JsonValue, error) {
					return toShell(values[0]), nil
				}),
			}, map[string]contracts.MethodFunction{
				"runShell": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
					key := exParam(args, "requestId")
					run, err := shellHandle.Start(jsonObject("command", args.Value("command"), "lines", args.Value("lines")), ctx, documents.StartOptions{Key: &key})
					if err != nil {
						return nil, err
					}
					return run.ID(), nil
				}),
				"abortShell": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					go func() { _, _ = shellHandle.Abort(exParam(args, "id"), background) }()
					return nil, nil
				}),
			}), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(chat, func(_ *delta.JsonObject, principal data.Principal, self Self) (contracts.Implementation, error) {
			store := self.Get(chatStore, jsonObject())
			if principalKind(principal) != "owner" {
				// A guest sees no output: the view drops it.
				return exVariant("guest", map[string]chord.Watchable[delta.JsonValue]{
					"entries": Expose(store.Member("entries")),
					"shells": self.Derive([]any{store.Member("shells")}, func(values []any) (delta.JsonValue, error) {
						out := []any{}
						for _, entry := range values[0].([]any) {
							object := entry.(*delta.JsonObject)
							out = append(out, jsonObject("id", object.Value("id"), "phase", object.Value("phase"), "command", object.Value("command")))
						}
						return out, nil
					}),
				}, nil), nil
			}
			return exVariant("owner", map[string]chord.Watchable[delta.JsonValue]{
				"entries": Expose(store.Member("entries")), "shells": Expose(store.Member("shells")),
			}, map[string]contracts.MethodFunction{
				"runShell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("runShell", args, ctx)
				},
				"abortShell": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return store.Call("abortShell", args, ctx)
				},
			}), nil
		}, ServeOptions{})
	})
	// 2. A server and two tabs, the owner's and a guest's.
	server, serverErrors := exServer(t, nil, HostFacet{Name: "@example/chat", Facet: facet})
	ownerTab := exAccept(t, server, ownerPrincipal())
	guestTab := exAccept(t, server, jsonObject("kind", "guest"))
	owner := exGet(t, ownerTab.Host, chat, jsonObject())
	guest := exGet(t, guestTab.Host, chat, jsonObject())
	if exVariantName(owner) != "owner" || exVariantName(guest) != "guest" {
		t.Fatalf("variants: %q %q", exVariantName(owner), exVariantName(guest))
	}
	// 3. The owner runs a command; its request id dedupes a resent request (the same run comes back).
	id := exCall(t, owner, "runShell", jsonObject("command", "npm test", "lines", float64(5), "requestId", "r1"))
	again := exCall(t, owner, "runShell", jsonObject("command", "npm test", "lines", float64(5), "requestId", "r1"))
	if id != again {
		t.Fatalf("a resent request returns another run: %v, %v", id, again)
	}
	scopeEventually(t, "the owner sees the output", func() bool {
		list := exValue(t, owner, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("output") != ""
	})
	ownerShell := exValue(t, owner, "shells").([]any)[0].(*delta.JsonObject)
	if ownerShell.Value("id") != id || ownerShell.Value("phase") != "running" || ownerShell.Value("command") != "npm test" {
		t.Fatalf("owner sees: %s", exText(ownerShell))
	}
	scopeEventually(t, "the guest sees the run", func() bool {
		list := exValue(t, guest, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == id
	})
	guestSees := exValue(t, guest, "shells").([]any)[0].(*delta.JsonObject)
	if _, hasOutput := guestSees.Get("output"); hasOutput || guestSees.Value("command") != "npm test" {
		t.Fatalf("guest sees: %s", exText(guestSees))
	}
	// 4. One run at a time: a second command is refused while the first runs.
	_, err = owner.Call("runShell", jsonObject("command", "npm run build", "lines", float64(1), "requestId", "r2"), background)
	_ = exCode(t, err, data.CodeUnavailable, data.ReasonBusy)
	scopeEventually(t, "entries after the first run", func() bool {
		return exJSONEqual(exValue(t, owner, "entries"), exJSON(t, `["npm test: exit 0, 5 lines","notified: npm test"]`))
	})
	// 5. Abort is a request: the run stops at its next `sleep`, its last output is flushed, `finish` records it.
	long := exCall(t, owner, "runShell", jsonObject("command", "tail -f log", "lines", float64(100), "requestId", "r3"))
	scopeEventually(t, "the long run has output", func() bool {
		list := exValue(t, owner, "shells").([]any)
		return len(list) == 1 && list[0].(*delta.JsonObject).Value("id") == long && list[0].(*delta.JsonObject).Value("output") != ""
	})
	exCall(t, owner, "abortShell", jsonObject("id", long))
	scopeEventually(t, "entries after the abort", func() bool { return len(exValue(t, owner, "entries").([]any)) == 4 })
	entries := exValue(t, owner, "entries").([]any)
	if text := entries[2].(string); !strings.HasPrefix(text, "tail -f log: interrupted (aborted), ") || !strings.HasSuffix(text, " lines kept") || entries[3] != "notified: tail -f log" {
		t.Fatalf("entries after the abort: %s", exText(entries))
	}
	scopeEventually(t, "nothing running", func() bool { return len(exValue(t, owner, "shells").([]any)) == 0 })
	exCloseHost(t, ownerTab.Host)
	exCloseHost(t, guestTab.Host)
	exCloseHost(t, server)
	if errs.Len() > 0 || serverErrors.Len() > 0 {
		t.Fatalf("background errors: %v %v", errs.All(), serverErrors.All())
	}
}

// exFileStore opens a store over the SQLite file at path, as `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))`;
// the app owns it: the test closes it after the host.
func exFileStore(t testing.TB, path string) *store.ChordStore {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	must(t, err)
	opened, err := store.OpenStore(store.SqliteBackend(database), background, store.StoreOptions{})
	must(t, err)
	return opened.(*store.ChordStore)
}

// exCloseStore is `await store.close(context)`.
func exCloseStore(t testing.TB, st store.Store) {
	t.Helper()
	must(t, st.Close(background))
}
