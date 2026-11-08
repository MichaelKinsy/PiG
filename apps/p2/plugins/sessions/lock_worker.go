package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Ports apps/p2/plugins/sessions/lock-worker.ts: the session's lock on its worker: followed from the server's
// `SessionLock` and mirrored into the session's Durable state (`SessionLockDoc`), where the tool guard and the prompt
// read it at each use.

// lockMirror is the mirror (worker, host lifetime): no members, a routine.
var lockMirror = must(contracts.Local("p2.session-lock-mirror", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// MirrorLock commits lock (a JSON `SessionLock` or nil) as the session's lock unless it is that already.
func MirrorLock(ctx context.Context, session harness.Harness, lock delta.JsonValue) error {
	current, err := durable.Snapshot(ctx, session, p2runtime.SessionLockDoc)
	if err != nil {
		return fmt.Errorf("reading the session's lock: %w", err)
	}
	var have delta.JsonValue
	if current != nil && current.Lock != nil {
		have, err = lockJSON(*current.Lock)
		if err != nil {
			return err
		}
	}
	if sameJSON(have, lock) {
		return nil
	}
	_, err = session.Commit(ctx, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc(tx, p2runtime.SessionLockDoc)
		if err != nil {
			return nil, err
		}
		return nil, draft.Set("lock", lock)
	})
	if err != nil {
		return fmt.Errorf("mirroring the session's lock: %w", err)
	}
	return nil
}

// lockJSON is the lock as the server's view carries it.
func lockJSON(lock p2runtime.SessionLock) (delta.JsonValue, error) {
	data, err := json.Marshal(lock)
	if err != nil {
		return nil, err
	}
	var out *delta.JsonObject
	return out, json.Unmarshal(data, &out)
}

func sameJSON(a, b delta.JsonValue) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return slices.Equal(left, right)
}

// serveLockMirror serves the routine that follows the session's lock from the server.
func serveLockMirror(facet host.FacetHost) error {
	return facet.Serve(lockMirror, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		session := p2runtime.HarnessOf(self)
		view := self.Get(contract.SessionLock, delta.NewJsonObject(0))
		// Each change is committed after the one before it (a routine's calls never overlap), the newest value last.
		self.On(view.Member("lock"), func(call chord.WatchCall[delta.JsonValue], ctx host.Ctx) *host.Handled {
			return host.AwaitingCtx(ctx, func() error {
				parts, err := session()
				if err != nil {
					return err
				}
				durableSession, err := parts.Durable()
				if err != nil {
					return err
				}
				return MirrorLock(ctx, durableSession, call.Value)
			})
		})
		return contracts.Implementation{}, nil
	}), host.ServeOptions{})
}
