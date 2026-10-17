package ai

// Ports the read freshness of packages/coding-agent/src/core/auth-storage.ts readLatestData and models-store.ts readLatest for one model operation.

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

// fileReadSnapshot is one model operation's view of auth.json and models-store.json: the revision the operation last statted for each.
//
// Pi stats auth.json on every credential read, but an availability pass or a catalog restore with no extension provider makes all of its reads in
// one macrotask: nothing else in the process runs between them and no I/O completes during them, so an outside write lands before all of them or
// after all of them as far as any observer can tell. An operation therefore stats once. Its later reads answer from the published data while that
// data is still the version the operation statted; once a write in this process (Modify, Delete, Write) or a reload by another reader replaces it,
// the next read stats again. A read whose stored credentials contain a "!command" key runs that command, which can rewrite the files, so such a
// view is not reused: its reads stat as they do outside an operation.
type fileReadSnapshot struct {
	auth, models heldFile
	// onCaller marks an operation whose stores are this package's own (WithFileReadSnapshotOnCaller).
	onCaller bool
}

type fileReadSnapshotKey struct{}

// heldFile is the revision an operation last statted for one file. Its token admits one stat at a time, so concurrent first reads share one stat.
type heldFile struct {
	token chan struct{}
	held  atomic.Pointer[heldRevision]
}

// heldRevision is the read state a stat was published to and the revision it saw. The zero revision makes every read of that state stat.
type heldRevision struct {
	state    any
	revision fileRevision
}

// WithFileReadSnapshot starts one model operation's file-read snapshot: its auth.json and models-store.json reads stat once while no write
// replaces what they read. Only an operation that runs no extension code between its reads may carry one.
func WithFileReadSnapshot(ctx context.Context) context.Context {
	return withFileReadSnapshot(ctx, false)
}

// WithFileReadSnapshotOnCaller is WithFileReadSnapshot for an operation whose credential and models stores are this package's own (the file and
// in-memory ones): an offline one does no I/O but reads of their one view, so its providers may run one after another on the caller
// (ProvidersRunOnCaller). A caller-supplied store reads with the caller's code, which can wait on anything; Pi races each provider's refresh and
// check with the caller's signal (models.ts:586,605,684), so an operation over one keeps a goroutine per provider and settles when the caller
// cancels while its reads finish on their own.
func WithFileReadSnapshotOnCaller(ctx context.Context) context.Context {
	return withFileReadSnapshot(ctx, true)
}

func withFileReadSnapshot(ctx context.Context, onCaller bool) context.Context {
	return context.WithValue(ctx, fileReadSnapshotKey{}, &fileReadSnapshot{
		auth:     heldFile{token: make(chan struct{}, 1)},
		models:   heldFile{token: make(chan struct{}, 1)},
		onCaller: onCaller,
	})
}

// ProvidersRunOnCaller reports whether ctx belongs to an operation that carries a file-read snapshot over this package's own stores
// (WithFileReadSnapshotOnCaller), and so runs no extension code and reads no caller-supplied store.
func ProvidersRunOnCaller(ctx context.Context) bool {
	snapshot := fileReadSnapshotOf(ctx)
	return snapshot != nil && snapshot.onCaller
}

func fileReadSnapshotOf(ctx context.Context) *fileReadSnapshot {
	snapshot, _ := ctx.Value(fileReadSnapshotKey{}).(*fileReadSnapshot)
	return snapshot
}

// runsNoCommands reports whether reading these credentials runs no "!command" key.
func (d *authStorageData) runsNoCommands() bool {
	if d == nil {
		return true
	}
	for _, credential := range d.values {
		if credential.Type == CredentialAPIKey && configvalue.IsCommandConfigValue(credential.Key) {
			return false
		}
	}
	return true
}

// modelsHoldable holds a models-store.json view only after a held auth.json view: a credential read that ran a command may have rewritten the store.
func (s *fileReadSnapshot) modelsHoldable([]storedModels) bool {
	held := s.auth.held.Load()
	return held != nil && held.revision != (fileRevision{})
}

// readHeld answers from state's published data when it is still the revision hold last statted for state, and otherwise reads through read
// (one stat and, when the file changed, the shared reload) under hold's token and holds the revision read returned.
func readHeld[T any](ctx context.Context, hold *heldFile, state any, mu *sync.Mutex, published func() (fileRevision, T), read func(context.Context) (T, fileRevision, error), holdable func(T) bool, cancelled func(context.Context) error) (T, error) {
	held := hold.held.Load()
	if held != nil && held.state == state {
		if held.revision == (fileRevision{}) {
			data, _, err := read(ctx)
			return data, err
		}
		if data, ok := publishedAt(mu, published, held.revision); ok {
			return data, nil
		}
	}
	select {
	case hold.token <- struct{}{}:
	case <-ctx.Done():
		var zero T
		return zero, cancelled(ctx)
	}
	defer func() { <-hold.token }()
	// Another reader may have statted while this one waited.
	if again := hold.held.Load(); again != held && again != nil && again.state == state && again.revision != (fileRevision{}) {
		if data, ok := publishedAt(mu, published, again.revision); ok {
			return data, nil
		}
	}
	data, revision, err := read(ctx)
	if err != nil {
		return data, err
	}
	if !holdable(data) {
		revision = fileRevision{}
	}
	hold.held.Store(&heldRevision{state: state, revision: revision})
	return data, nil
}

func publishedAt[T any](mu *sync.Mutex, published func() (fileRevision, T), revision fileRevision) (T, bool) {
	mu.Lock()
	current, data := published()
	mu.Unlock()
	return data, current == revision
}
