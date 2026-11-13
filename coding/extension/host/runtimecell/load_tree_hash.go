package runtimecell

import (
	"context"
	"sync"
)

// loadTreeHashes holds hashTree of each SDK tree that the cells of one load have asked for.
type loadTreeHashes struct {
	mu     sync.Mutex
	byRoot map[string]*loadTreeHashEntry
}

type loadTreeHashEntry struct {
	once sync.Once
	hash string
}

type loadTreeHashesKey struct{}

// WithLoadTreeHashes returns the context of one load of extension cells. The cache key of a packed cell covers the whole SDK tree, and a load
// asks for one key for each of its cells. Under this context the lookups of cells that are already built read each SDK tree once between them
// and share its hash, the one each lookup would have computed, so a start reads the SDK once and not once for each cell. The reading lasts as
// long as the load: the next load reads the tree again, and a cell that has to build hashes the tree under its build lock and not from here.
func WithLoadTreeHashes(ctx context.Context) context.Context {
	return context.WithValue(ctx, loadTreeHashesKey{}, &loadTreeHashes{byRoot: map[string]*loadTreeHashEntry{}})
}

// loadTreeHash is hashTree(root) as the load that ctx belongs to read it, which the first caller of the load does and the others wait for.
// Outside a load it is hashTree(root).
func loadTreeHash(ctx context.Context, root string) string {
	hashes, _ := ctx.Value(loadTreeHashesKey{}).(*loadTreeHashes)
	if hashes == nil {
		return hashTree(root)
	}
	hashes.mu.Lock()
	entry := hashes.byRoot[root]
	if entry == nil {
		entry = &loadTreeHashEntry{}
		hashes.byRoot[root] = entry
	}
	hashes.mu.Unlock()
	entry.once.Do(func() { entry.hash = hashTree(root) })
	return entry.hash
}
