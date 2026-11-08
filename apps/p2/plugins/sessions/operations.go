package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	envcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagents "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports the lifecycle operations of apps/p2/plugins/sessions/server.ts: relocate, clone, fork and archive, each a method
// that answers once its visible effect committed and a job for its long part, shown in `Sessions.operations` while it
// runs and when it failed.

var (
	sstr = schema.Str
	snul = schema.Nul
)

func sunion(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }

func sobj(properties schema.Properties) schema.Schema { return schema.Obj(properties) }

// rerun: lifecycle jobs persist and run again after a crash: each run is idempotent (a copy starts over, a move
// repeats).
var rerun = &host.JobPersistence{Resume: host.ResumeRerun}

func mustJob(name string, options host.JobOptions) *host.JobDefinition {
	definition, err := host.DefineJob(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

var (
	relocateJob = mustJob("p2.sessions.relocate", host.JobOptions{
		Version: 1,
		Args: p2runtime.Contract(sobj(schema.Properties{
			schema.Prop("id", sstr()), schema.Prop("cwd", sstr()), schema.Prop("env", sunion(sstr(), snul())),
			// The owner's move (a refusal is a failed operation) or the agent's (`SessionMoves`: its tool reports it).
			schema.Prop("by", sunion(p2runtime.Literal("owner"), p2runtime.Literal("agent"))),
		})),
		Result:  p2runtime.Contract(sobj(schema.Properties{schema.Prop("refused", sunion(sstr(), snul()))})),
		Persist: rerun,
	})
	cloneJob = mustJob("p2.sessions.clone", host.JobOptions{
		Version: 1,
		Args:    p2runtime.Contract(sobj(schema.Properties{schema.Prop("id", sstr()), schema.Prop("copy", sstr())})),
		// Why a busy worker refused the copy (rolled back: a failed operation), else null.
		Result:  p2runtime.Contract(sobj(schema.Properties{schema.Prop("refused", sunion(sstr(), snul()))})),
		Persist: rerun,
	})
	// forkProgress is a fork's progress: copying its files (its worker waits), then seeding its history (through its
	// worker).
	forkProgress = mustState("p2.sessions.fork.progress", documents.StateOptions{
		Schema:         p2runtime.Contract(sobj(schema.Properties{schema.Prop("copying", schema.Bool())})),
		Version:        1,
		Initial:        func() *delta.JsonObject { return delta.JsonObjectOf("copying", true) },
		Persist:        true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return true },
	})
	forkJob = mustJob("p2.sessions.fork", host.JobOptions{
		Version: 1,
		// entries: the history the fork holds, read from the source when the fork was asked for.
		Args: p2runtime.Contract(sobj(schema.Properties{
			schema.Prop("id", sstr()), schema.Prop("copy", sstr()), schema.Prop("entries", schema.Arr(schema.Unknown())),
		})),
		Result:  p2runtime.Contract(snul()),
		State:   forkProgress,
		Persist: rerun,
	})
	archiveJob = mustJob("p2.sessions.archive", host.JobOptions{
		Version: 1,
		Args:    p2runtime.Contract(sobj(schema.Properties{schema.Prop("id", sstr())})),
		Result:  p2runtime.Contract(snul()),
		Persist: rerun,
	})
	// preparingState lists the copies whose row is being made (in memory): their worker waits like one whose files are being
	// copied.
	preparingState = mustState("p2.sessions.preparing", documents.StateOptions{
		Schema:  p2runtime.Contract(sobj(schema.Properties{schema.Prop("copies", schema.Arr(sstr()))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("copies", []any{}) },
	})
	// failedState holds the failed operations, until dismissed (the latest `maxFailed`).
	failedState = mustState("p2.sessions.failed", documents.StateOptions{
		Schema:  p2runtime.Contract(sobj(schema.Properties{schema.Prop("list", schema.Arr(contract.OperationSchema))})),
		Version: 1,
		Initial: func() *delta.JsonObject { return delta.JsonObjectOf("list", []any{}) },
	})
)

const maxFailed = 20

// rowWait is how long a worker start waits for its session's row (a client-chosen ID held before `create` answered).
const rowWait = 10 * time.Second

// failureText is why a job failed, in words.
func failureText(failure host.JobError) string {
	switch failure.Kind {
	case host.JobThrew:
		if failure.Error != nil {
			return failure.Error.Error()
		}
		return "The operation failed"
	case host.JobAborted:
		return "The operation was interrupted"
	}
	return fmt.Sprintf("The operation was stopped after %d restarts", failure.Restarts)
}

// catalogRef is the catalog as another object of the server uses it.
type catalogRef struct{ ref *host.Reference }

func (c catalogRef) rows() []any {
	value, _ := c.ref.Member("sessions").Value()
	rows, _ := value.([]any)
	return rows
}

// find is the row of session id, if the catalog has it.
func (c catalogRef) find(id string) (contract.Session, bool) {
	for _, row := range c.rows() {
		if session := contract.SessionFromJSON(row); session.ID == id {
			return session, true
		}
	}
	return contract.Session{}, false
}

func (c catalogRef) get(ctx context.Context, id string) (contract.Session, error) {
	row, err := c.ref.Call("get", delta.JsonObjectOf("id", id), ctx)
	return contract.SessionFromJSON(row), err
}

func (c catalogRef) update(ctx context.Context, id string, set ...any) (contract.Session, error) {
	args := delta.NewJsonObject(1 + len(set)/2)
	args.Set("id", id)
	for i := 0; i+1 < len(set); i += 2 {
		args.Set(set[i].(string), set[i+1])
	}
	row, err := c.ref.Call("update", args, ctx)
	return contract.SessionFromJSON(row), err
}

// location is where a session works.
type location struct {
	Cwd string
	Env *string
}

func (l location) json() *delta.JsonObject {
	if l.Env == nil {
		return delta.JsonObjectOf("cwd", l.Cwd, "env", nil)
	}
	return delta.JsonObjectOf("cwd", l.Cwd, "env", *l.Env)
}

// childName is the name of a child of a session: its subagent's name, or `#conversation`.
func childName(children *host.Reference, id string, conversation int) string {
	value, _ := children.Member("children").Value()
	all, _ := value.(*delta.JsonObject)
	if all != nil {
		list, _ := all.Value(id).([]any)
		for _, each := range list {
			if child, _ := each.(*delta.JsonObject); child != nil && int(numberOf(child.Value("conversationId"))) == conversation {
				return stringOf(child.Value("name"))
			}
		}
	}
	return fmt.Sprintf("#%d", conversation)
}

// fenceWorker fences the session's running worker for `doing` ("changing directory"): "" once fenced, else why it refused
// (it has work, or `to` is a move its `RelocationChecks` refuse; nothing fenced). ctx acts as the server (a local
// object's job or method): the worker's `WorkerBoundary` answers only the server.
func fenceWorker(catalog catalogRef, children *host.Reference, id, doing string, ctx host.Ctx, to *location) (string, error) {
	boundary, err := ctx.Get(p2runtime.WorkerBoundary, delta.JsonObjectOf("session", id), host.GetOptions{})
	if err != nil {
		return "", err
	}
	var args delta.JsonValue
	if to != nil {
		args = delta.JsonObjectOf("to", to.json())
	}
	answer, err := boundary.Call("prepare", args, ctx)
	if err != nil {
		return "", err
	}
	result, _ := answer.(*delta.JsonObject)
	if result.Value("accepted") == true {
		return "", nil
	}
	if refusal, ok := result.Value("refusal").(string); ok {
		return refusal, nil
	}
	row, err := catalog.get(ctx, id)
	if err != nil {
		return "", err
	}
	// A `/tree` branch left while running goes on in the background (docs/tree.md); conversation 1 is top-level before
	// the worker first reports `branches`.
	branchOf := func(conversation int) (contract.Branch, bool) {
		for _, branch := range row.Branches {
			if branch.Conversation == conversation {
				return branch, true
			}
		}
		return contract.Branch{}, false
	}
	topLevel := func(conversation int) bool {
		_, found := branchOf(conversation)
		return conversation == 1 || found
	}
	var branches, agents []string
	busyMain := false
	busy, _ := result.Value("busy").([]any)
	for _, each := range busy {
		conversation := int(numberOf(each))
		switch {
		case conversation == row.Main:
			busyMain = true
		case topLevel(conversation):
			label := row.Title
			if branch, found := branchOf(conversation); found && branch.Label != nil {
				label = *branch.Label
			}
			branches = append(branches, `"`+label+`"`)
		default:
			agents = append(agents, childName(children, id, conversation))
		}
	}
	switch {
	case busyMain:
		return "Stop the current run before " + doing, nil
	case len(branches) > 0:
		return fmt.Sprintf("Branch %s is still running: switch to it and stop it before %s", strings.Join(branches, ", "), doing), nil
	case len(agents) > 0:
		return fmt.Sprintf("Subagent %s is still running: stop it before %s", strings.Join(agents, ", "), doing), nil
	}
	return "Background work is still running (for example a message waiting for a subagent's answer): wait for it or stop the subagent before " + doing, nil
}

const backgroundWork = "Background work is still running"

// operationRunning is a running operation's entry.
func operationRunning(id, kind, session string, target *string, copying bool) *delta.JsonObject {
	entry := delta.JsonObjectOf("phase", "running", "id", id, "kind", kind, "session", session)
	if target == nil {
		entry.Set("target", nil)
	} else {
		entry.Set("target", *target)
	}
	entry.Set("copying", copying)
	return entry
}

// expandHome is `~` and `~/…` on this machine.
func expandHome(cwd string) string {
	home, err := os.UserHomeDir()
	switch {
	case err != nil:
		return cwd
	case cwd == "~":
		return home
	case strings.HasPrefix(cwd, "~/"):
		return filepath.Join(home, cwd[2:])
	}
	return cwd
}

// withKey is the options of a start that dedupes by key.
func withKey(key string) host.StartOptions { return host.StartOptions{Key: &key} }

func argsOf(outcome host.Outcome) *delta.JsonObject { return objectArgs(outcome.Args) }

// serveOperations serves SessionOperations: the lifecycle jobs and their views. A host-lifetime object, so its job runs
// persist.
func serveOperations(facet host.FacetHost) error {
	return facet.Serve(contract.SessionOperations, host.LocalFactory(func(_ *delta.JsonObject, raw host.Self) (contracts.Implementation, error) {
		self := raw.(host.StoreSelf)
		catalog := catalogRef{self.Get(contract.Catalog, delta.NewJsonObject(0))}
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		natives := p2runtime.ServerOf(self)
		self.Get(envcontract.EnvService, delta.NewJsonObject(0))
		envs := envcontract.EnvManagersOf(self)
		children := self.Get(subagents.ChildStore, delta.NewJsonObject(0))
		archivedStarts := self.Get(contract.ArchivedStarts, delta.NewJsonObject(0))
		envService := self.Get(envcontract.EnvService, delta.NewJsonObject(0))
		archived := self.Registry(contract.SessionArchived.Registry)
		copied := self.Registry(contract.SessionCopied.Registry)
		failed := self.State(failedState).(*documents.Singleton)
		preparing := self.State(preparingState).(*documents.Singleton)
		// The jobs below read these registries off the host's loop: start them here, in the object's own section, so
		// that a job's first read only reads (a registry's first read composes it).
		_, _ = contract.SessionArchived.Table(archived)
		_, _ = contract.SessionCopied.Table(copied)

		dataDir := func() string {
			info, _ := server.Member("info").Value()
			return stringOf(objectArgs(info).Value("dataDir"))
		}
		workers := func() (p2runtime.SessionWorkerControl, error) {
			parts, err := natives()
			if err != nil {
				return nil, err
			}
			return parts.Workers, nil
		}

		// recordFailure keeps a finished run that failed (a refused owner's move too) in `operations` until dismissed.
		recordFailure := func(tx documents.Tx, id, kind, session string, target *string, text string) error {
			draft, err := tx.Draft(failed)
			if err != nil {
				return err
			}
			list := draft.Array("list")
			entry := delta.JsonObjectOf("phase", "failed", "id", id, "kind", kind, "session", session)
			if target == nil {
				entry.Set("target", nil)
			} else {
				entry.Set("target", *target)
			}
			entry.Set("error", text)
			if _, err := list.Push(entry); err != nil {
				return err
			}
			if extra := list.Len() - maxFailed; extra > 0 {
				_, err = list.Splice(0, extra)
			}
			return err
		}

		// failCopy: a failed copy has its row archived first (so its worker cannot start), its worker stopped, its files gone.
		failCopy := func(ctx context.Context, copy string) error {
			if _, found := catalog.find(copy); found {
				if _, err := catalog.update(ctx, copy, "archived", true); err != nil {
					return err
				}
			}
			control, err := workers()
			if err != nil {
				return err
			}
			if err := control.Suspend(ctx, copy); err != nil {
				return err
			}
			return RemoveSessionCopy(dataDir(), copy)
		}

		// relocate moves session `id` to `to`: a stopped worker only gets the catalog update; a running one is fenced, the
		// catalog moves and the worker follows in place (restarted from the catalog when it cannot). It returns why a busy
		// worker refused (nothing moved), else "".
		relocate := func(id string, to location, ctx host.Ctx) (string, error) {
			control, err := workers()
			if err != nil {
				return "", err
			}
			refused := ""
			boundary := func() (*host.Reference, error) {
				return ctx.Get(p2runtime.WorkerBoundary, delta.JsonObjectOf("session", id), host.GetOptions{})
			}
			err = control.Relocate(ctx, id, p2runtime.RelocationSteps{
				Prepare: func(context.Context) (string, error) {
					refusal, err := fenceWorker(catalog, children, id, "changing directory", ctx, &to)
					refused = refusal
					return refusal, err
				},
				Update: func(context.Context) error {
					_, err := catalog.update(ctx, id, "cwd", to.Cwd, "env", to.json().Value("env"))
					return err
				},
				Move: func(context.Context) error {
					held, err := boundary()
					if err != nil {
						return err
					}
					_, err = held.Call("relocate", to.json(), ctx)
					return err
				},
				Cancel: func(context.Context) error {
					held, err := boundary()
					if err != nil {
						return err
					}
					_, err = held.Call("cancel", nil, ctx)
					return err
				},
			})
			if err != nil {
				// A busy worker (or one starting or stopping): nothing moved.
				if refused != "" {
					return refused, nil
				}
				//portlint:allow erroridentity Pi matches the message too (sessions/server.ts: startsWith("Background work is still running"))
				if strings.HasPrefix(err.Error(), backgroundWork) {
					return err.Error(), nil
				}
				return "", err
			}
			return "", nil
		}

		refusedResult := func(refused string) delta.JsonValue {
			if refused == "" {
				return delta.JsonObjectOf("refused", nil)
			}
			return delta.JsonObjectOf("refused", refused)
		}

		relocations := self.Job(relocateJob, host.JobImplementation{
			Run: func(args any, ctx host.JobCtx) (any, error) {
				in := args.(*delta.JsonObject)
				refused, err := relocate(stringOf(in.Value("id")), location{Cwd: stringOf(in.Value("cwd")), Env: optionalString(in.Value("env"))}, ctx)
				return refusedResult(refused), err
			},
			Finish: func(outcome host.Outcome, tx documents.Tx) error {
				in := argsOf(outcome)
				var text string
				switch {
				case outcome.Kind == host.OutcomeFailed:
					text = failureText(outcome.Error)
				case in.Value("by") == "owner":
					text = stringOf(objectArgs(outcome.Result).Value("refused"))
				}
				if text == "" {
					return nil
				}
				return recordFailure(tx, outcome.ID, "relocate", stringOf(in.Value("id")), nil, text)
			},
		})

		clones := self.Job(cloneJob, host.JobImplementation{
			Run: func(args any, ctx host.JobCtx) (any, error) {
				in := args.(*delta.JsonObject)
				id, copy := stringOf(in.Value("id")), stringOf(in.Value("copy"))
				control, err := workers()
				if err != nil {
					return nil, err
				}
				refused := ""
				// The store is copied while the worker is stopped: SQLite's file and WAL belong together then.
				err = control.Pause(ctx, id,
					func(context.Context) (string, error) {
						refusal, err := fenceWorker(catalog, children, id, "cloning", ctx, nil)
						refused = refusal
						return refusal, err
					},
					func(work context.Context) error {
						if err := RemoveSessionCopy(dataDir(), copy); err != nil {
							return err
						}
						return CopySessionDirectory(work, dataDir(), id, copy)
					}, "cloning")
				if err == nil {
					// A move while the clone waited for the pause belongs to the copy too.
					source, err1 := catalog.get(ctx, id)
					row, err2 := catalog.get(ctx, copy)
					if err := errors.Join(err1, err2); err != nil {
						return nil, errors.Join(err, failCopy(ctx, copy))
					}
					if source.Cwd != row.Cwd || !sameEnv(source.Env, row.Env) {
						if _, err := catalog.update(ctx, copy, "cwd", source.Cwd, "env", location{Env: source.Env}.json().Value("env")); err != nil {
							return nil, errors.Join(err, failCopy(ctx, copy))
						}
					}
					return refusedResult(""), nil
				}
				failure := failCopy(ctx, copy)
				// A busy worker (or one starting or stopping) is an answer, not a failure of the job.
				if refused != "" {
					return refusedResult(refused), failure
				}
				//portlint:allow erroridentity Pi matches the message too (sessions/server.ts: startsWith("Background work is still running"))
				if strings.HasPrefix(err.Error(), backgroundWork) {
					return refusedResult(err.Error()), failure
				}
				return nil, errors.Join(err, failure)
			},
			Finish: func(outcome host.Outcome, tx documents.Tx) error {
				text := ""
				if outcome.Kind == host.OutcomeFailed {
					text = failureText(outcome.Error)
				} else {
					text = stringOf(objectArgs(outcome.Result).Value("refused"))
				}
				if text == "" {
					return nil
				}
				in := argsOf(outcome)
				return recordFailure(tx, outcome.ID, "clone", stringOf(in.Value("id")), optionalString(in.Value("copy")), text)
			},
		})

		forks := self.Job(forkJob, host.JobImplementation{
			Run: func(args any, ctx host.JobCtx) (any, error) {
				in := args.(*delta.JsonObject)
				id, copy := stringOf(in.Value("id")), stringOf(in.Value("copy"))
				err := func() error {
					progress, err := ctx.State().Value()
					if err != nil {
						return err
					}
					if progress.Value("copying") == true {
						if err := RemoveSessionCopy(dataDir(), copy); err != nil {
							return err
						}
						// The source's files, copied by its worker (its store is open there; it goes on meanwhile).
						transfer, err := ctx.Get(contract.SessionTransfer, delta.JsonObjectOf("session", id), host.GetOptions{})
						if err != nil {
							return err
						}
						if _, err := transfer.Call("copyFiles", delta.JsonObjectOf("to", copy), ctx); err != nil {
							return err
						}
						if _, err := ctx.State().Change(ctx, func(draft *delta.Object) (any, error) {
							return nil, draft.Set("copying", false)
						}, host.ChangeOptions{}); err != nil {
							return err
						}
					}
					// The copy's worker starts now (this hold, or a page that navigated to it) and writes the history.
					target, err := ctx.Get(contract.SessionTransfer, delta.JsonObjectOf("session", copy), host.GetOptions{})
					if err != nil {
						return err
					}
					_, err = target.Call("seed", delta.JsonObjectOf("entries", in.Value("entries")), ctx)
					return err
				}()
				if err != nil {
					return nil, errors.Join(err, failCopy(ctx, copy))
				}
				return nil, nil
			},
			Finish: func(outcome host.Outcome, tx documents.Tx) error {
				if outcome.Kind != host.OutcomeFailed {
					return nil
				}
				in := argsOf(outcome)
				return recordFailure(tx, outcome.ID, "fork", stringOf(in.Value("id")), optionalString(in.Value("copy")), failureText(outcome.Error))
			},
		})

		archives := self.Job(archiveJob, host.JobImplementation{
			Run: func(args any, ctx host.JobCtx) (any, error) {
				id := stringOf(args.(*delta.JsonObject).Value("id"))
				// Archive stops the worker whatever it is doing; its storage and history stay. Its holders (a page's lingering
				// conversation) wait for a restore rather than start a worker the archive refuses.
				table, err := contract.SessionArchived.Table(archived)
				if err != nil {
					return nil, err
				}
				for _, key := range table.Keys() {
					close, _ := table.Get(key)
					if err := close(ctx, id); err != nil {
						return nil, err
					}
				}
				control, err := workers()
				if err != nil {
					return nil, err
				}
				return nil, control.Suspend(ctx, id)
			},
			Finish: func(outcome host.Outcome, tx documents.Tx) error {
				if outcome.Kind != host.OutcomeFailed {
					return nil
				}
				return recordFailure(tx, outcome.ID, "archive", stringOf(argsOf(outcome).Value("id")), nil, failureText(outcome.Error))
			},
		})

		operations := self.Derive([]any{relocations.Running(), clones.Running(), forks.Running(), archives.Running(), failed},
			func(values []any) (delta.JsonValue, error) {
				list := []any{}
				each := func(source any, make func(entry *delta.JsonObject, args *delta.JsonObject) *delta.JsonObject) {
					entries, _ := source.([]any)
					for _, value := range entries {
						entry, _ := value.(*delta.JsonObject)
						if entry.Value("phase") == "blocked" {
							continue
						}
						if operation := make(entry, objectArgs(entry.Value("args"))); operation != nil {
							list = append(list, operation)
						}
					}
				}
				each(values[0], func(entry, args *delta.JsonObject) *delta.JsonObject {
					if args.Value("by") == "agent" {
						return nil
					}
					return operationRunning(stringOf(entry.Value("id")), "relocate", stringOf(args.Value("id")), nil, false)
				})
				each(values[1], func(entry, args *delta.JsonObject) *delta.JsonObject {
					return operationRunning(stringOf(entry.Value("id")), "clone", stringOf(args.Value("id")), optionalString(args.Value("copy")), true)
				})
				each(values[2], func(entry, args *delta.JsonObject) *delta.JsonObject {
					copying := objectArgs(entry.Value("state")).Value("copying") == true
					return operationRunning(stringOf(entry.Value("id")), "fork", stringOf(args.Value("id")), optionalString(args.Value("copy")), copying)
				})
				each(values[3], func(entry, args *delta.JsonObject) *delta.JsonObject {
					return operationRunning(stringOf(entry.Value("id")), "archive", stringOf(args.Value("id")), nil, false)
				})
				stored, _ := objectArgs(values[4]).Value("list").([]any)
				return append(list, stored...), nil
			})
		// Sessions whose worker must not start yet: a copy's row being made, or its files being copied.
		copying := self.Derive([]any{preparing, operations}, func(values []any) (delta.JsonValue, error) {
			ids := []any{}
			made, _ := objectArgs(values[0]).Value("copies").([]any)
			ids = append(ids, made...)
			list, _ := values[1].([]any)
			for _, value := range list {
				operation, _ := value.(*delta.JsonObject)
				if operation.Value("phase") == "running" && operation.Value("copying") == true && operation.Value("target") != nil {
					ids = append(ids, operation.Value("target"))
				}
			}
			return ids, nil
		})

		// gate tells the starts of workers when their session is no copy being made: `copying` is followed in a routine of
		// this object (a derive is not safe to watch from outside its sections).
		gate := newCopyGate()
		self.On(copying, func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
			ids, _ := call.Value.([]any)
			gate.set(ids)
			return nil
		})

		// destination is a destination as the move stores it: `~` expanded and checked to be a directory (on its
		// environment).
		destination := func(ctx context.Context, target location) (location, error) {
			if target.Env != nil && *target.Env != "" {
				parts, err := envs()
				if err != nil {
					return location{}, err
				}
				cwd, err := parts.Manager.WorkingDirectory(ctx, *target.Env, target.Cwd, false)
				return location{Cwd: cwd, Env: target.Env}, err
			}
			local, err := filepath.Abs(expandHome(target.Cwd))
			if err != nil {
				return location{}, err
			}
			info, err := os.Stat(local)
			if err != nil {
				return location{}, nodeerrno.FromPathError(err)
			}
			if !info.IsDir() {
				return location{}, errors.New("Working directory is not a directory")
			}
			return location{Cwd: local}, nil
		}
		liveSession := func(ctx context.Context, id string) (contract.Session, error) {
			session, err := catalog.get(ctx, id)
			if err == nil && session.ArchivedAt != nil {
				return session, errors.New("Session is archived")
			}
			return session, err
		}
		// copyOf is a copy's catalog entry: the source's place, model and level under a marked title.
		copyOf := func(ctx context.Context, source contract.Session, id, mark string) error {
			title := cleanTitle(source.Title + " (" + mark + ")")
			title = utf16Slice(title, contract.MaxTitle)
			args := delta.JsonObjectOf("id", id, "cwd", source.Cwd, "env", location{Env: source.Env}.json().Value("env"), "title", title,
				"titleEdited", true, "model", delta.JsonObjectOf("provider", source.Model.Provider, "modelId", source.Model.ModelId),
				"thinkingLevel", source.ThinkingLevel)
			_, err := catalog.ref.Call("create", args, ctx)
			return err
		}
		// addPreparing and removePreparing change the list of copies being made.
		addPreparing := func(ctx context.Context, copy string) error {
			_, err := preparing.Change(ctx, func(draft *delta.Object) (any, error) { return draft.Array("copies").Push(copy) })
			return err
		}
		removePreparing := func(ctx context.Context, copy string) error {
			_, err := preparing.Change(ctx, func(draft *delta.Object) (any, error) {
				copies := draft.Array("copies")
				for i := copies.Len() - 1; i >= 0; i-- {
					if copies.Get(i) == copy {
						if _, err := copies.Splice(i, 1); err != nil {
							return nil, err
						}
					}
				}
				return nil, nil
			})
			return err
		}
		// startCopy makes a copy's row and starts the job filling it, its worker held back meanwhile. A copy that exists
		// already is a retry of the same request: its row is the answer.
		startCopy := func(ctx context.Context, source contract.Session, copy, mark string, start func() error) (delta.JsonValue, error) {
			if existing, found := catalog.find(copy); found {
				return existing.JSON(), nil
			}
			if err := addPreparing(ctx, copy); err != nil {
				return nil, err
			}
			defer func() { _ = removePreparing(chordctx.WithoutAbortSignal(ctx), copy) }()
			err := copyOf(ctx, source, copy, mark)
			if err == nil {
				if table, tableErr := contract.SessionCopied.Table(copied); tableErr != nil {
					err = tableErr
				} else {
					for _, key := range table.Keys() {
						take, _ := table.Get(key)
						if err = take(ctx, source.ID, copy); err != nil {
							break
						}
					}
				}
			}
			if err == nil {
				err = start()
			}
			if err != nil {
				return nil, errors.Join(err, failCopy(ctx, copy))
			}
			row, _ := catalog.find(copy)
			return row.JSON(), nil
		}

		// The start data of a session's worker; refuses archived and unknown sessions. A copy's worker waits until its
		// files are there; a client-chosen ID held before its row exists waits for the row.
		startData := func(ctx context.Context, id string) (delta.JsonValue, error) {
			session, err := catalog.get(ctx, id)
			if err != nil {
				return nil, err
			}
			// An archived session starts only to rebuild its index, and then without resuming its work.
			allowed, _ := archivedStarts.Member("allowed").Value()
			indexing := false
			if session.ArchivedAt != nil {
				list, _ := allowed.([]any)
				for _, each := range list {
					indexing = indexing || each == id
				}
				if !indexing {
					return nil, p2runtime.Forbidden("Session is archived")
				}
			}
			if session.Env != nil {
				configs, _ := envService.Member("configs").Value()
				if !objectArgs(configs).Has(*session.Env) {
					return nil, fmt.Errorf("Environment %q is not configured; refusing local execution", *session.Env)
				}
			}
			init := delta.JsonObjectOf("id", session.ID, "cwd", session.Cwd, "env", location{Env: session.Env}.json().Value("env"),
				"model", delta.JsonObjectOf("provider", session.Model.Provider, "modelId", session.Model.ModelId),
				"thinkingLevel", session.ThinkingLevel)
			if indexing {
				init.Set("resume", false)
			}
			return init, nil
		}
		p2runtime.ContributeKeyed(self, p2runtime.WorkerInitValues, p2runtime.Entry("session", p2runtime.WorkerInitContribution(
			func(id string, ctx context.Context) (delta.JsonValue, error) {
				waiting, cancel := context.WithTimeout(ctx, rowWait)
				defer cancel()
				_, _ = p2runtime.Until(waiting, catalog.ref.Member("sessions"), func(value delta.JsonValue) bool {
					rows, _ := value.([]any)
					for _, row := range rows {
						if contract.SessionFromJSON(row).ID == id {
							return true
						}
					}
					return false
				})
				if err := gate.wait(ctx, id); err != nil {
					return nil, err
				}
				return startData(ctx, id)
			})))

		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"operations": operations},
			Methods: map[string]contracts.MethodFunction{
				"move": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					id := stringOf(in.Value("id"))
					if _, err := liveSession(ctx, id); err != nil {
						return nil, err
					}
					target, err := destination(ctx, location{Cwd: stringOf(in.Value("cwd")), Env: optionalString(in.Value("env"))})
					if err != nil {
						return nil, err
					}
					run, err := relocations.Start(delta.JsonObjectOf("id", id, "cwd", target.Cwd, "env", target.json().Value("env"), "by", "agent"), ctx, host.StartOptions{})
					if err != nil {
						return nil, err
					}
					outcome, err := run.Outcome(ctx)
					if err != nil {
						return nil, err
					}
					finished := outcome.(host.Outcome)
					if finished.Kind == host.OutcomeFailed {
						return nil, errors.New(failureText(finished.Error))
					}
					return finished.Result.(delta.JsonValue), nil
				},
				"relocate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					id := stringOf(in.Value("id"))
					if _, err := liveSession(ctx, id); err != nil {
						return nil, err
					}
					target, err := destination(ctx, location{Cwd: stringOf(in.Value("cwd")), Env: optionalString(in.Value("env"))})
					if err != nil {
						return nil, err
					}
					_, err = relocations.Start(delta.JsonObjectOf("id", id, "cwd", target.Cwd, "env", target.json().Value("env"), "by", "owner"), ctx, host.StartOptions{})
					return nil, err
				},
				"clone": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					id, copy := stringOf(in.Value("id")), stringOf(in.Value("copy"))
					source, err := liveSession(ctx, id)
					if err != nil {
						return nil, err
					}
					return startCopy(ctx, source, copy, "copy", func() error {
						_, err := clones.Start(delta.JsonObjectOf("id", id, "copy", copy), ctx, withKey(copy))
						return err
					})
				},
				"fork": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					in := objectArgs(args)
					id, copy := stringOf(in.Value("id")), stringOf(in.Value("copy"))
					source, err := liveSession(ctx, id)
					if err != nil {
						return nil, err
					}
					// Read now, so a fork of a subagent or of an unknown message is refused before anything is made. This local
					// object's `ctx.Get` acts as the server, whom `SessionTransfer` serves.
					rooted, ok := ctx.(host.Ctx)
					if !ok {
						return nil, errors.New("fork: no call context")
					}
					transfer, err := rooted.Get(contract.SessionTransfer, delta.JsonObjectOf("session", id), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					history, err := transfer.Call("history", delta.JsonObjectOf("conversation", in.Value("conversation"), "before", in.Value("before")), ctx)
					if err != nil {
						return nil, err
					}
					entries := objectArgs(history).Value("entries")
					return startCopy(ctx, source, copy, "fork", func() error {
						_, err := forks.Start(delta.JsonObjectOf("id", id, "copy", copy, "entries", entries), ctx, withKey(copy))
						return err
					})
				},
				"archive": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					id := stringOf(objectArgs(args).Value("id"))
					if _, err := catalog.update(ctx, id, "archived", true); err != nil {
						return nil, err
					}
					_, err := archives.Start(delta.JsonObjectOf("id", id), ctx, withKey(id))
					return nil, err
				},
				"restore": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					id := stringOf(objectArgs(args).Value("id"))
					// After a running archive of it: its worker stop must not land after the restore.
					value, _ := archives.Running().Value()
					running, _ := value.([]any)
					for _, value := range running {
						entry, _ := value.(*delta.JsonObject)
						if entry.Value("phase") != "blocked" && entry.Value("key") == id {
							_, _ = archives.Outcome(stringOf(entry.Value("id")), ctx)
						}
					}
					if _, err := catalog.update(ctx, id, "archived", false); err != nil {
						return nil, err
					}
					// A start refused while archived left the worker failed or suspended: its holders start it again.
					control, err := workers()
					if err != nil {
						return nil, err
					}
					return nil, control.Resume(ctx, id)
				},
				"dismiss": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					operation := objectArgs(args).Value("operation")
					_, err := failed.Change(ctx, func(draft *delta.Object) (any, error) {
						list := draft.Array("list")
						for i := list.Len() - 1; i >= 0; i-- {
							if entry := list.Object(i); entry != nil && entry.Get("id") == operation {
								if _, err := list.Splice(i, 1); err != nil {
									return nil, err
								}
							}
						}
						return nil, nil
					})
					return nil, err
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func sameEnv(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// serveMoves serves SessionMoves: the agent's relocation, through the operations' `move`.
func serveMoves(facet host.FacetHost) error {
	return facet.Serve(contract.SessionMoves, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		operations := self.Get(contract.SessionOperations, delta.NewJsonObject(0))
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Methods: map[string]contracts.MethodFunction{
				"relocate": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return operations.Call("move", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// copyGate is the sessions that are copies being made, as the object's routine last saw them, and the starts waiting
// for one to be done.
type copyGate struct {
	mu      sync.Mutex
	ids     map[string]struct{}
	waiting map[string][]chan struct{}
}

func newCopyGate() *copyGate {
	return &copyGate{ids: map[string]struct{}{}, waiting: map[string][]chan struct{}{}}
}

// set replaces the copies being made and wakes the starts of the sessions that are not any more.
func (g *copyGate) set(ids []any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ids = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if text, ok := id.(string); ok {
			g.ids[text] = struct{}{}
		}
	}
	for id, waiters := range g.waiting {
		if _, copying := g.ids[id]; !copying {
			for _, waiter := range waiters {
				close(waiter)
			}
			delete(g.waiting, id)
		}
	}
}

// wait blocks until session id is no copy being made, or ctx ends.
func (g *copyGate) wait(ctx context.Context, id string) error {
	chordctx.Suspend(ctx)
	g.mu.Lock()
	if _, copying := g.ids[id]; !copying {
		g.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	g.waiting[id] = append(g.waiting[id], done)
	g.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
