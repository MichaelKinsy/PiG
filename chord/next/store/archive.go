package store

import (
	"context"
	"fmt"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/store/archive.ts
//
// Archives and recorded commits as they cross a trust boundary (slice 6a §13, slice 6b §1.6; decisions "Export
// archives are operations", "Writes carry their versions; archives are lossless"): the parts an export yields and an
// import takes, their order, and the compiled schemas every imported part and every applied write is checked against
// first.
//
// Go forced differences:
//   - Pi's ArchivePart is a structural union; here it is the interface ArchivePart, whose values are the pointer types
//     of the part kinds: *ManifestPart and *EndPart (this file), and the store writes *TableDefinitionWrite,
//     *PutWrite, *IndexWrite, *DocumentCreate and *DocumentRevisionWrite.
//   - Pi's `unknown` input is a decoded JSON value (a *delta.JsonObject tree, as delta.DecodeJson reads it). Pi casts
//     a checked value to its type; Go cannot, so CheckPart and CheckWrite decode the checked JSON into the typed
//     value. A typed part is checked through its JSON text (JSON.stringify of it) and returned as it is.
//   - Pi's compiled `validatorFor({ schema })` owner is a package-level *contracts.ParamsData carrying the schema.

// ArchiveFormat is the archive format `Export` writes; `ImportStore` takes this one only.
const ArchiveFormat int64 = 1

// ArchivePart is one part of an archive, in this order: the manifest; per table (by SQL name) its `table` write, its
// `put`s and its `index` writes; per document record (by id, live and retired) its `document.create` without a base,
// then its kept revisions in seq order; `end` with the count of the parts between. Its values are *ManifestPart,
// *TableDefinitionWrite, *PutWrite, *IndexWrite, *DocumentCreate, *DocumentRevisionWrite and *EndPart.
type ArchivePart interface {
	// StoreWriteKind is the part's `kind` tag.
	StoreWriteKind() string
}

// ManifestPart is the first part: the format, the store's schema version, its `lastSeq` and the allocator's next id
// (rule 24, D3).
type ManifestPart struct {
	Format  int64
	Schema  int64
	LastSeq int64
	NextID  int64
}

// StoreWriteKind is "manifest".
func (*ManifestPart) StoreWriteKind() string { return "manifest" }

type manifestJSON struct {
	Kind    string `json:"kind"`
	Format  int64  `json:"format"`
	Schema  int64  `json:"schema"`
	LastSeq int64  `json:"lastSeq"`
	NextID  int64  `json:"nextId"`
}

// MarshalJSON is the manifest in Pi's key order.
func (part *ManifestPart) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(manifestJSON{"manifest", part.Format, part.Schema, part.LastSeq, part.NextID})
}

// EndPart is the last part: the count of the parts between the manifest and it.
type EndPart struct {
	Writes int64
}

// StoreWriteKind is "end".
func (*EndPart) StoreWriteKind() string { return "end" }

type endJSON struct {
	Kind   string `json:"kind"`
	Writes int64  `json:"writes"`
}

// MarshalJSON is the end part in Pi's key order.
func (part *EndPart) MarshalJSON() ([]byte, error) {
	return jsstring.MarshalJSON(endJSON{"end", part.Writes})
}

// safe is an integer from min up to 2^53 - 1: seqs, ids, versions and counts stay exact (a seq after 2^53 would
// repeat).
func safe(min int64) contracts.TSchema {
	return contracts.Int(contracts.Min(float64(min)), contracts.Max(float64(MaxSafeInteger)))
}

var (
	archiveVersion = safe(1)

	archiveColumn = contracts.Obj(
		contracts.Prop("type", contracts.Union([]contracts.TSchema{
			contracts.Lit("str"), contracts.Lit("int"), contracts.Lit("num"), contracts.Lit("bool"), contracts.Lit("json"),
		})),
		contracts.Prop("optional", contracts.Opt(contracts.Lit(true))),
		contracts.Prop("default", contracts.Opt(contracts.Union([]contracts.TSchema{contracts.Str(), contracts.Num(), contracts.Bool()}))),
		contracts.Prop("version", contracts.Opt(archiveVersion)),
		contracts.Prop("fingerprint", contracts.Opt(contracts.Str())),
	)

	archiveVersions = contracts.Map(archiveVersion)

	archiveTable = contracts.Obj(
		contracts.Prop("kind", contracts.Lit("table")),
		contracts.Prop("table", contracts.Str()),
		contracts.Prop("version", archiveVersion),
		contracts.Prop("state", contracts.Union([]contracts.TSchema{contracts.Lit("ready"), contracts.Lit("migrating")})),
		contracts.Prop("columns", contracts.Map(archiveColumn)),
		contracts.Prop("key", contracts.Arr(contracts.Str())),
		contracts.Prop("fingerprint", contracts.Str()),
	)

	archivePut = contracts.Obj(
		contracts.Prop("kind", contracts.Lit("put")),
		contracts.Prop("table", contracts.Str()),
		contracts.Prop("row", contracts.Map(contracts.Unknown())),
		contracts.Prop("version", archiveVersion),
		contracts.Prop("columns", archiveVersions),
	)

	archiveIndex = contracts.Obj(
		contracts.Prop("kind", contracts.Lit("index")),
		contracts.Prop("table", contracts.Str()),
		contracts.Prop("name", contracts.Str()),
		contracts.Prop("columns", contracts.Arr(contracts.Str())),
	)

	archiveRecord = contracts.Obj(
		contracts.Prop("id", safe(1)),
		contracts.Prop("owner", contracts.Str()),
		contracts.Prop("namespace", contracts.Str()),
		contracts.Prop("kind", contracts.Str()),
		// Absent for a singleton.
		contracts.Prop("key", contracts.Opt(contracts.Str())),
		contracts.Prop("history", contracts.Union([]contracts.TSchema{contracts.Lit("none"), contracts.Lit("latest"), contracts.Lit("rewindable")})),
		contracts.Prop("createdAt", safe(1)),
		contracts.Prop("retiredAt", contracts.Opt(safe(1))),
		contracts.Prop("meta", contracts.Opt(contracts.Str())),
		contracts.Prop("revision", safe(1)),
		contracts.Prop("version", archiveVersion),
		contracts.Prop("fingerprint", contracts.Str()),
	)

	archiveBase = contracts.Obj(
		contracts.Prop("version", archiveVersion),
		contracts.Prop("fingerprint", contracts.Str()),
		contracts.Prop("value", contracts.Unknown()),
	)

	// archiveOps are a delta's ops; their shapes are checked where they are applied.
	archiveOps = contracts.Arr(contracts.Unknown())

	archiveRevision = contracts.Obj(
		contracts.Prop("kind", contracts.Lit("document.revision")),
		contracts.Prop("id", safe(1)),
		contracts.Prop("seq", safe(1)),
		contracts.Prop("content", contracts.Union([]contracts.TSchema{
			contracts.Obj(contracts.Prop("kind", contracts.Lit("base")), contracts.Prop("version", archiveVersion), contracts.Prop("value", contracts.Unknown())),
			contracts.Obj(contracts.Prop("kind", contracts.Lit("delta")), contracts.Prop("version", archiveVersion), contracts.Prop("ops", archiveOps)),
		})),
	)

	archivePartSchema = contracts.Union([]contracts.TSchema{
		contracts.Obj(
			contracts.Prop("kind", contracts.Lit("manifest")),
			contracts.Prop("format", safe(1)),
			contracts.Prop("schema", safe(1)),
			contracts.Prop("lastSeq", safe(0)),
			contracts.Prop("nextId", safe(1)),
		),
		archiveTable,
		archivePut,
		archiveIndex,
		contracts.Obj(contracts.Prop("kind", contracts.Lit("document.create")), contracts.Prop("record", archiveRecord)),
		archiveRevision,
		contracts.Obj(contracts.Prop("kind", contracts.Lit("end")), contracts.Prop("writes", safe(0))),
	})

	archiveCount = safe(0)

	archiveEdit = contracts.Union([]contracts.TSchema{
		contracts.Obj(contracts.Prop("kind", contracts.Lit("rename")), contracts.Prop("from", contracts.Str()), contracts.Prop("to", contracts.Str())),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("add")), contracts.Prop("name", contracts.Str()), contracts.Prop("column", archiveColumn)),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("drop")), contracts.Prop("name", contracts.Str())),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("json")), contracts.Prop("name", contracts.Str()), contracts.Prop("column", archiveColumn)),
	})

	archiveKey = contracts.Map(contracts.Union([]contracts.TSchema{contracts.Str(), contracts.Num(), contracts.Bool()}))

	// archiveWrite is one recorded write of a commit (a batch's); `where` and `set` nodes are checked by the applier.
	archiveWrite = contracts.Union([]contracts.TSchema{
		archiveTable,
		archivePut,
		archiveIndex,
		contracts.Obj(contracts.Prop("kind", contracts.Lit("dropIndex")), contracts.Prop("table", contracts.Str()), contracts.Prop("name", contracts.Str())),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("alter")), contracts.Prop("table", contracts.Str()), contracts.Prop("version", archiveVersion), contracts.Prop("edit", archiveEdit)),
		contracts.Obj(
			contracts.Prop("kind", contracts.Lit("update")),
			contracts.Prop("table", contracts.Str()),
			contracts.Prop("key", archiveKey),
			contracts.Prop("set", contracts.Unknown()),
			contracts.Prop("columns", archiveVersions),
			contracts.Prop("count", archiveCount),
			contracts.Prop("version", contracts.Opt(archiveVersion)),
		),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("delete")), contracts.Prop("table", contracts.Str()), contracts.Prop("key", archiveKey), contracts.Prop("count", archiveCount)),
		contracts.Obj(
			contracts.Prop("kind", contracts.Lit("updateWhere")),
			contracts.Prop("table", contracts.Str()),
			contracts.Prop("where", contracts.Unknown()),
			contracts.Prop("set", contracts.Unknown()),
			contracts.Prop("columns", archiveVersions),
			contracts.Prop("count", archiveCount),
			contracts.Prop("version", contracts.Opt(archiveVersion)),
		),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("deleteWhere")), contracts.Prop("table", contracts.Str()), contracts.Prop("where", contracts.Unknown()), contracts.Prop("count", archiveCount)),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("document.create")), contracts.Prop("record", archiveRecord), contracts.Prop("base", archiveBase)),
		contracts.Obj(
			contracts.Prop("kind", contracts.Lit("document.change")),
			contracts.Prop("id", safe(1)),
			contracts.Prop("revision", safe(1)),
			contracts.Prop("content", contracts.Union([]contracts.TSchema{
				contracts.Obj(contracts.Prop("kind", contracts.Lit("base")), contracts.Prop("version", archiveVersion), contracts.Prop("fingerprint", contracts.Str()), contracts.Prop("value", contracts.Unknown())),
				contracts.Obj(contracts.Prop("kind", contracts.Lit("delta")), contracts.Prop("version", archiveVersion), contracts.Prop("ops", archiveOps)),
			})),
		),
		contracts.Obj(contracts.Prop("kind", contracts.Lit("document.retire")), contracts.Prop("id", safe(1)), contracts.Prop("revision", safe(1))),
	})

	// archivePartOwner and archiveWriteOwner are Pi's `{ schema: PART }` and `{ schema: WRITE }` owners of the
	// compiled validators.
	archivePartOwner  = &contracts.ParamsData{Schema: archivePartSchema}
	archiveWriteOwner = &contracts.ParamsData{Schema: archiveWrite}
)

// CheckPart checks one imported part against the archive schema (a trust boundary); `invalid/args` naming its
// position. part is a typed ArchivePart (returned as it is once its JSON passes) or a decoded JSON value (decoded into
// its typed part).
func CheckPart(part any, position int, ctx context.Context) (ArchivePart, error) {
	checked, err := checkedValue(part, ctx, archivePartOwner, func() error {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("import: part %d: not an archive part", position))
	})
	if err != nil {
		return nil, err
	}
	if typed, ok := part.(ArchivePart); ok {
		return typed, nil
	}
	return decodeArchivePart(checked)
}

// CheckWrite checks one write of a batch's commit against the write schema (a trust boundary); `what` names its place.
// write is a typed StoreWrite (returned as it is once its JSON passes) or a decoded JSON value (decoded into its
// typed write; the `where` and `set` nodes of the update and delete kinds are decoded here and refused as the
// write is, by `what`).
func CheckWrite(write any, what string, ctx context.Context) (StoreWrite, error) {
	checked, err := checkedValue(write, ctx, archiveWriteOwner, func() error {
		return data.Invalid(data.ReasonArgs, what+": not a store write")
	})
	if err != nil {
		return nil, err
	}
	if typed, ok := write.(StoreWrite); ok {
		return typed, nil
	}
	object, ok := checked.(*delta.JsonObject)
	if !ok {
		return nil, data.Invalid(data.ReasonArgs, what+": not a store write")
	}
	return decodeStoreWrite(object, what)
}

// checkedValue is the JSON form of value, checked against owner's schema (counted); refused returns the error of a
// failed check. A typed value is read through its JSON text.
func checkedValue(value any, ctx context.Context, owner *contracts.ParamsData, refused func() error) (any, error) {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.data.validations", telemetry.InstrumentOptions{}).Add(1, nil)
	validator, err := data.ValidatorFor(owner, ctx)
	if err != nil {
		return nil, err
	}
	json, err := jsonForm(value)
	if err != nil {
		// A value JSON.stringify cannot encode is no part.
		return nil, refused()
	}
	if !validator.Check(json) {
		return nil, refused()
	}
	return json, nil
}

// jsonForm is value as decoded JSON: a typed part or write through its JSON text, anything else as it is.
func jsonForm(value any) (any, error) {
	switch value.(type) {
	case StoreWrite:
		// ArchivePart has StoreWrite's method set: one case covers both.
		text, err := jsstring.MarshalJSON(value)
		if err != nil {
			return nil, err
		}
		return delta.DecodeJson(text)
	}
	return value, nil
}

// decodeArchivePart is a part that passed the archive schema as its typed value.
func decodeArchivePart(checked any) (ArchivePart, error) {
	object, ok := checked.(*delta.JsonObject)
	if !ok {
		return nil, data.Invalid(data.ReasonArgs, "import: not an archive part")
	}
	switch jsonText(object, "kind") {
	case "manifest":
		return &ManifestPart{
			Format: jsonInt(object, "format"), Schema: jsonInt(object, "schema"),
			LastSeq: jsonInt(object, "lastSeq"), NextID: jsonInt(object, "nextId"),
		}, nil
	case "end":
		return &EndPart{Writes: jsonInt(object, "writes")}, nil
	}
	return decodeStoreWrite(object, "import")
}

// decodeStoreWrite is a write that passed the archive or write schema as its typed value. The `where` and `set`
// nodes are decoded here, and refused `invalid/args` naming `what` when they are no node (Pi's applier checks them).
func decodeStoreWrite(object *delta.JsonObject, what string) (StoreWrite, error) {
	node := func(err error) error { return data.Invalid(data.ReasonArgs, what+": "+err.Error()) }
	table := jsonText(object, "table")
	switch jsonText(object, "kind") {
	case "table":
		columns, err := ColumnRecordsFromJSON(object.Value("columns"))
		if err != nil {
			return nil, data.Invalid(data.ReasonArgs, what+": "+err.Error())
		}
		return &TableDefinitionWrite{
			Table: table, Version: int(jsonInt(object, "version")), State: TableState(jsonText(object, "state")),
			Columns: columns, Key: jsonTexts(object, "key"), Fingerprint: jsonText(object, "fingerprint"),
		}, nil
	case "put":
		row, _ := object.Value("row").(*delta.JsonObject)
		versions, _ := object.Value("columns").(*delta.JsonObject)
		return &PutWrite{Table: table, Row: row, Version: int(jsonInt(object, "version")), Columns: versions}, nil
	case "index":
		return &IndexWrite{Table: table, Name: jsonText(object, "name"), Columns: jsonTexts(object, "columns")}, nil
	case "dropIndex":
		return &DropIndexWrite{Table: table, Name: jsonText(object, "name")}, nil
	case "alter":
		edit, err := DecodeTableEdit(object.Value("edit"))
		if err != nil {
			return nil, node(err)
		}
		return &AlterWrite{Table: table, Version: int(jsonInt(object, "version")), Edit: edit}, nil
	case "update":
		set, err := DecodeSetNode(object.Value("set"))
		if err != nil {
			return nil, node(err)
		}
		key, _ := object.Value("key").(*delta.JsonObject)
		versions, _ := object.Value("columns").(*delta.JsonObject)
		return &UpdateWrite{Table: table, Key: key, Set: set, Columns: versions, Count: jsonInt(object, "count"), Version: jsonOptionalVersion(object)}, nil
	case "delete":
		key, _ := object.Value("key").(*delta.JsonObject)
		return &DeleteWrite{Table: table, Key: key, Count: jsonInt(object, "count")}, nil
	case "updateWhere":
		where, err := DecodeWhereNode(object.Value("where"))
		if err != nil {
			return nil, node(err)
		}
		set, err := DecodeSetNode(object.Value("set"))
		if err != nil {
			return nil, node(err)
		}
		versions, _ := object.Value("columns").(*delta.JsonObject)
		return &UpdateWhereWrite{Table: table, Where: where, Set: set, Columns: versions, Count: jsonInt(object, "count"), Version: jsonOptionalVersion(object)}, nil
	case "deleteWhere":
		where, err := DecodeWhereNode(object.Value("where"))
		if err != nil {
			return nil, node(err)
		}
		return &DeleteWhereWrite{Table: table, Where: where, Count: jsonInt(object, "count")}, nil
	case "document.create":
		record, _ := object.Value("record").(*delta.JsonObject)
		create := &DocumentCreate{Record: documentRecordOf(record)}
		if base, present := object.Value("base").(*delta.JsonObject); present {
			create.Base = &DocumentBase{Version: jsonInt(base, "version"), Fingerprint: jsonText(base, "fingerprint"), Value: base.Value("value")}
		}
		return create, nil
	case "document.change":
		content, _ := object.Value("content").(*delta.JsonObject)
		ops, err := jsonOps(content, what)
		if err != nil {
			return nil, err
		}
		return &DocumentChange{
			ID: jsonInt(object, "id"), Revision: jsonInt(object, "revision"),
			Content: DocumentContent{
				Kind: jsonText(content, "kind"), Version: jsonInt(content, "version"), Fingerprint: jsonText(content, "fingerprint"),
				Value: content.Value("value"), Ops: ops,
			},
		}, nil
	case "document.retire":
		return &DocumentRetire{ID: jsonInt(object, "id"), Revision: jsonInt(object, "revision")}, nil
	case "document.revision":
		content, _ := object.Value("content").(*delta.JsonObject)
		ops, err := jsonOps(content, what)
		if err != nil {
			return nil, err
		}
		return &DocumentRevisionWrite{
			ID: jsonInt(object, "id"), Seq: jsonInt(object, "seq"),
			Content: RevisionContent{Kind: jsonText(content, "kind"), Version: jsonInt(content, "version"), Value: content.Value("value"), Ops: ops},
		}, nil
	}
	return nil, data.Invalid(data.ReasonArgs, what+": not a store write")
}

// documentRecordOf is a checked record object as its typed value.
func documentRecordOf(record *delta.JsonObject) DocumentRecord {
	out := DocumentRecord{
		ID: jsonInt(record, "id"), Owner: jsonText(record, "owner"), Namespace: jsonText(record, "namespace"), Kind: jsonText(record, "kind"),
		History: DocumentHistory(jsonText(record, "history")), CreatedAt: jsonInt(record, "createdAt"),
		Revision: jsonInt(record, "revision"), Version: jsonInt(record, "version"), Fingerprint: jsonText(record, "fingerprint"),
	}
	if key, present := record.Value("key").(string); present {
		out.Key = &key
	}
	if retiredAt, present := record.Value("retiredAt").(float64); present {
		at := int64(retiredAt)
		out.RetiredAt = &at
	}
	if meta, present := record.Value("meta").(string); present {
		out.Meta = &meta
	}
	return out
}

// jsonOps are the `ops` of a checked delta content; nil for a base. An op that is no array cannot be held by a typed
// revision and is refused (Pi stores it; the applier never reads an op's shape here).
func jsonOps(content *delta.JsonObject, what string) ([]delta.Op, error) {
	items, present := content.Value("ops").([]any)
	if !present {
		return nil, nil
	}
	ops := make([]delta.Op, len(items))
	for index, item := range items {
		op, isArray := item.([]any)
		if !isArray {
			return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("%s: op %d is not an array", what, index))
		}
		ops[index] = op
	}
	return ops, nil
}

// jsonText is the string member `key` of a checked object ("" when absent).
func jsonText(object *delta.JsonObject, key string) string {
	text, _ := object.Value(key).(string)
	return text
}

// jsonInt is the integer member `key` of a checked object (0 when absent): the schema admits safe integers only.
func jsonInt(object *delta.JsonObject, key string) int64 {
	number, _ := object.Value(key).(float64)
	return int64(number)
}

// jsonOptionalVersion is the optional `version` of an update write.
func jsonOptionalVersion(object *delta.JsonObject) *int {
	number, present := object.Value("version").(float64)
	if !present {
		return nil
	}
	version := int(number)
	return &version
}

// jsonTexts are the strings of the array member `key` of a checked object.
func jsonTexts(object *delta.JsonObject, key string) []string {
	items, _ := object.Value(key).([]any)
	texts := make([]string, len(items))
	for index, item := range items {
		texts[index], _ = item.(string)
	}
	return texts
}
