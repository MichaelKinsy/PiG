package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Splitting a store archive by owner (D462).
//
// A Pi host has ONE store, so `store.archive` of an archive Pi made holds every owner's tables and documents together. A PiG host has
// two (a chord store gives out the document ids of its file, so two engines cannot share one): the NodeHost's `store.sqlite` for the plugins
// and the Go kit's `kit.sqlite` for what the kit owns. An import routes the parts of `store.archive` by owner, as Pi's own `files-copy.ts`
// does for the owner `chord.app.files` (`table.startsWith("chord.app.files:")` at :46, `record.owner === OWNER` at :69-86, the documents'
// revisions by the remembered ids, `end` counted again): the kit's parts into `kit.sqlite`, the rest into `store.sqlite`, each half the
// archive's manifest, its own parts and an `end` counting them (a valid archive of its own).

// kitOwners are the owners whose tables and documents live in the Go kit's store (`kit.sqlite`): every kit object that has a store on a
// worker (`Files`). Everything else is a plugin's and lives in the NodeHost's.
var kitOwners = []string{filesName}

// ownerSet says whether an owner belongs to the half an archive is split for: the kit's store (kitOwned) or, in a host's data
// archive, the server's Go host (`Host.Serves`).
type ownerSet func(owner string) bool

// kitOwned is the ownerSet of the kit's store.
var kitOwned ownerSet = func(owner string) bool { return slices.Contains(kitOwners, owner) }

// owns reports whether part belongs to the owners of the set: a table's write by its name (`<owner>:<table>`), a document's record by its
// owner, a revision by the id of a document of the set (documents). The manifest and `end` belong to neither.
func (owners ownerSet) owns(part any, documents map[int64]struct{}) bool {
	switch part := part.(type) {
	case *store.TableDefinitionWrite:
		return owners(tableOwner(part.Table))
	case *store.PutWrite:
		return owners(tableOwner(part.Table))
	case *store.IndexWrite:
		return owners(tableOwner(part.Table))
	case *store.DocumentCreate:
		return owners(part.Record.Owner)
	case *store.DocumentRevisionWrite:
		_, ok := documents[part.ID]
		return ok
	}
	return false
}

// tableOwner is the owner of the table named table (`<owner>:<table>`; "" for a name without one).
func tableOwner(table string) string {
	owner, _, _ := strings.Cut(table, ":")
	return owner
}

// splitCensus is what one read of an archive says that no half can: the ids of the owned half's documents, and the writes of the owned
// half.
type splitCensus struct {
	documents map[int64]struct{}
	writes    int64
}

// countSplit reads the archive at archive once and checks what splitting by owners could drop: no document id twice (each half is checked
// alone), and the writes of the two halves adding up to the `end` the archive declared.
func countSplit(ctx context.Context, archive string, owners ownerSet) (splitCensus, error) {
	census := splitCensus{documents: map[int64]struct{}{}}
	source, err := openParts(archive)
	if err != nil {
		return census, err
	}
	defer func() { _ = source.Close() }()
	ids := map[int64]struct{}{}
	var restWrites int64
	declared := int64(-1)
	for {
		part, ok, err := source.Next(ctx)
		if err != nil {
			return census, err
		}
		if !ok {
			break
		}
		switch part := part.(type) {
		case *store.ManifestPart:
			continue
		case *store.EndPart:
			declared = part.Writes
			continue
		case *store.DocumentCreate:
			if _, twice := ids[part.Record.ID]; twice {
				return census, data.Invalid(data.ReasonArgs, fmt.Sprintf("import: id %d occurs twice", part.Record.ID))
			}
			ids[part.Record.ID] = struct{}{}
			if owners.owns(part, nil) {
				census.documents[part.Record.ID] = struct{}{}
			}
		}
		if owners.owns(part, census.documents) {
			census.writes++
		} else {
			restWrites++
		}
	}
	if declared >= 0 && declared != census.writes+restWrites {
		return census, data.Invalid(data.ReasonArgs, fmt.Sprintf("import: end counts %d writes, the archive has %d", declared, census.writes+restWrites))
	}
	return census, nil
}

// openParts is the typed parts of the JSONL store archive at path, each checked against the archive schema as ImportStore checks it (a
// trust boundary: the decoder yields JSON values, and the split must read the owner of what it routes).
func openParts(path string) (store.PartSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &checkedParts{source: store.DecodeJsonl(&fileBytes{file: file})}, nil
}

// checkedParts is a JSONL source whose values are checked and decoded into typed parts.
type checkedParts struct {
	source   store.PartSource
	position int
}

func (parts *checkedParts) Next(ctx context.Context) (any, bool, error) {
	value, ok, err := parts.source.Next(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	part, err := store.CheckPart(value, parts.position, ctx)
	parts.position++
	return part, err == nil, err
}

func (parts *checkedParts) Close() error { return parts.source.Close() }

// splitParts is one half of an archive: the manifest, the parts of the owners (owned) or of everyone else, and an `end` counting them.
type splitParts struct {
	source    store.PartSource
	owners    ownerSet
	owned     bool
	documents map[int64]struct{}
	writes    int64
}

// Next is the next part of the half.
func (half *splitParts) Next(ctx context.Context) (any, bool, error) {
	for {
		part, ok, err := half.source.Next(ctx)
		if err != nil || !ok {
			return nil, false, err
		}
		switch part.(type) {
		case *store.ManifestPart:
			return part, true, nil
		case *store.EndPart:
			return &store.EndPart{Writes: half.writes}, true, nil
		}
		if half.owners.owns(part, half.documents) == half.owned {
			half.writes++
			return part, true, nil
		}
	}
}

// Close ends the read early.
func (half *splitParts) Close() error { return half.source.Close() }

// importSplit makes the stores in staging from the store archive at archive: the plugins' `store.sqlite` from the parts no kit owner has
// and the kit's `kit.sqlite` from the parts of the kit owners (an empty kit store is a valid one). Nothing is written before the archive
// passed countSplit; a failure leaves the staging directory for the caller to remove.
func importSplit(ctx context.Context, archive, staging string) error {
	census, err := countSplit(ctx, archive, kitOwned)
	if err != nil {
		return err
	}
	for _, half := range []struct {
		name  string
		owned bool
	}{{kitStore, true}, {archiveStore, false}} {
		source, err := openParts(archive)
		if err != nil {
			return err
		}
		path := filepath.Join(staging, half.name)
		if err := PrivateStore(path); err != nil {
			return errors.Join(err, source.Close())
		}
		if err := importParts(ctx, &splitParts{source: source, owners: kitOwned, owned: half.owned, documents: census.documents}, path); err != nil {
			return err
		}
	}
	return nil
}

// splitHostData splits the host's data archive at archive by owners into two JSONL archives, each the manifest, its parts and an `end`
// counting them: the parts of the owners (an owner the server's Go host serves) at owned, the rest (the NodeHost's) at rest. Nothing is
// written before the archive passed countSplit.
func splitHostData(ctx context.Context, archive, owned, rest string, owners ownerSet) error {
	census, err := countSplit(ctx, archive, owners)
	if err != nil {
		return err
	}
	for _, half := range []struct {
		path  string
		owned bool
	}{{owned, true}, {rest, false}} {
		source, err := openParts(archive)
		if err != nil {
			return err
		}
		parts := &splitParts{source: source, owners: owners, owned: half.owned, documents: census.documents}
		if err := writeParts(ctx, partsOf(parts), half.path); err != nil {
			return err
		}
	}
	return nil
}

// partsOf is the typed parts of source (what openParts and splitParts yield) as a part iterator, which closes source.
func partsOf(source store.PartSource) store.PartIterator { return typedParts{source} }

type typedParts struct{ source store.PartSource }

func (parts typedParts) Next(ctx context.Context) (store.ArchivePart, bool, error) {
	part, ok, err := parts.source.Next(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	return part.(store.ArchivePart), true, nil
}

func (parts typedParts) Close() error { return parts.source.Close() }
