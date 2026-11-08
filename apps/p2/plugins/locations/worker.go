package locations

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableharness "github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/locations/worker.ts: the location tools and the main agent's deferred move: `location_move` in the
// main conversation records the destination (`LocationMoveDoc`); once the session's worker has nothing running (the turn
// ended, no subagent work), it asks the server to move the session (`SessionMoves`, the same as the owner's Relocate). A
// refusal because work started meanwhile is retried after the next commit; a destination that became invalid is recorded
// as the move's failure. The record is Durable: a worker that stops first applies it after its next start.

// RetryAfter is how long to wait before trying a refused move again when no commit comes.
const RetryAfter = 2 * time.Second

var workerFacet = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.Contribute(facet, "p2.location-tools", registerTools); err != nil {
		return err
	}
	return p2runtime.Contribute(facet, "p2.location-mover", registerMover)
})

func ownerRequest(fields ...any) *delta.JsonObject {
	return delta.JsonObjectOf(append([]any{"origin", "owner"}, fields...)...)
}

// serverCall asks `AgentLocations` of the worker's server for method in a root of the worker's, under the tool's context.
func serverCall(worker func() (*p2runtime.WorkerParts, error), method string, args *delta.JsonObject, ctx context.Context) (*delta.JsonObject, error) {
	parts, err := worker()
	if err != nil {
		return nil, err
	}
	answered, err := parts.Root(func(root host.Ctx) (any, error) {
		locations, err := root.Get(contract.AgentLocations, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return locations.Call(method, args, root)
	}, ctx)
	if err != nil {
		return nil, err
	}
	reply, _ := answered.(*delta.JsonObject)
	return reply, nil
}

type toolServer struct {
	worker func() (*p2runtime.WorkerParts, error)
}

func (s toolServer) Envs(ctx context.Context) ([]EnvInfo, error) {
	reply, err := serverCall(s.worker, "envs", ownerRequest(), ctx)
	if err != nil {
		return nil, err
	}
	rows, _ := reply.Value("envs").([]any)
	envs := make([]EnvInfo, len(rows))
	for at, each := range rows {
		row := objectOf(each)
		env := EnvInfo{Name: stringOf(row.Value("name")), Type: stringOf(row.Value("type")), Host: stringOf(row.Value("host")), User: stringOf(row.Value("user"))}
		if port, ok := row.Value("port").(float64); ok {
			env.Port = &port
		}
		if tested := objectOf(row.Value("tested")); tested != nil {
			env.Tested = &EnvTested{OK: tested.Value("ok") == true, Platform: stringOf(tested.Value("platform")), Arch: stringOf(tested.Value("arch")), Error: stringOf(tested.Value("error"))}
		}
		envs[at] = env
	}
	return envs, nil
}

func (s toolServer) Directories(ctx context.Context, env *string, path string) (p2runtime.DirectoryListing, error) {
	var name any
	if env != nil {
		name = *env
	}
	reply, err := serverCall(s.worker, "directories", ownerRequest("env", name, "path", path), ctx)
	if err != nil {
		return p2runtime.DirectoryListing{}, err
	}
	listing := p2runtime.DirectoryListing{Path: stringOf(reply.Value("path")), Truncated: reply.Value("truncated") == true}
	if parent, ok := reply.Value("parent").(string); ok {
		listing.Parent = &parent
	}
	directories, _ := reply.Value("directories").([]any)
	for _, each := range directories {
		listing.Directories = append(listing.Directories, stringOf(each))
	}
	return listing, nil
}

func registerTools(self host.StoreSelf) error {
	worker := p2runtime.WorkerOf(self)
	harness := p2runtime.NativeOf[p2runtime.HarnessParts](self.Registry(p2runtime.HarnessNative), "The session's Harness is not open")
	extension, err := CreateExtension(Tools{
		Server: toolServer{worker: worker},
		Locate: func(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (p2runtime.Location, error) {
			parts, err := harness()
			if err != nil {
				return p2runtime.Location{}, err
			}
			environments, err := parts.Environments()
			if err != nil {
				return p2runtime.Location{}, err
			}
			return environments.Locate(ctx, id, read)
		},
	})
	if err != nil {
		return err
	}
	p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(ExtensionName, extension))
	return nil
}

func moveJSON(location p2runtime.Location) *delta.JsonObject {
	var env any
	if location.Env != nil {
		env = *location.Env
	}
	return delta.JsonObjectOf("cwd", location.Cwd, "env", env)
}

func registerMover(self host.StoreSelf) error {
	harness := p2runtime.HarnessOf(self)
	session := func() (durableharness.Harness, error) {
		parts, err := harness()
		if err != nil {
			return nil, err
		}
		return parts.Durable()
	}
	// Every commit while a move waits (a run may have ended), else only changes of the move's record.
	var waiting atomic.Bool
	commits := self.Open(
		func(context.Context) (any, error) {
			opened, err := session()
			if err != nil {
				return nil, err
			}
			return p2runtime.NewCommitSignal(opened, func(publication durable.CommitPublication) bool {
				if waiting.Load() {
					return true
				}
				for _, change := range publication.Changes {
					if typed, ok := change.(durable.DocumentChange); ok && typed.Record.Kind == contract.MoveKind {
						return true
					}
				}
				return false
			}), nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*p2runtime.CommitSignal).Close()
			return nil
		},
	)
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		value, err := commits.Value()
		if err != nil {
			return host.Threw(err)
		}
		signal := value.(*p2runtime.CommitSignal)
		return host.AwaitingCtx(ctx, func() error {
			for ctx.Err() == nil {
				seen := signal.Version()
				err := moveOnce(ctx, session, signal, seen, &waiting)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					fmt.Fprintln(os.Stderr, "p2 locations: the deferred move failed, retrying:", err)
					wait, stop := context.WithTimeout(ctx, RetryAfter)
					<-wait.Done()
					stop()
				}
			}
			return nil
		})
	})
	return nil
}

// waitForCommit waits for a commit after seen, or RetryAfter, whichever comes first.
func waitForCommit(ctx context.Context, signal *p2runtime.CommitSignal, seen int) {
	bounded, cancel := context.WithTimeout(ctx, RetryAfter)
	defer cancel()
	_ = signal.Next(bounded, seen)
}

// moveOnce applies the pending move when the session is idle, else waits for a reason to look again.
func moveOnce(ctx host.Ctx, session func() (durableharness.Harness, error), signal *p2runtime.CommitSignal, seen int, waiting *atomic.Bool) error {
	opened, err := session()
	if err != nil {
		return err
	}
	state, err := durable.Snapshot(ctx, opened, contract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return err
	}
	var pending *p2runtime.Location
	if state != nil {
		pending = subagentscontract.LocationOf(state.Pending)
	}
	waiting.Store(pending != nil)
	if pending == nil {
		return signal.Next(ctx, seen)
	}
	inspection, err := opened.Inspect(ctx)
	if err != nil {
		return err
	}
	if len(inspection.Tasks)+len(inspection.Submissions) > 0 {
		// Bounded too: a commit ending the run may have come before `waiting` was set.
		waitForCommit(ctx, signal, seen)
		return nil
	}
	var failure string
	locations, err := ctx.Get(contract.AgentLocations, delta.NewJsonObject(0), host.GetOptions{})
	if err == nil {
		var answered any
		if answered, err = locations.Call("move", ownerRequest("env", moveJSON(*pending).Value("env"), "cwd", pending.Cwd), ctx); err == nil {
			if objectOf(answered).Value("refused") != nil {
				// Work started meanwhile: once it ends.
				waitForCommit(ctx, signal, seen)
				return nil
			}
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		failure = err.Error()
	}
	if _, err := durable.Commit(ctx, opened, func(tx durable.Tx) (struct{}, error) {
		draft, err := durable.TxDoc(tx, contract.LocationMoveDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return struct{}{}, err
		}
		// A draft's object is a handle (*delta.Object), not a *delta.JsonObject: read it as the handle it is, or the move's
		// record is never cleared and the mover repeats the move (each repeat fences the worker).
		current := draft.Object("pending")
		// A newer destination waits for its own turn.
		if current == nil {
			return struct{}{}, nil
		}
		now := p2runtime.Location{}
		now.Cwd, _ = current.Get("cwd").(string)
		if name, ok := current.Get("env").(string); ok {
			now.Env = &name
		}
		if !p2runtime.SameLocation(now, *pending) {
			return struct{}{}, nil
		}
		draft.Delete("pending")
		if failure == "" {
			draft.Delete("failed")
			return struct{}{}, nil
		}
		return struct{}{}, draft.Set("failed", delta.JsonObjectOf("to", moveJSON(*pending), "error", failure))
	}); err != nil {
		return err
	}
	if failure != "" {
		fmt.Fprintln(os.Stderr, "p2 locations: moving the session failed:", failure)
	}
	return nil
}
