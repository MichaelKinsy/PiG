package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/hostdata"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/store/host-data.ts
//
// The data belonging to one host address (decision "Documents and table rows can belong to a host"; spec
// `docs/host-keyed/spec.md` §4): the records whose `host` is the address's key and the rows holding it in a `host(...)`
// column, exported as an archive in the store's format, imported into a store that may hold data (refused, replaced
// or copied under another address of the kind, older tables' rows migrated, in one commit) and deleted in one commit.
// Imports are a trust boundary. Export and read are public; import and delete are the host's (`host_*.go` coordinates
// the live owners around them).
//
// Go forced differences:
//   - Pi's `HostImportOptions.existing` is the string "refuse" | "replace"; the empty string is the default (refuse).
//   - Pi's `Set<string>` of owners is a map[string]struct{}.
//   - Async contract: every Pi function here is `async` over one commit or one read sequence; here each blocks and
//     returns `(value, error)` in the same order. `ReadHostArchive` takes the archive's parts one at a time, as
//     ImportStore does (PartSource).
//   - A delta of an archive that does not apply is reported with the Go error's text (Pi's `String(error)`).

// HostImportOptions is what the host's `importHostData` takes besides the kind, the archive and the context.
type HostImportOptions struct {
	// Address is the address the data is imported under: a copy when it is not the archive's; another kind is refused.
	// Nil: the archive's own.
	Address *contracts.Address
	// Existing is what happens when the store has data of the address: "refuse" (the default, also ""), or "replace"
	// (replaced in the same commit).
	Existing string
}

// The values of HostImportOptions.Existing and hostImportPlan.Existing.
const (
	ExistingRefuse  = "refuse"
	ExistingReplace = "replace"
)

// HostDataCounts is what an import or a delete changed: the documents it created or retired, the rows it put or
// deleted.
type HostDataCounts = hostdata.Counts

// HostArchive is a host's data archive, read and checked (`ReadHostArchive`): one archive is one commit, so it is held
// in memory.
type HostArchive struct {
	// Host is the address key whose data it holds (its manifest's `host`).
	Host      string
	Tables    []*HostTable
	Documents []HostDocument
}

// HostTable is one table of a host's data archive: its `table` write, its host column, the `put`s of the host's rows,
// its indexes.
type HostTable struct {
	Write   *TableDefinitionWrite
	Column  string
	Puts    []*PutWrite
	Indexes []*IndexWrite
}

// HostDocument is one document of a host's data archive: its record and its value (its base with its deltas applied).
type HostDocument struct {
	Record DocumentRecord
	Value  delta.JsonValue
}

// ExportHostData is the records and rows of store belonging to address as archive parts (manifest with `host`, the
// tables with a `host(...)` column of its kind and only its rows, its records), holding the commit queue as
// `store.Export` does.
func ExportHostData(store Store, address contracts.Address, ctx context.Context) (*StoreExport, error) {
	// Stores are Chord's own: `OpenStore` and `ImportStore` made them.
	chord := store.(*ChordStore)
	if err := chord.AssertUsable(); err != nil {
		return nil, err
	}
	key, err := StoredText(address.Key, "exportHostData: address")
	if err != nil {
		return nil, err
	}
	return NewSelectionExport(chord, ctx, ExportSelection{Kind: SelectHost, Key: key}), nil
}

// ReadHostArchive reads and checks a host's data archive (a trust boundary): every part by the archive schema and in
// archive order; a manifest with `host` (well-formed text); tables with exactly one host column of that address's
// kind; every row's host cell and every record's `host` and key the manifest's `host`; live Chord records (history
// `none`) whose revisions start at a base, each delta at its base's version; `end` counting the parts between.
// `invalid/args` otherwise.
func ReadHostArchive(parts PartSource, ctx context.Context) (*HostArchive, error) {
	reader := &hostArchiveReader{ctx: ctx, place: hostManifest, addresses: map[string]bool{}}
	if err := importEach(parts, ctx, reader.take); err != nil {
		return nil, err
	}
	return reader.finish()
}

// hostPlace is where the next part of a host's data archive may go.
type hostPlace int

const (
	hostManifest hostPlace = iota
	hostTables
	hostDocuments
	hostEnded
)

// hostOpenDocument is the record whose revisions may follow, with its last seq, its base's version and its value so
// far.
type hostOpenDocument struct {
	record DocumentRecord
	seq    int64
	base   *int64
	value  delta.JsonValue
}

// hostArchiveReader is one host's data archive being read: its place, what it read so far, the open table and
// document.
type hostArchiveReader struct {
	ctx       context.Context
	position  int
	place     hostPlace
	host      string
	writes    int64
	tables    []*HostTable
	documents []HostDocument
	addresses map[string]bool
	document  *hostOpenDocument
}

func (reader *hostArchiveReader) take(value any) error {
	position := reader.position
	reader.position++
	part, err := CheckPart(value, position, reader.ctx)
	if err != nil {
		return err
	}
	what := fmt.Sprintf("importHostData: part %d (%s)", position, part.StoreWriteKind())
	if reader.place == hostEnded {
		return invalidArgs("%s: after the end", what)
	}
	manifest, isManifest := part.(*ManifestPart)
	if reader.place == hostManifest || isManifest {
		return reader.start(manifest, what)
	}
	if end, isEnd := part.(*EndPart); isEnd {
		if end.Writes != reader.writes {
			return invalidArgs("%s: counts %d writes, the archive has %d", what, end.Writes, reader.writes)
		}
		if err := reader.endDocument(what); err != nil {
			return err
		}
		reader.place = hostEnded
		return nil
	}
	reader.writes++
	switch typed := part.(type) {
	case *TableDefinitionWrite:
		return reader.table(typed, what)
	case *PutWrite:
		return reader.put(typed, what)
	case *IndexWrite:
		return reader.index(typed, what)
	case *DocumentCreate:
		return reader.create(typed.Record, what)
	case *DocumentRevisionWrite:
		return reader.revision(typed, what)
	}
	// CheckPart admits no other kind.
	return invalidArgs("%s: not an archive part", what)
}

func (reader *hostArchiveReader) finish() (*HostArchive, error) {
	if reader.place != hostEnded {
		return nil, invalidArgs("importHostData: the archive has no end part")
	}
	return &HostArchive{Host: reader.host, Tables: reader.tables, Documents: reader.documents}, nil
}

// start is the manifest, first and once: a format and schema this code reads, the address key it holds data of.
func (reader *hostArchiveReader) start(manifest *ManifestPart, what string) error {
	if manifest == nil || reader.place != hostManifest {
		return invalidArgs("%s: the manifest comes first, once", what)
	}
	if manifest.Format != ArchiveFormat {
		return invalidArgs("%s: format %d is not %d", what, manifest.Format, ArchiveFormat)
	}
	if manifest.Schema > StoreSchema {
		return invalidArgs("%s: schema %d is newer than %d", what, manifest.Schema, StoreSchema)
	}
	if manifest.Host == nil {
		return invalidArgs("%s: not a host's data archive (no host)", what)
	}
	stored, err := StoredText(*manifest.Host, what)
	if err != nil {
		return err
	}
	if stored != *manifest.Host {
		return invalidArgs("%s: not a host's data archive (no host)", what)
	}
	reader.host = *manifest.Host
	reader.place = hostTables
	return nil
}

// table is a table with exactly one host column, of the archive's address's kind; once; before the documents.
func (reader *hostArchiveReader) table(write *TableDefinitionWrite, what string) error {
	if reader.place != hostTables {
		return invalidArgs("%s: tables come before the documents", what)
	}
	for _, each := range reader.tables {
		if each.Write.Table == write.Table {
			return invalidArgs("%s: table %s occurs twice", what, write.Table)
		}
	}
	hosts := 0
	for _, entry := range write.Columns {
		if entry.Record.Host != nil {
			hosts++
		}
	}
	kind := contracts.KindOfKey(reader.host)
	column, found := HostColumnName(write.Columns, kind)
	if hosts != 1 || !found {
		return invalidArgs("%s: table %s has no single host column of %s", what, write.Table, kind)
	}
	reader.tables = append(reader.tables, &HostTable{Write: write, Column: column})
	return nil
}

// put is a row of the open table, before its indexes, belonging to the archive's address.
func (reader *hostArchiveReader) put(write *PutWrite, what string) error {
	var table *HostTable
	if len(reader.tables) > 0 {
		table = reader.tables[len(reader.tables)-1]
	}
	if reader.place != hostTables || table == nil || table.Write.Table != write.Table || len(table.Indexes) > 0 {
		return invalidArgs("%s: a put follows its table's table write, before its indexes", what)
	}
	if !hostCellIs(write.Row, table.Column, reader.host) {
		return invalidArgs("%s: a row that does not belong to %s", what, reader.host)
	}
	table.Puts = append(table.Puts, write)
	return nil
}

// index is an index of the open table, after its puts.
func (reader *hostArchiveReader) index(write *IndexWrite, what string) error {
	var table *HostTable
	if len(reader.tables) > 0 {
		table = reader.tables[len(reader.tables)-1]
	}
	if reader.place != hostTables || table == nil || table.Write.Table != write.Table {
		return invalidArgs("%s: an index follows its table's table write and puts", what)
	}
	table.Indexes = append(table.Indexes, write)
	return nil
}

// create is a live Chord record (history `none`) of the archive's address (its `host` and key), at an address no other
// record of the archive has, its address well-formed text. The record before it must have ended well.
func (reader *hostArchiveReader) create(record DocumentRecord, what string) error {
	if err := reader.endDocument(what); err != nil {
		return err
	}
	reader.place = hostDocuments
	if record.Host == nil || *record.Host != reader.host || record.Key == nil || *record.Key != reader.host {
		return invalidArgs("%s: a record that does not belong to %s", what, reader.host)
	}
	if record.History != HistoryNone || record.RetiredAt != nil || record.Meta != nil {
		return invalidArgs("%s: not a live Chord record", what)
	}
	for _, text := range []string{record.Owner, record.Namespace, record.Kind} {
		stored, err := StoredText(text, what)
		if err != nil {
			return err
		}
		if stored != text {
			return invalidArgs("%s: an address is not well-formed text", what)
		}
	}
	encoded, err := jsstring.MarshalJSON([]string{record.Owner, record.Namespace, record.Kind})
	if err != nil {
		return err
	}
	address := string(encoded)
	if reader.addresses[address] {
		return invalidArgs("%s: a second record at one address", what)
	}
	reader.addresses[address] = true
	reader.document = &hostOpenDocument{record: record}
	return nil
}

// revision is a revision of the record before it, in seq order, from a base, each delta at its base's version, applied.
func (reader *hostArchiveReader) revision(part *DocumentRevisionWrite, what string) error {
	document := reader.document
	if document == nil || document.record.ID != part.ID || part.Seq <= document.seq {
		return invalidArgs("%s: a revision follows its record's create, in seq order", what)
	}
	content := part.Content
	if content.Kind == ContentBase {
		version := content.Version
		document.base = &version
		document.value = content.Value
	} else if document.base == nil || *document.base != content.Version {
		return invalidArgs("%s: a delta without a base at its version", what)
	} else {
		applied, err := delta.Apply(document.value, content.Ops)
		if err != nil {
			return invalidArgs("%s: a delta that does not apply (%s)", what, err.Error())
		}
		document.value = applied
	}
	document.seq = part.Seq
	return nil
}

// endDocument: the record before ends well: its last base at the record's version; its value kept.
func (reader *hostArchiveReader) endDocument(what string) error {
	document := reader.document
	reader.document = nil
	if document == nil {
		return nil
	}
	if document.base == nil || *document.base != document.record.Version {
		return invalidArgs("%s: record %d has no base at its version", what, document.record.ID)
	}
	reader.documents = append(reader.documents, HostDocument{Record: document.record, Value: document.value})
	return nil
}

// hostCellIs reports whether row holds the string value in column.
func hostCellIs(row *delta.JsonObject, column, value string) bool {
	cell, isText := row.Value(column).(string)
	return isText && cell == value
}

// Pi's barrel exports none of the import and delete operations: the host reaches them through the internal package
// hostdata (see there), which these adapters register; the store's own types come back from its `any`s, and one of
// another type is a programming error (panics).
func init() {
	archiveOf := func(archive any) *HostArchive {
		typed, _ := archive.(*HostArchive)
		return typed
	}
	commitOf := func(commit hostdata.Commit) hostCommit {
		after := make([]StagedWrite, len(commit.After))
		for index, write := range commit.After {
			after[index] = write.(StagedWrite)
		}
		return hostCommit{Fence: CommitFence(commit.Fence), After: after}
	}
	hostdata.Register(hostdata.Operations{
		TablesToMigrate: func(store, archive any) []string { return tablesToMigrate(store.(Store), archiveOf(archive)) },
		Owners: func(store any, key string, archive any, ctx context.Context) (map[string]struct{}, error) {
			return hostDataOwners(store.(Store), key, archiveOf(archive), ctx)
		},
		Import: func(store, archive any, plan hostdata.Plan, commit hostdata.Commit, ctx context.Context) (HostDataCounts, error) {
			histories := make(map[string]*TableDefinition, len(plan.Histories))
			for table, definition := range plan.Histories {
				histories[table] = definition.(*TableDefinition)
			}
			return importHostData(store.(Store), archiveOf(archive), hostImportPlan{Target: plan.Target, Existing: plan.Existing, Histories: histories}, commitOf(commit), ctx)
		},
		Delete: func(store any, key string, commit hostdata.Commit, ctx context.Context) (HostDataCounts, error) {
			return deleteHostData(store.(Store), key, commitOf(commit), ctx)
		},
	})
}

// hostCommit is how a host data operation commits: the host's fence and its reads in the step.
type hostCommit struct {
	// Fence admits the commit (the caller's context, the host open) and adopts what After read, after `COMMIT`.
	Fence CommitFence
	// After is staged after the operation's writes, in the same step: what the live owners hold, read again.
	After []StagedWrite
}

// hostImportPlan is what `importHostData` does with an archive: under Target (canonical), Existing, with the tables'
// histories.
type hostImportPlan struct {
	Target string
	// Existing is ExistingRefuse or ExistingReplace.
	Existing string
	// Histories are the newest definitions (from the owners' code) of the archive's tables whose rows must migrate, by
	// SQL name.
	Histories map[string]*TableDefinition
}

// tablesToMigrate is the archive's tables whose rows must migrate before they can be merged into store (recorded at a
// newer version, or exported `migrating`): their owners must be live for their histories. By SQL name.
func tablesToMigrate(store Store, archive *HostArchive) []string {
	// Stores are Chord's own: `OpenStore` and `ImportStore` made them.
	chord := store.(*ChordStore)
	var tables []string
	for _, table := range archive.Tables {
		write := table.Write
		recorded := chord.Table(write.Table)
		if recorded != nil && (recorded.Version > write.Version || write.State == StateMigrating) {
			tables = append(tables, write.Table)
		}
	}
	return tables
}

// hostDataOwners is the owners (object types) whose data an operation on host key changes: of tables with a
// `host(...)` column of its kind (recorded, and the archive's), of records belonging to it, and of the archive's
// records. archive is nil for a delete.
func hostDataOwners(store Store, key string, archive *HostArchive, ctx context.Context) (map[string]struct{}, error) {
	// Stores are Chord's own: `OpenStore` and `ImportStore` made them.
	chord := store.(*ChordStore)
	kind := contracts.KindOfKey(key)
	owners := map[string]struct{}{}
	ownerOf := func(table string) string { return table[:strings.IndexByte(table, ':')] }
	for _, record := range chord.Tables() {
		if _, held := HostColumnName(record.Columns, kind); held {
			owners[ownerOf(record.Table)] = struct{}{}
		}
	}
	if archive != nil {
		for _, table := range archive.Tables {
			owners[ownerOf(table.Write.Table)] = struct{}{}
		}
		for _, document := range archive.Documents {
			owners[document.Record.Owner] = struct{}{}
		}
	}
	rows, err := Read(chord, func(executor sqlite.SqliteExecutor) ([]sqlite.SqliteRow, error) {
		return executor.All(`SELECT DISTINCT owner FROM `+TableDocuments+` WHERE host = ?`, key)
	}, ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		owner, _ := row["owner"].(string)
		owners[owner] = struct{}{}
	}
	return owners, nil
}

// importHostData imports archive into store in one commit (spec §4) under plan.Target: the store's data of it refused
// or, with ExistingReplace, retired and deleted first; tables the store does not record created from the archive,
// recorded ones checked against the archive's metadata (the recorded host column decides) or migrated through their
// histories; rows that would replace another row refused; records created with fresh ids at the commit's seq, their
// values as bases; commit.After staged after it. Internal: the host coordinates it.
func importHostData(store Store, archive *HostArchive, plan hostImportPlan, commit hostCommit, ctx context.Context) (HostDataCounts, error) {
	if contracts.KindOfKey(plan.Target) != contracts.KindOfKey(archive.Host) {
		return HostDataCounts{}, invalidArgs("importHostData: data of %s cannot belong to %s, another kind", archive.Host, plan.Target)
	}
	var counts HostDataCounts
	apply := func(step *Step, writes *[]StoreWrite) error {
		var err error
		counts, err = applyImport(step, archive, plan, writes, ctx)
		return err
	}
	if err := commitOperation(store, apply, commit, ctx); err != nil {
		return HostDataCounts{}, err
	}
	return counts, nil
}

// deleteHostData deletes the data of host key (canonical) from store in one commit: every record whose `host` is the
// key retired, every row holding it in a `host(...)` column of its kind deleted (`deleteWhere`, counted). Internal, as
// importHostData.
func deleteHostData(store Store, key string, commit hostCommit, ctx context.Context) (HostDataCounts, error) {
	var counts HostDataCounts
	apply := func(step *Step, writes *[]StoreWrite) error {
		var err error
		counts, err = removeHostData(step, key, writes)
		return err
	}
	if err := commitOperation(store, apply, commit, ctx); err != nil {
		return HostDataCounts{}, err
	}
	return counts, nil
}

// commitOperation commits apply's writes, then commit.After, as one seq under commit.Fence.
func commitOperation(store Store, apply func(step *Step, writes *[]StoreWrite) error, commit hostCommit, ctx context.Context) error {
	write := stagedFunc{order: OrderWrite, apply: func(step *Step) ([]StoreWrite, error) {
		var writes []StoreWrite
		if err := apply(step, &writes); err != nil {
			return nil, err
		}
		return writes, nil
	}}
	// Stores are Chord's own: `OpenStore` and `ImportStore` made them.
	_, err := store.(*ChordStore).Commit(append([]StagedWrite{write}, commit.After...), commit.Fence, ctx)
	return err
}

// applyImport is the import inside its commit step: the existing data first, then the tables, the rows, the records.
func applyImport(step *Step, archive *HostArchive, plan hostImportPlan, writes *[]StoreWrite, ctx context.Context) (HostDataCounts, error) {
	target := plan.Target
	if plan.Existing == ExistingReplace {
		if _, err := removeHostData(step, target, writes); err != nil {
			return HostDataCounts{}, err
		}
	} else {
		has, err := hasHostData(step, target)
		if err != nil {
			return HostDataCounts{}, err
		}
		if has {
			return HostDataCounts{}, invalidArgs(`importHostData: %s has data in this store (import with existing: "replace")`, target)
		}
	}
	rows := 0
	for _, table := range archive.Tables {
		placed, err := importTable(step, table, archive.Host, plan, writes, ctx)
		if err != nil {
			return HostDataCounts{}, err
		}
		rows += placed
	}
	for _, document := range archive.Documents {
		record := document.Record
		address := DocumentAddress{Owner: record.Owner, Namespace: record.Namespace, Kind: record.Kind, Key: &target, Host: &target}
		text, err := jsstring.MarshalJSON(document.Value)
		if err != nil {
			return HostDataCounts{}, err
		}
		base := DocumentBase{Version: record.Version, Fingerprint: record.Fingerprint, Value: document.Value}
		created, err := CreateDocument(address, base, string(text), nil).Apply(step)
		if err != nil {
			return HostDataCounts{}, err
		}
		*writes = append(*writes, created...)
	}
	return HostDataCounts{Documents: len(archive.Documents), Rows: rows}, nil
}

// importTable is one table of the archive: created with its indexes when the store does not record it; recorded newer
// here, or exported `migrating`, its rows migrate through the table's history first. Every row must then hold target
// in the recorded table's own host column (whatever the archive's metadata says) and take no other row's key.
func importTable(step *Step, table *HostTable, source string, plan hostImportPlan, writes *[]StoreWrite, ctx context.Context) (int, error) {
	write := table.Write
	what := "importHostData: table " + write.Table
	// The copy's rewrite, on the archive's own host column (a migration's renames carry it along).
	puts := make([]*PutWrite, len(table.Puts))
	for index, put := range table.Puts {
		if hostCellIs(put.Row, table.Column, plan.Target) {
			puts[index] = put
			continue
		}
		row := put.Row.Clone()
		row.Set(table.Column, plan.Target)
		puts[index] = &PutWrite{Table: put.Table, Row: row, Version: put.Version, Columns: put.Columns}
	}
	recorded := step.Table(write.Table)
	placed := puts
	if recorded == nil {
		writesOf := []StoreWrite{write}
		for _, index := range table.Indexes {
			writesOf = append(writesOf, index)
		}
		for _, each := range writesOf {
			if err := applyWrite(step, each, what); err != nil {
				return 0, err
			}
			*writes = append(*writes, each)
		}
	} else {
		if recorded.State != StateReady {
			return 0, invalidArgs("%s: migrating here; open its owner first", what)
		}
		if write.Version > recorded.Version {
			return 0, invalidArgs("%s: at version %d in the archive, newer than %d here", what, write.Version, recorded.Version)
		}
		if write.Version < recorded.Version || write.State == StateMigrating {
			history := plan.Histories[write.Table]
			if history == nil {
				return 0, invalidArgs("%s: at version %d, %d here; its owner is not live", what, write.Version, recorded.Version)
			}
			var err error
			if placed, err = migrateRows(write, puts, history, recorded, ctx); err != nil {
				return 0, err
			}
		}
	}
	record := step.Table(write.Table)
	column, found := HostColumnName(record.Columns, contracts.KindOfKey(source))
	if !found {
		return 0, invalidArgs("%s: no host column of %s here", what, contracts.KindOfKey(source))
	}
	var conditions strings.Builder
	for index, name := range record.Key {
		if index > 0 {
			conditions.WriteString(" AND ")
		}
		conditions.WriteString(Quote(name) + " = ?")
	}
	exists := `SELECT 1 AS found FROM ` + Quote(write.Table) + ` WHERE ` + conditions.String()
	for _, put := range placed {
		if !hostCellIs(put.Row, column, plan.Target) {
			return 0, invalidArgs("%s: a row that does not belong to %s", what, plan.Target)
		}
		key := make([]sqlite.SqliteValue, len(record.Key))
		for index, name := range record.Key {
			columnRecord, _ := record.Columns.Get(name)
			scalar, err := CheckScalar(columnRecord.Type, put.Row.Value(name), what+": key")
			if err != nil {
				return 0, err
			}
			key[index] = ToSqlite(scalar)
		}
		found, err := step.Get(exists, key...)
		if err != nil {
			return 0, err
		}
		if found != nil {
			text, err := jsstring.MarshalJSON(key)
			if err != nil {
				return 0, err
			}
			return 0, invalidArgs("%s: a row with key %s exists (of another address)", what, text)
		}
		if err := applyWrite(step, put, what); err != nil {
			return 0, err
		}
		step.Rows++
		*writes = append(*writes, put)
	}
	return len(placed), nil
}

// removeHostData retires the records of host key and deletes its rows in step, recording the writes; returns the
// counts.
func removeHostData(step *Step, key string, writes *[]StoreWrite) (HostDataCounts, error) {
	records, err := ListHostRecords(step, key, 0, MaxSafeInteger)
	if err != nil {
		return HostDataCounts{}, err
	}
	for _, record := range records {
		retired, err := RetireDocument(record.ID, record.Revision).Apply(step)
		if err != nil {
			return HostDataCounts{}, err
		}
		*writes = append(*writes, retired...)
	}
	rows := 0
	for _, hosted := range hostColumnTables(step, contracts.KindOfKey(key)) {
		count, err := step.RunCounted(`DELETE FROM `+Quote(hosted.table)+` WHERE `+Quote(hosted.column)+` = ?`, key)
		if err != nil {
			return HostDataCounts{}, err
		}
		step.Rows += int(count)
		rows += int(count)
		*writes = append(*writes, &DeleteWhereWrite{Table: hosted.table, Where: WhereNode{Op: WhereEq, Column: hosted.column, Value: key}, Count: count})
	}
	return HostDataCounts{Documents: len(records), Rows: rows}, nil
}

// hasHostData reports whether step's store holds a record or a row of host key.
func hasHostData(step *Step, key string) (bool, error) {
	records, err := ListHostRecords(step, key, 0, 1)
	if err != nil || len(records) > 0 {
		return len(records) > 0, err
	}
	for _, hosted := range hostColumnTables(step, contracts.KindOfKey(key)) {
		found, err := step.Get(`SELECT 1 AS found FROM `+Quote(hosted.table)+` WHERE `+Quote(hosted.column)+` = ? LIMIT 1`, key)
		if err != nil {
			return false, err
		}
		if found != nil {
			return true, nil
		}
	}
	return false, nil
}

// hostColumnTable is a recorded table with a host column.
type hostColumnTable struct{ table, column string }

// hostColumnTables are the recorded tables with a `host(...)` column of kind, with that column's name.
func hostColumnTables(step *Step, kind string) []hostColumnTable {
	var tables []hostColumnTable
	for _, record := range step.Records() {
		if column, held := HostColumnName(record.Columns, kind); held {
			tables = append(tables, hostColumnTable{table: record.Table, column: column})
		}
	}
	return tables
}
