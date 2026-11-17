package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Merging two stores' archives into one (D462).
//
// A PiG worker's records are in two stores (the NodeHost's `store.sqlite` and the Go kit's `kit.sqlite`) and so are the server's (the
// NodeHost's and the Go host's), where a Pi host has one, whose export is the one archive an address archive holds. An export therefore
// merges the two stores' exports into the one archive Pi writes: the manifest (the larger schema and `lastSeq`, the allocator's next id
// grown by the kit's documents), the tables of both by name (`CompareText`, the order a store exports them in), the plugins' documents
// by id and then the kit's, numbered on from the plugins' next id in the order they came (their ids are internal: each store keeps its
// own allocator, an import keeps the archive's ids and `nextId`), and one `end` counting them all. An import splits it again by owner
// (archive_split.go).

// maxSafeInteger is the largest id an archive holds: JavaScript's `Number.MAX_SAFE_INTEGER`, which Pi's import reads them with.
const maxSafeInteger = 1<<53 - 1

// lookahead is a part iterator whose next part can be looked at before it is taken.
type lookahead struct {
	parts  store.PartIterator
	part   store.ArchivePart
	ok     bool
	primed bool
}

// peek is the next part (ok false at the end), not taken.
func (l *lookahead) peek(ctx context.Context) (store.ArchivePart, bool, error) {
	if !l.primed {
		part, ok, err := l.parts.Next(ctx)
		if err != nil {
			return nil, false, err
		}
		l.part, l.ok, l.primed = part, ok, true
	}
	return l.part, l.ok, nil
}

// take takes the part peek showed.
func (l *lookahead) take() { l.part, l.ok, l.primed = nil, false, false }

// Next is the next part, taken.
func (l *lookahead) Next(ctx context.Context) (store.ArchivePart, bool, error) {
	part, ok, err := l.peek(ctx)
	if err != nil || !ok {
		return nil, false, err
	}
	l.take()
	return part, true, nil
}

// Close closes the iterator behind it.
func (l *lookahead) Close() error { return l.parts.Close() }

// stagedKit is the kit store's export written to a file, with what the merge needs to know of it before it reads it.
type stagedKit struct {
	path      string
	documents int64
}

// stageParts writes parts (closed after) as JSONL to path and counts its documents. With owned, an owner it does not name is refused: the
// import would route it to the plugins' store.
func stageParts(ctx context.Context, parts store.PartIterator, path string, owned ownerSet) (stagedKit, error) {
	counting := &countingParts{parts: parts, owned: owned}
	if err := writeParts(ctx, counting, path); err != nil {
		return stagedKit{}, err
	}
	return stagedKit{path: path, documents: counting.documents}, nil
}

// countingParts counts the documents of the parts that pass and refuses an owner outside owned.
type countingParts struct {
	parts     store.PartIterator
	owned     ownerSet
	documents int64
}

func (c *countingParts) Next(ctx context.Context) (store.ArchivePart, bool, error) {
	part, ok, err := c.parts.Next(ctx)
	if err != nil || !ok {
		return part, ok, err
	}
	owner := ""
	switch part := part.(type) {
	case *store.TableDefinitionWrite:
		owner = tableOwner(part.Table)
	case *store.DocumentCreate:
		owner = part.Record.Owner
		c.documents++
	default:
		return part, true, nil
	}
	if c.owned != nil && !c.owned(owner) {
		return nil, false, fmt.Errorf("exportData: the kit's store holds records of %q, which the import would route to the plugins' store", owner)
	}
	return part, true, nil
}

func (c *countingParts) Close() error { return c.parts.Close() }

// mergedParts is the merge of two archives' parts; see the comment of the file.
type mergedParts struct {
	first, second *lookahead
	// secondDocuments is how many documents the second archive has: the manifest's next id counts them before they come.
	secondDocuments int64
	stage           mergeStage
	// group is the archive whose table the merge is writing the rows and indexes of.
	group *lookahead
	// writes counts the parts written between the manifest and `end`.
	writes int64
	// nextID is the id the second archive's next document gets; the pair of the document in hand follows.
	nextID, oldID, newID int64
}

type mergeStage int

const (
	mergeManifest mergeStage = iota
	mergeTables
	mergeFirstDocuments
	mergeSecondDocuments
	mergeEnd
	mergeDone
)

// mergeParts is the merge of the archive first with the archive second, which has secondDocuments documents (stageParts counted them).
func mergeParts(first store.PartIterator, second store.PartIterator, secondDocuments int64) store.PartIterator {
	return &mergedParts{first: &lookahead{parts: first}, second: &lookahead{parts: second}, secondDocuments: secondDocuments}
}

func (m *mergedParts) Close() error { return errors.Join(m.first.Close(), m.second.Close()) }

func (m *mergedParts) Next(ctx context.Context) (store.ArchivePart, bool, error) {
	switch m.stage {
	case mergeManifest:
		return m.manifest(ctx)
	case mergeTables:
		return m.table(ctx)
	case mergeFirstDocuments:
		part, ok, err := m.first.peek(ctx)
		if err != nil {
			return nil, false, err
		}
		switch part.(type) {
		case *store.DocumentCreate, *store.DocumentRevisionWrite:
			m.first.take()
			m.writes++
			return part, true, nil
		case *store.EndPart:
			m.first.take()
			m.stage = mergeSecondDocuments
			return m.Next(ctx)
		}
		return nil, false, m.unexpected("the first archive", part, ok)
	case mergeSecondDocuments:
		return m.secondDocument(ctx)
	case mergeEnd:
		m.stage = mergeDone
		return &store.EndPart{Writes: m.writes}, true, nil
	}
	return nil, false, nil
}

func (m *mergedParts) unexpected(where string, part store.ArchivePart, ok bool) error {
	if !ok {
		return fmt.Errorf("exportData: %s ends without its end part", where)
	}
	return fmt.Errorf("exportData: %s has a %s where the merge expected a table, a document or its end", where, part.StoreWriteKind())
}

// manifest merges the two manifests: both are format 1 and, in a host's data, of one host.
func (m *mergedParts) manifest(ctx context.Context) (store.ArchivePart, bool, error) {
	var manifests [2]*store.ManifestPart
	for i, archive := range []*lookahead{m.first, m.second} {
		part, _, err := archive.peek(ctx)
		if err != nil {
			return nil, false, err
		}
		manifest, ok := part.(*store.ManifestPart)
		if !ok {
			return nil, false, errors.New("exportData: an archive to merge does not start with its manifest")
		}
		archive.take()
		manifests[i] = manifest
	}
	first, second := manifests[0], manifests[1]
	if first.Format != second.Format {
		return nil, false, fmt.Errorf("exportData: the stores' archives are of formats %d and %d", first.Format, second.Format)
	}
	merged := *first
	merged.Schema = max(first.Schema, second.Schema)
	merged.LastSeq = max(first.LastSeq, second.LastSeq)
	merged.NextID = first.NextID + m.secondDocuments
	if merged.NextID > maxSafeInteger {
		return nil, false, fmt.Errorf("exportData: the stores' ids would pass %d", int64(maxSafeInteger))
	}
	if first.Host == nil {
		merged.Host = second.Host
	} else if second.Host != nil && *first.Host != *second.Host {
		return nil, false, fmt.Errorf("exportData: the server's parts are of %q and of %q", *first.Host, *second.Host)
	}
	m.nextID = first.NextID
	m.stage = mergeTables
	return &merged, true, nil
}

// table is the next table write, put or index: a table's group (its definition, rows and indexes) comes from one archive, the tables
// of the two by name.
func (m *mergedParts) table(ctx context.Context) (store.ArchivePart, bool, error) {
	if m.group != nil {
		part, ok, err := m.group.peek(ctx)
		if err != nil {
			return nil, false, err
		}
		switch part.(type) {
		case *store.PutWrite, *store.IndexWrite:
			m.group.take()
			m.writes++
			return part, true, nil
		}
		if !ok {
			return nil, false, m.unexpected("an archive", part, ok)
		}
		m.group = nil
	}
	var heads [2]*store.TableDefinitionWrite
	for i, archive := range []*lookahead{m.first, m.second} {
		part, ok, err := archive.peek(ctx)
		if err != nil {
			return nil, false, err
		}
		switch part := part.(type) {
		case *store.TableDefinitionWrite:
			heads[i] = part
		case *store.DocumentCreate, *store.DocumentRevisionWrite, *store.EndPart:
		default:
			return nil, false, m.unexpected("an archive", part, ok)
		}
	}
	var chosen *lookahead
	switch {
	case heads[0] == nil && heads[1] == nil:
		m.stage = mergeFirstDocuments
		return m.Next(ctx)
	case heads[1] == nil:
		chosen = m.first
	case heads[0] == nil:
		chosen = m.second
	default:
		switch order := store.CompareText(heads[0].Table, heads[1].Table); {
		case order == 0:
			return nil, false, fmt.Errorf("exportData: the table %s is in both stores", heads[0].Table)
		case order < 0:
			chosen = m.first
		default:
			chosen = m.second
		}
	}
	part, _, _ := chosen.peek(ctx)
	chosen.take()
	m.group = chosen
	m.writes++
	return part, true, nil
}

// secondDocument is the next part of the second archive's documents, its ids numbered on from the first's next id. A document's
// revisions follow it, as an export writes them.
func (m *mergedParts) secondDocument(ctx context.Context) (store.ArchivePart, bool, error) {
	part, ok, err := m.second.peek(ctx)
	if err != nil {
		return nil, false, err
	}
	switch part := part.(type) {
	case *store.DocumentCreate:
		m.second.take()
		m.oldID, m.newID = part.Record.ID, m.nextID
		m.nextID++
		renumbered := *part
		renumbered.Record.ID = m.newID
		m.writes++
		return &renumbered, true, nil
	case *store.DocumentRevisionWrite:
		if part.ID != m.oldID {
			return nil, false, fmt.Errorf("exportData: a revision of document %d does not follow its record", part.ID)
		}
		m.second.take()
		renumbered := *part
		renumbered.ID = m.newID
		m.writes++
		return &renumbered, true, nil
	case *store.EndPart:
		m.second.take()
		m.stage = mergeEnd
		return m.Next(ctx)
	}
	return nil, false, m.unexpected("the second archive", part, ok)
}
