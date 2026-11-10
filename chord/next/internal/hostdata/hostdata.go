// Package hostdata is the seam between the store and the host for a host's data operations: import and delete, which
// Pi's `store/index.ts` barrel does not export ("keeps import and delete off the store's public surface: the host is the
// only way"). The store keeps its implementation unexported and registers it here in its `init`; the host calls it
// through this package. Being under `chord/next/internal`, only packages under `chord/next` can import it, so no
// consumer of the module reaches the operations.
//
// Go forced difference: the store cannot be imported here (it imports this package to register), so what the
// operations take of the store's own types (the store, the archive, the staged writes, the tables' definitions) is an
// `any` the store asserts back, and a value of another type fails the operation as a programming error. The host wraps
// each call in a typed function.
package hostdata

import (
	"context"
	"errors"
)

// Counts is what an import or a delete changed: the documents it created or retired, the rows it put or deleted. The
// store's `HostDataCounts` is this type.
type Counts struct {
	Documents int
	Rows      int
}

// Fence is the store's `CommitFence`: Admit is called at the commit step's entry (false rejects the commit `released`
// and writes nothing), Adopt after `COMMIT` with the ids of its created documents and the rows its writes changed.
type Fence struct {
	Admit func() bool
	Adopt func(seq int64, ids []int64, rows int) error
}

// Commit is how an operation commits: the host's fence and the writes staged after the operation's, in the same step
// (the store's `StagedWrite`s).
type Commit struct {
	Fence Fence
	After []any
}

// Plan is what an import does with an archive: under Target (canonical), Existing ("refuse" or "replace"), with the
// tables' histories (the store's `*TableDefinition`s, by SQL name).
type Plan struct {
	Target    string
	Existing  string
	Histories map[string]any
}

// Operations are the store's host data operations; every field is set. store is the store's `Store`, archive its
// `*HostArchive` (nil for a delete's owners).
type Operations struct {
	// TablesToMigrate is the archive's tables whose rows must migrate before they can be merged, by SQL name.
	TablesToMigrate func(store, archive any) []string
	// Owners is the owners whose data an operation on host key changes.
	Owners func(store any, key string, archive any, ctx context.Context) (map[string]struct{}, error)
	// Import imports archive into store in one commit.
	Import func(store, archive any, plan Plan, commit Commit, ctx context.Context) (Counts, error)
	// Delete deletes the data of host key from store in one commit.
	Delete func(store any, key string, commit Commit, ctx context.Context) (Counts, error)
}

var registered *Operations

// Register is the store's registration of its operations, in its `init`.
func Register(operations Operations) {
	registered = &operations
}

// ErrUnregistered is the panic value of a call before the store registered its operations: the host always has the store
// linked in, so this is a build error, never an operating condition.
var ErrUnregistered = errors.New("hostdata: the store registered no host data operations (is chord/next/store linked in?)")

func operations() *Operations {
	if registered == nil {
		panic(ErrUnregistered)
	}
	return registered
}

// TablesToMigrate is Operations.TablesToMigrate.
func TablesToMigrate(store, archive any) []string { return operations().TablesToMigrate(store, archive) }

// Owners is Operations.Owners.
func Owners(store any, key string, archive any, ctx context.Context) (map[string]struct{}, error) {
	return operations().Owners(store, key, archive, ctx)
}

// Import is Operations.Import.
func Import(store, archive any, plan Plan, commit Commit, ctx context.Context) (Counts, error) {
	return operations().Import(store, archive, plan, commit, ctx)
}

// Delete is Operations.Delete.
func Delete(store any, key string, commit Commit, ctx context.Context) (Counts, error) {
	return operations().Delete(store, key, commit, ctx)
}
