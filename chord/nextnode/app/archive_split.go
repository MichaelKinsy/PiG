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

// ownedByKit reports whether part belongs to a kit owner: a table's write by its name (`<owner>:<table>`), a document's record by its
// owner, a revision by the id of a kit document (kitDocuments). The manifest and `end` belong to neither.
func ownedByKit(part any, kitDocuments map[int64]struct{}) bool {
	switch part := part.(type) {
	case *store.TableDefinitionWrite:
		return kitTable(part.Table)
	case *store.PutWrite:
		return kitTable(part.Table)
	case *store.IndexWrite:
		return kitTable(part.Table)
	case *store.DocumentCreate:
		return slices.Contains(kitOwners, part.Record.Owner)
	case *store.DocumentRevisionWrite:
		_, ok := kitDocuments[part.ID]
		return ok
	}
	return false
}

// kitTable is whether the table named table (`<owner>:<table>`) is a kit owner's.
func kitTable(table string) bool {
	for _, owner := range kitOwners {
		if strings.HasPrefix(table, owner+":") {
			return true
		}
	}
	return false
}

// splitCensus is what one read of a store archive says that no half can: the ids of the kit's documents, and the writes of each half.
type splitCensus struct {
	kitDocuments map[int64]struct{}
	kitWrites    int64
}

// countSplit reads the store archive at archive once and checks what splitting could drop: no document id twice (each half is checked
// alone), and the writes of the two halves adding up to the `end` the archive declared.
func countSplit(ctx context.Context, archive string) (splitCensus, error) {
	census := splitCensus{kitDocuments: map[int64]struct{}{}}
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
			if ownedByKit(part, nil) {
				census.kitDocuments[part.Record.ID] = struct{}{}
			}
		}
		if ownedByKit(part, census.kitDocuments) {
			census.kitWrites++
		} else {
			restWrites++
		}
	}
	if declared >= 0 && declared != census.kitWrites+restWrites {
		return census, data.Invalid(data.ReasonArgs, fmt.Sprintf("import: end counts %d writes, the archive has %d", declared, census.kitWrites+restWrites))
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

// splitParts is one half of a store archive: the manifest, the parts of the kit (kit) or of everyone else, and an `end` counting them.
type splitParts struct {
	source       store.PartSource
	kit          bool
	kitDocuments map[int64]struct{}
	writes       int64
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
		if ownedByKit(part, half.kitDocuments) == half.kit {
			half.writes++
			return part, true, nil
		}
	}
}

// Close ends the read early.
func (half *splitParts) Close() error { return half.source.Close() }

// importSplit makes the stores in staging from the store archive at archive: the plugins' `store.sqlite` from the parts no kit owner has
// and, unless keepKit is false, the kit's `kit.sqlite` from the parts of the kit owners (an empty kit store is a valid one). With keepKit
// false the archive must hold none (`kit.archive` carries the kit's store, see UnpackStaging). Nothing is written before the archive passed
// countSplit; a failure leaves the staging directory for the caller to remove.
func importSplit(ctx context.Context, archive, staging string, keepKit bool) error {
	census, err := countSplit(ctx, archive)
	if err != nil {
		return err
	}
	if !keepKit && census.kitWrites != 0 {
		return errors.New("importData: the archive holds the kit's data in store.archive and in kit.archive")
	}
	halves := []struct {
		name string
		kit  bool
		skip bool
	}{{kitStore, true, !keepKit}, {archiveStore, false, false}}
	for _, half := range halves {
		if half.skip {
			continue
		}
		source, err := openParts(archive)
		if err != nil {
			return err
		}
		path := filepath.Join(staging, half.name)
		if err := PrivateStore(path); err != nil {
			return errors.Join(err, source.Close())
		}
		if err := importParts(ctx, &splitParts{source: source, kit: half.kit, kitDocuments: census.kitDocuments}, path); err != nil {
			return err
		}
	}
	return nil
}
