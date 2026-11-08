package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/node/app/files-index.ts
//
// The index of one host's files (Files v2, decision "Files v2"): four tables of the host's store and two documents. `files`
// holds one row per version (its space, folder, path, version, blob, size, metadata), `folders` one row per folder, `blobs`
// one row per blob on disk with its reference count, `paths` each path's highest version ever (kept through removals: no
// number is used twice for a path); the `summary` document per space (counts, bytes, the newest versions) is what an app
// mirrors to its server, the `usage` document the bytes on disk the caps count. Reads by id, by path, by folder, by `ref`
// and by `key`; every change is one transaction, computed by its caller's turn (the object runs one change at a time), so
// a version number, a key and a blob's count are decided once.
//
// Go forced differences (the type-level ones of Pi's `Table<C, K>` are designed out, see store/table_handle.go):
//   - FileInfo, FolderInfo and FolderEntry are structs with JSON() (the schema form a method answers with); a rows
//     schema's `ref?`, `key?` and `meta?` are "" / nil when absent. JSON numbers are float64, table integers int64.
//   - A query's `(f) => and(...)` callbacks are the Conditions built over the table's column references.
//   - `crypto.randomUUID()` is a google/uuid version 4; `Date.now()` is time.Now().UnixMilli().

// FileInfo is one version of a file as the index gives it.
type FileInfo struct {
	ID      string
	Space   string
	Path    string
	Version int64
	Size    int64
	Mime    string
	Source  string
	Ref     string
	Key     string
	Created int64
	// Meta is the free JSON metadata; nil when the version has none.
	Meta delta.JsonValue
}

// JSON is the version as `FileSchema` has it (absent `ref`, `key` and `meta` left out).
func (file FileInfo) JSON() *delta.JsonObject {
	object := delta.JsonObjectOf("id", file.ID, "space", file.Space, "path", file.Path, "version", float64(file.Version),
		"size", float64(file.Size), "mime", file.Mime, "source", file.Source)
	if file.Ref != "" {
		object.Set("ref", file.Ref)
	}
	if file.Key != "" {
		object.Set("key", file.Key)
	}
	object.Set("created", float64(file.Created))
	if file.Meta != nil {
		object.Set("meta", file.Meta)
	}
	return object
}

// fileInfoOf reads a version back from its JSON form (a summary's newest list).
func fileInfoOf(object *delta.JsonObject) FileInfo {
	file := FileInfo{
		ID: object.Value("id").(string), Space: object.Value("space").(string), Path: object.Value("path").(string),
		Version: int64(object.Value("version").(float64)), Size: int64(object.Value("size").(float64)), //portlint:allow numbers a summary this package wrote: integers
		Mime: object.Value("mime").(string), Source: object.Value("source").(string),
		Created: int64(object.Value("created").(float64)), //portlint:allow numbers a summary this package wrote: an integer
	}
	file.Ref, _ = object.Value("ref").(string)
	file.Key, _ = object.Value("key").(string)
	file.Meta = object.Value("meta")
	return file
}

// FolderInfo is a folder as the index gives it.
type FolderInfo struct {
	Space   string
	Path    string
	Created int64
}

// JSON is the folder as `FolderSchema` has it.
func (folder FolderInfo) JSON() *delta.JsonObject {
	return delta.JsonObjectOf("space", folder.Space, "path", folder.Path, "created", float64(folder.Created))
}

// FolderEntryKind is what a FolderEntry holds.
type FolderEntryKind string

// The kinds of a folder entry.
const (
	FolderEntryFolder FolderEntryKind = "folder"
	FolderEntryFile   FolderEntryKind = "file"
)

// FolderEntry is one entry of a folder's listing: a folder (Kind "folder"), or a file's newest version (Kind "file").
type FolderEntry struct {
	Kind   FolderEntryKind
	Folder *FolderInfo
	File   *FileInfo
}

// JSON is the entry as `EntrySchema` has it.
func (entry FolderEntry) JSON() *delta.JsonObject {
	if entry.Kind == FolderEntryFolder {
		return delta.JsonObjectOf("kind", "folder", "folder", entry.Folder.JSON())
	}
	return delta.JsonObjectOf("kind", "file", "file", entry.File.JSON())
}

// FileWrite is what a write stores besides its bytes: where, its type, who wrote it, its producer's reference, its key,
// `meta`, and for an import the time the file was made elsewhere (`created`, ms since the epoch; the write's time
// otherwise).
type FileWrite struct {
	Space  string
	Path   string
	Mime   string
	Source string
	Ref    string
	Key    string
	// Created is zero when the write's time counts.
	Created int64
	Meta    delta.JsonValue
}

// MetaSchema is `meta`'s schema: any JSON (stored as one JSON cell, so what is written is JSON; the schema cannot say so).
var MetaSchema = contracts.Unknown()

// FileSchema is a file version's schema (FileInfo).
var FileSchema = contracts.Obj(
	contracts.Prop("id", contracts.Str()), contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str()),
	contracts.Prop("version", contracts.Int()), contracts.Prop("size", contracts.Int()), contracts.Prop("mime", contracts.Str()),
	contracts.Prop("source", contracts.Str()), contracts.Prop("ref", contracts.Opt(contracts.Str())),
	contracts.Prop("key", contracts.Opt(contracts.Str())), contracts.Prop("created", contracts.Int()),
	contracts.Prop("meta", contracts.Opt(MetaSchema)),
)

// FolderSchema is a folder's schema (FolderInfo).
var FolderSchema = contracts.Obj(
	contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("created", contracts.Int()),
)

// SummarySchema is a space summary's schema.
var SummarySchema = contracts.Obj(
	contracts.Prop("files", contracts.Int()), contracts.Prop("versions", contracts.Int()), contracts.Prop("bytes", contracts.Int()),
	contracts.Prop("newest", contracts.Arr(FileSchema)),
)

func mustTable(name string, options store.TableOptions) *store.TableDefinition {
	definition, err := store.DefineTable(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

func mustState(name string, options documents.StateOptions) *documents.StateDefinition {
	definition, err := documents.DefineState(name, options)
	if err != nil {
		panic(err)
	}
	return definition
}

// The columns of a file version. `ref` and `key` are "" when absent (index columns are never `opt`).
var fileRows = mustTable("files", store.TableOptions{
	Version: 1,
	Columns: []store.ColumnProp{
		store.Col("id", store.Str()), store.Col("space", store.Str()), store.Col("folder", store.Str()), store.Col("path", store.Str()),
		store.Col("version", store.Int()), store.Col("latest", store.Int()), store.Col("blob", store.Str()), store.Col("size", store.Int()),
		store.Col("mime", store.Str()), store.Col("source", store.Str()), store.Col("ref", store.Str()), store.Col("key", store.Str()),
		store.Col("created", store.Int()), store.Col("meta", store.Opt(store.Json(contracts.Unknown(), store.JsonColumnOptions{Version: 1}))),
	},
	Key: []string{"id"},
	Indexes: store.IndexRecords{
		{Name: "by_folder", Columns: []string{"space", "folder", "latest", "path"}},
		{Name: "by_path", Columns: []string{"space", "path", "version"}},
		{Name: "by_ref", Columns: []string{"space", "ref", "created"}},
		{Name: "by_key", Columns: []string{"space", "key"}},
		{Name: "by_created", Columns: []string{"space", "created"}},
	},
})

// folderRows are the rows of folders (the root, "", has none).
var folderRows = mustTable("folders", store.TableOptions{
	Version: 1,
	Columns: []store.ColumnProp{
		store.Col("id", store.Str()), store.Col("space", store.Str()), store.Col("parent", store.Str()), store.Col("path", store.Str()),
		store.Col("created", store.Int()),
	},
	Key:     []string{"id"},
	Indexes: store.IndexRecords{{Name: "by_path", Columns: []string{"space", "path"}}, {Name: "by_parent", Columns: []string{"space", "parent", "path"}}},
})

// markRows are the highest version each path ever had, kept when its versions or the path itself are removed, so a
// version number is never used twice for a path (links name a version by path and number).
var markRows = mustTable("paths", store.TableOptions{
	Version: 1,
	Columns: []store.ColumnProp{store.Col("space", store.Str()), store.Col("path", store.Str()), store.Col("last", store.Int())},
	Key:     []string{"space", "path"},
})

// blobRows are the blobs on disk and how many versions reference each.
var blobRows = mustTable("blobs", store.TableOptions{
	Version: 1,
	Columns: []store.ColumnProp{store.Col("id", store.Str()), store.Col("size", store.Int()), store.Col("refs", store.Int())},
	Key:     []string{"id"},
})

func checkpointAt50(_ *delta.JsonObject, _ []delta.Op, info documents.CheckpointInfo) bool {
	return info.DeltasSinceBase >= 50
}

// SpaceSummary is what a space holds: its files (paths), their versions, their bytes (every version's) and the newest versions.
type SpaceSummary struct {
	Files    int64
	Versions int64
	Bytes    int64
	Newest   []FileInfo
}

// JSON is the summary as its document.
func (s SpaceSummary) JSON() *delta.JsonObject {
	return delta.JsonObjectOf("files", float64(s.Files), "versions", float64(s.Versions), "bytes", float64(s.Bytes), "newest", infoList(s.Newest)) //portlint:allow numbers a count the index wrote: an integer
}

// summaries are the summary of each space, by space.
var summaries = mustState("summary", documents.StateOptions{
	Schema:  SummarySchema,
	Version: 1,
	Initial: func() *delta.JsonObject {
		return SpaceSummary{}.JSON()
	},
	Keyed:          true,
	Persist:        true,
	CheckpointWhen: checkpointAt50,
})

// usage is the bytes of every blob on disk: what the host's total cap counts.
var usage = mustState("usage", documents.StateOptions{
	Schema:         contracts.Obj(contracts.Prop("bytes", contracts.Int())),
	Version:        1,
	Initial:        func() *delta.JsonObject { return delta.JsonObjectOf("bytes", float64(0)) },
	Persist:        true,
	CheckpointWhen: checkpointAt50,
})

// Newest is how many of a space's newest versions its summary keeps.
const Newest = 20

// MaxPage is the most a page holds.
const MaxPage = 1000

// BlobUse is a blob a new version references: a fresh one (just written) or one other versions share.
type BlobUse struct {
	ID    string
	Size  int64
	Fresh bool
}

// Page is a page of items and the cursor of the next one (nil when none follows).
type Page[T any] struct {
	Items []T
	Next  *string
}

// FilesIndex is one host's index of files, over its object's tables and documents. Reads see committed state; each change
// is one transaction and returns the blobs no version references any more (for the caller to delete).
type FilesIndex struct {
	// Summary is the summary document of each space.
	Summary *documents.Keyed
	// Usage is the bytes of every blob on disk.
	Usage   *documents.Singleton
	files   *store.TableHandle
	folders *store.TableHandle
	blobs   *store.TableHandle
	marks   *store.TableHandle
	self    host.StoreSelf
}

// NewFilesIndex is the index of the object self declares it in (its factory's).
func NewFilesIndex(self host.StoreSelf) *FilesIndex {
	return &FilesIndex{
		files: self.Table(fileRows), folders: self.Table(folderRows), blobs: self.Table(blobRows), marks: self.Table(markRows),
		Summary: self.State(summaries).(*documents.Keyed), Usage: self.State(usage).(*documents.Singleton), self: self,
	}
}

func col(name string) *store.ColumnRef { return store.NewColumnRef(name) }

func int64Of(row store.Row, name string) int64 { return row[name].(int64) }

func stringOf(row store.Row, name string) string { return row[name].(string) }

// infoOf is a stored version as the index gives it.
func infoOf(row store.Row) FileInfo {
	file := FileInfo{
		ID: stringOf(row, "id"), Space: stringOf(row, "space"), Path: stringOf(row, "path"), Version: int64Of(row, "version"),
		Size: int64Of(row, "size"), Mime: stringOf(row, "mime"), Source: stringOf(row, "source"), Ref: stringOf(row, "ref"),
		Key: stringOf(row, "key"), Created: int64Of(row, "created"),
	}
	// A json(unknown()) cell: JSON as it was written.
	file.Meta = row["meta"]
	return file
}

func folderOf(row store.Row) FolderInfo {
	return FolderInfo{Space: stringOf(row, "space"), Path: stringOf(row, "path"), Created: int64Of(row, "created")}
}

func infos(page store.Page) Page[FileInfo] {
	items := make([]FileInfo, len(page.Items))
	for index, row := range page.Items {
		items[index] = infoOf(row)
	}
	return Page[FileInfo]{Items: items, Next: page.Next}
}

func pageOptions(after *string) store.PageOptions { return store.PageOptions{After: after} }

// File is the version id, or nil.
func (index *FilesIndex) File(id string, ctx context.Context) (*FileInfo, error) {
	row, err := index.files.Get(ctx, store.Row{"id": id})
	if err != nil || row == nil {
		return nil, err
	}
	file := infoOf(row)
	return &file, nil
}

// BlobOf is the blob of version id and its size, or nil.
func (index *FilesIndex) BlobOf(id string, ctx context.Context) (blob string, size int64, found bool, err error) {
	row, err := index.files.Get(ctx, store.Row{"id": id})
	if err != nil || row == nil {
		return "", 0, false, err
	}
	return stringOf(row, "blob"), int64Of(row, "size"), true, nil
}

// Get is version version of the file at path (its newest without one: 0), or nil.
func (index *FilesIndex) Get(space, path string, version int64, ctx context.Context) (*FileInfo, error) {
	row, err := index.version(space, path, version, ctx)
	if err != nil || row == nil {
		return nil, err
	}
	file := infoOf(row)
	return &file, nil
}

// ByKey is the version written with key in space, or nil.
func (index *FilesIndex) ByKey(space, key string, ctx context.Context) (*FileInfo, error) {
	q := store.NewQuery(fileRows).Where(func(f store.ColumnRefs) store.Condition {
		return store.And(store.Eq(f["space"], space), store.Eq(f["key"], key))
	})
	page, err := index.files.Page(ctx, q, 1, store.PageOptions{})
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	file := infoOf(page.Items[0])
	return &file, nil
}

// ByRef is a page of the versions with ref in space, oldest first.
func (index *FilesIndex) ByRef(space, ref string, limit int64, after *string, ctx context.Context) (Page[FileInfo], error) {
	q := store.NewQuery(fileRows).
		Where(func(f store.ColumnRefs) store.Condition {
			return store.And(store.Eq(f["space"], space), store.Eq(f["ref"], ref))
		}).
		OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["created"] }, "asc")
	page, err := index.files.Page(ctx, q, limit, pageOptions(after))
	if err != nil {
		return Page[FileInfo]{}, err
	}
	return infos(page), nil
}

// Versions is a page of the versions of the file at path, newest first.
func (index *FilesIndex) Versions(space, path string, limit int64, after *string, ctx context.Context) (Page[FileInfo], error) {
	q := store.NewQuery(fileRows).
		Where(func(f store.ColumnRefs) store.Condition {
			return store.And(store.Eq(f["space"], space), store.Eq(f["path"], path))
		}).
		OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["version"] }, "desc")
	page, err := index.files.Page(ctx, q, limit, pageOptions(after))
	if err != nil {
		return Page[FileInfo]{}, err
	}
	return infos(page), nil
}

// List is a page of folder `folder`'s entries ("" is the root): its folders, then its files' newest versions, each by path.
// The cursor names the part it continues.
func (index *FilesIndex) List(space, folder string, limit int64, after *string, ctx context.Context) (Page[FolderEntry], error) {
	cursor, err := listCursor(after)
	if err != nil {
		return Page[FolderEntry]{}, err
	}
	entries := []FolderEntry{}
	folders := store.NewQuery(folderRows).
		Where(func(f store.ColumnRefs) store.Condition {
			return store.And(store.Eq(f["space"], space), store.Eq(f["parent"], folder))
		}).
		OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["path"] }, "asc")
	files := store.NewQuery(fileRows).
		Where(func(f store.ColumnRefs) store.Condition {
			return store.And(store.Eq(f["space"], space), store.Eq(f["folder"], folder), store.Eq(f["latest"], int64(1)))
		}).
		OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["path"] }, "asc")
	if cursor.part == "folders" {
		page, err := index.folders.Page(ctx, folders, limit, pageOptions(cursor.after))
		if err != nil {
			return Page[FolderEntry]{}, err
		}
		for _, row := range page.Items {
			info := folderOf(row)
			entries = append(entries, FolderEntry{Kind: FolderEntryFolder, Folder: &info})
		}
		if page.Next != nil {
			return Page[FolderEntry]{Items: entries, Next: cursorText(map[string]any{"folders": *page.Next})}, nil
		}
	}
	room := limit - int64(len(entries))
	var options *string
	if cursor.part == "files" {
		options = cursor.after
	}
	// A page the folders filled continues with the files only when there are some.
	page, err := index.files.Page(ctx, files, max(room, 1), pageOptions(options))
	if err != nil {
		return Page[FolderEntry]{}, err
	}
	if room == 0 {
		if len(page.Items) == 0 {
			return Page[FolderEntry]{Items: entries}, nil
		}
		next := `{"files":null}`
		return Page[FolderEntry]{Items: entries, Next: &next}, nil
	}
	for _, row := range page.Items {
		info := infoOf(row)
		entries = append(entries, FolderEntry{Kind: FolderEntryFile, File: &info})
	}
	if page.Next == nil {
		return Page[FolderEntry]{Items: entries}, nil
	}
	return Page[FolderEntry]{Items: entries, Next: cursorText(map[string]any{"files": *page.Next})}, nil
}

func cursorText(value map[string]any) *string {
	encoded, _ := jsstring.MarshalJSON(value)
	text := string(encoded)
	return &text
}

// Exists reports whether the file or folder path exists in space: "file", "folder" or "".
func (index *FilesIndex) Exists(space, path string, ctx context.Context) (string, error) {
	folder, err := index.folder(space, path, ctx)
	if err != nil {
		return "", err
	}
	if folder != nil {
		return "folder", nil
	}
	file, err := index.version(space, path, 0, ctx)
	if err != nil || file == nil {
		return "", err
	}
	return "file", nil
}

// Add adds a version at write.Path referencing blob: version 1, or one past the newest; its folders made.
func (index *FilesIndex) Add(id string, write FileWrite, blob BlobUse, ctx context.Context) (FileInfo, error) {
	space, path := write.Space, write.Path
	if folder, err := index.folder(space, path, ctx); err != nil || folder != nil {
		if err != nil {
			return FileInfo{}, err
		}
		return FileInfo{}, clash(space, path, "is a folder")
	}
	folders, err := index.missingFolders(space, ParentOf(path), ctx)
	if err != nil {
		return FileInfo{}, err
	}
	previous, err := index.version(space, path, 0, ctx)
	if err != nil {
		return FileInfo{}, err
	}
	mark, err := index.marks.Get(ctx, store.Row{"space": space, "path": path})
	if err != nil {
		return FileInfo{}, err
	}
	version := int64(0)
	if previous != nil {
		version = int64Of(previous, "version")
	}
	if mark != nil {
		version = max(version, int64Of(mark, "last"))
	}
	created := write.Created
	if created == 0 {
		created = time.Now().UnixMilli()
	}
	row := store.Row{
		"id": id, "space": space, "folder": ParentOf(path), "path": path, "version": version + 1, "latest": int64(1),
		"blob": blob.ID, "size": blob.Size, "mime": write.Mime, "source": write.Source, "ref": write.Ref, "key": write.Key,
		"created": created,
	}
	if write.Meta != nil {
		row["meta"] = write.Meta
	}
	info := infoOf(row)
	existing, err := index.Summary.Get(ctx, space)
	if err != nil {
		return FileInfo{}, err
	}
	newest := newestOf(existing)
	_, err = index.self.Tx(func(tx documents.Tx) (any, error) {
		for _, folder := range folders {
			if err := tx.Put(index.folders, folder); err != nil {
				return nil, err
			}
		}
		if previous != nil {
			if err := tx.Update(index.files, store.Row{"id": stringOf(previous, "id")}, store.Row{"latest": int64(0)}); err != nil {
				return nil, err
			}
		}
		if err := tx.Put(index.files, row); err != nil {
			return nil, err
		}
		if err := tx.Put(index.marks, store.Row{"space": space, "path": path, "last": row["version"]}); err != nil {
			return nil, err
		}
		if blob.Fresh {
			if err := tx.Put(index.blobs, store.Row{"id": blob.ID, "size": blob.Size, "refs": int64(1)}); err != nil {
				return nil, err
			}
			bytes, err := tx.Draft(index.Usage)
			if err != nil {
				return nil, err
			}
			if err := bytes.Set("bytes", bytes.Get("bytes").(float64)+float64(blob.Size)); err != nil {
				return nil, err
			}
		} else if err := tx.Update(index.blobs, store.Row{"id": blob.ID}, store.Row{"refs": store.Add(col("refs"), int64(1))}); err != nil {
			return nil, err
		}
		summary, err := tx.Draft(index.Summary, space)
		if err != nil {
			return nil, err
		}
		if previous == nil {
			_ = summary.Set("files", summary.Get("files").(float64)+1)
		}
		_ = summary.Set("versions", summary.Get("versions").(float64)+1)
		_ = summary.Set("bytes", summary.Get("bytes").(float64)+float64(blob.Size))
		all := append([]FileInfo{info}, newest...)
		sort.SliceStable(all, func(a, b int) bool { return newestFirst(all[a], all[b]) < 0 })
		if len(all) > Newest {
			all = all[:Newest]
		}
		return nil, summary.Set("newest", infoList(all))
	}, ctx)
	if err != nil {
		return FileInfo{}, err
	}
	return info, nil
}

// newestOf is a summary document's newest versions.
func newestOf(summary *delta.JsonObject) []FileInfo {
	if summary == nil {
		return nil
	}
	list, _ := summary.Value("newest").([]any)
	files := make([]FileInfo, len(list))
	for index, item := range list {
		files[index] = fileInfoOf(item.(*delta.JsonObject))
	}
	return files
}

func infoList(files []FileInfo) []any {
	list := make([]any, len(files))
	for index, file := range files {
		list[index] = file.JSON()
	}
	return list
}

// newestFirst is the order of a summary's newest versions, as `by_created` gives them: `created`, then id, descending.
func newestFirst(a, b FileInfo) int64 {
	if a.Created != b.Created {
		return b.Created - a.Created
	}
	switch {
	case a.ID < b.ID:
		return 1
	case a.ID > b.ID:
		return -1
	}
	return 0
}

// Mkdir makes folder path and its missing parents; refused where a file is.
func (index *FilesIndex) Mkdir(space, path string, ctx context.Context) (FolderInfo, error) {
	folders, err := index.missingFolders(space, path, ctx)
	if err != nil {
		return FolderInfo{}, err
	}
	if len(folders) > 0 {
		_, err := index.self.Tx(func(tx documents.Tx) (any, error) {
			for _, folder := range folders {
				if err := tx.Put(index.folders, folder); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}, ctx)
		if err != nil {
			return FolderInfo{}, err
		}
	}
	row, err := index.folder(space, path, ctx)
	if err != nil {
		return FolderInfo{}, err
	}
	return folderOf(row), nil
}

// Move moves the file or folder from to to (in space), changing only the index: every version of a file, everything under a
// folder. Refused when from does not exist, to exists or lies inside from. Versions keep their numbers; the paths left keep
// their marks, and each path moved to takes the higher of its own mark and the moved one's.
func (index *FilesIndex) Move(space, from, to string, ctx context.Context) error {
	kind, err := index.Exists(space, from, ctx)
	if err != nil {
		return err
	}
	if kind == "" {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf("files: %s:%s does not exist", space, from))
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return clash(space, to, "lies inside "+from)
	}
	if exists, err := index.Exists(space, to, ctx); err != nil {
		return err
	} else if exists != "" {
		return clash(space, to, "exists")
	}
	folders, err := index.missingFolders(space, ParentOf(to), ctx)
	if err != nil {
		return err
	}
	var moved store.Row
	if kind == "folder" {
		if moved, err = index.folder(space, from, ctx); err != nil {
			return err
		}
	}
	renamed := func(path string) string {
		if path == from {
			return to
		}
		return to + path[len(from):]
	}
	existing, err := index.Summary.Get(ctx, space)
	if err != nil {
		return err
	}
	newest := newestOf(existing)
	for position, file := range newest {
		if file.Path == from || strings.HasPrefix(file.Path, from+"/") {
			newest[position].Path = renamed(file.Path)
		}
	}
	marks, err := index.movedMarks(space, from, renamed, ctx)
	if err != nil {
		return err
	}
	_, err = index.self.Tx(func(tx documents.Tx) (any, error) {
		for _, folder := range folders {
			if err := tx.Put(index.folders, folder); err != nil {
				return nil, err
			}
		}
		for _, mark := range marks {
			if err := tx.Put(index.marks, mark); err != nil {
				return nil, err
			}
		}
		inSpace := store.Eq(col("space"), space)
		if moved == nil {
			if err := tx.UpdateWhere(index.files, store.And(inSpace, store.Eq(col("path"), from)), store.Row{"path": to, "folder": ParentOf(to)}); err != nil {
				return nil, err
			}
		} else {
			if err := tx.Update(index.folders, store.Row{"id": stringOf(moved, "id")}, store.Row{"path": to, "parent": ParentOf(to)}); err != nil {
				return nil, err
			}
			under := store.And(inSpace, store.Prefix(col("path"), from+"/"))
			if err := tx.UpdateWhere(index.folders, under, store.Row{
				"path": store.ReplacePrefix(col("path"), from+"/", to+"/"), "parent": store.ReplacePrefix(col("parent"), from, to),
			}); err != nil {
				return nil, err
			}
			if err := tx.UpdateWhere(index.files, under, store.Row{
				"path": store.ReplacePrefix(col("path"), from+"/", to+"/"), "folder": store.ReplacePrefix(col("folder"), from, to),
			}); err != nil {
				return nil, err
			}
		}
		if len(newest) > 0 {
			summary, err := tx.Draft(index.Summary, space)
			if err != nil {
				return nil, err
			}
			return nil, summary.Set("newest", infoList(newest))
		}
		return nil, nil
	}, ctx)
	return err
}

// movedMarks are the marks of the paths renamed moves (from and those under it), each the higher of its own and the
// target's.
func (index *FilesIndex) movedMarks(space, from string, renamed func(string) string, ctx context.Context) ([]store.Row, error) {
	marks := []store.Row{}
	q := store.NewQuery(markRows).Where(func(m store.ColumnRefs) store.Condition {
		return store.And(store.Eq(m["space"], space), store.Or(store.Eq(m["path"], from), store.Prefix(m["path"], from+"/")))
	})
	rows := index.marks.Rows(ctx, q)
	defer rows.Close()
	for {
		mark, ok, err := rows.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return marks, nil
		}
		path := renamed(stringOf(mark, "path"))
		target, err := index.marks.Get(ctx, store.Row{"space": space, "path": path})
		if err != nil {
			return nil, err
		}
		last := int64Of(mark, "last")
		if target != nil {
			last = max(last, int64Of(target, "last"))
		}
		marks = append(marks, store.Row{"space": space, "path": path, "last": last})
	}
}

// Removed is what removing a version did: its space, and the blobs no version references any more.
type Removed struct {
	Space string
	Gone  []string
}

// Remove removes version id (an unknown one: nil); the next older version becomes the path's newest. Its space and the
// blobs no version references any more.
func (index *FilesIndex) Remove(id string, ctx context.Context) (*Removed, error) {
	row, err := index.files.Get(ctx, store.Row{"id": id})
	if err != nil || row == nil {
		return nil, err
	}
	var older store.Row
	space, path := stringOf(row, "space"), stringOf(row, "path")
	if int64Of(row, "latest") == 1 {
		q := store.NewQuery(fileRows).
			Where(func(f store.ColumnRefs) store.Condition {
				return store.And(store.Eq(f["space"], space), store.Eq(f["path"], path), store.Lt(f["version"], int64Of(row, "version")))
			}).
			OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["version"] }, "desc")
		page, err := index.files.Page(ctx, q, 1, store.PageOptions{})
		if err != nil {
			return nil, err
		}
		if len(page.Items) > 0 {
			older = page.Items[0]
		}
	}
	pathGone := int64Of(row, "latest") == 1 && older == nil
	filesGone := int64(0)
	if pathGone {
		filesGone = 1
	}
	gone, err := index.drop(space, []store.Row{row}, filesGone, store.Ne(col("id"), id), ctx, func(tx documents.Tx) error {
		if err := tx.Delete(index.files, store.Row{"id": id}); err != nil {
			return err
		}
		if older != nil {
			return tx.Update(index.files, store.Row{"id": stringOf(older, "id")}, store.Row{"latest": int64(1)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Removed{Space: space, Gone: gone}, nil
}

// under is the condition that column is path or lies under it.
func under(column *store.ColumnRef, path string) store.Condition {
	return store.Or(store.Eq(column, path), store.Prefix(column, path+"/"))
}

// rowsOf reads every row of q.
func rowsOf(handle *store.TableHandle, q *store.Query, ctx context.Context) ([]store.Row, error) {
	rows := handle.Rows(ctx, q)
	defer rows.Close()
	var all []store.Row
	for {
		row, ok, err := rows.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return all, nil
		}
		all = append(all, row)
	}
}

// RemovePath removes the file at path (every version) or the folder at path and everything under it.
func (index *FilesIndex) RemovePath(space, path string, ctx context.Context) ([]string, error) {
	rows, err := rowsOf(index.files, store.NewQuery(fileRows).Where(func(f store.ColumnRefs) store.Condition {
		return store.And(store.Eq(f["space"], space), under(f["path"], path))
	}), ctx)
	if err != nil {
		return nil, err
	}
	paths := map[string]struct{}{}
	for _, row := range rows {
		paths[stringOf(row, "path")] = struct{}{}
	}
	return index.drop(space, rows, int64(len(paths)), store.Not(under(col("path"), path)), ctx, func(tx documents.Tx) error {
		if err := tx.DeleteWhere(index.files, store.And(store.Eq(col("space"), space), under(col("path"), path))); err != nil {
			return err
		}
		return tx.DeleteWhere(index.folders, store.And(store.Eq(col("space"), space), under(col("path"), path)))
	})
}

// RemoveSpace removes everything in space, its summary included.
func (index *FilesIndex) RemoveSpace(space string, ctx context.Context) ([]string, error) {
	rows, err := rowsOf(index.files, store.NewQuery(fileRows).Where(func(f store.ColumnRefs) store.Condition { return store.Eq(f["space"], space) }), ctx)
	if err != nil {
		return nil, err
	}
	released, err := index.released(rows, ctx)
	if err != nil {
		return nil, err
	}
	_, err = index.self.Tx(func(tx documents.Tx) (any, error) {
		if err := tx.DeleteWhere(index.files, store.Eq(col("space"), space)); err != nil {
			return nil, err
		}
		if err := tx.DeleteWhere(index.folders, store.Eq(col("space"), space)); err != nil {
			return nil, err
		}
		if err := index.release(tx, released); err != nil {
			return nil, err
		}
		return nil, tx.Retire(index.Summary, space)
	}, ctx)
	if err != nil {
		return nil, err
	}
	return released.gone, nil
}

// HasBlob reports whether blob id is referenced (a blob file without a row is a leftover of a write that never committed).
func (index *FilesIndex) HasBlob(id string, ctx context.Context) (bool, error) {
	row, err := index.blobs.Get(ctx, store.Row{"id": id})
	return row != nil, err
}

// drop removes rows (versions of space, files paths gone with them) in one transaction with writes: their blobs' counts
// lowered, the summary's counts lowered and its newest versions read again among the rows kept.
func (index *FilesIndex) drop(space string, rows []store.Row, files int64, kept store.Condition, ctx context.Context, writes func(tx documents.Tx) error) ([]string, error) {
	released, err := index.released(rows, ctx)
	if err != nil {
		return nil, err
	}
	bytes := int64(0)
	for _, row := range rows {
		bytes += int64Of(row, "size")
	}
	q := store.NewQuery(fileRows).
		Where(func(f store.ColumnRefs) store.Condition { return store.And(store.Eq(f["space"], space), kept) }).
		OrderBy(func(f store.ColumnRefs) *store.ColumnRef { return f["created"] }, "desc")
	newest, err := index.files.Page(ctx, q, Newest, store.PageOptions{})
	if err != nil {
		return nil, err
	}
	_, err = index.self.Tx(func(tx documents.Tx) (any, error) {
		if err := writes(tx); err != nil {
			return nil, err
		}
		if err := index.release(tx, released); err != nil {
			return nil, err
		}
		summary, err := tx.Draft(index.Summary, space)
		if err != nil {
			return nil, err
		}
		_ = summary.Set("files", summary.Get("files").(float64)-float64(files))
		_ = summary.Set("versions", summary.Get("versions").(float64)-float64(len(rows)))
		_ = summary.Set("bytes", summary.Get("bytes").(float64)-float64(bytes))
		list := make([]FileInfo, len(newest.Items))
		for position, row := range newest.Items {
			list[position] = infoOf(row)
		}
		return nil, summary.Set("newest", infoList(list))
	}, ctx)
	if err != nil {
		return nil, err
	}
	return released.gone, nil
}

// released is what removing rows does to their blobs: each blob's lowered count, and the blobs left without a reference.
type released struct {
	lowered []lowered
	gone    []string
	bytes   int64
}

type lowered struct {
	id string
	by int64
}

func (index *FilesIndex) released(rows []store.Row, ctx context.Context) (released, error) {
	counts := map[string]int64{}
	order := []string{}
	for _, row := range rows {
		blob := stringOf(row, "blob")
		if _, seen := counts[blob]; !seen {
			order = append(order, blob)
		}
		counts[blob]++
	}
	out := released{gone: []string{}}
	for _, id := range order {
		blob, err := index.blobs.Get(ctx, store.Row{"id": id})
		if err != nil {
			return released{}, err
		}
		if blob == nil {
			continue
		}
		if by := counts[id]; int64Of(blob, "refs") > by {
			out.lowered = append(out.lowered, lowered{id: id, by: by})
		} else {
			out.gone = append(out.gone, id)
			out.bytes += int64Of(blob, "size")
		}
	}
	return out, nil
}

// release stages released's blob writes and the usage they free.
func (index *FilesIndex) release(tx documents.Tx, released released) error {
	for _, each := range released.lowered {
		if err := tx.Update(index.blobs, store.Row{"id": each.id}, store.Row{"refs": store.Sub(col("refs"), each.by)}); err != nil {
			return err
		}
	}
	for _, id := range released.gone {
		if err := tx.Delete(index.blobs, store.Row{"id": id}); err != nil {
			return err
		}
	}
	if released.bytes > 0 {
		bytes, err := tx.Draft(index.Usage)
		if err != nil {
			return err
		}
		return bytes.Set("bytes", bytes.Get("bytes").(float64)-float64(released.bytes))
	}
	return nil
}

// version is version version of path (its newest without one: 0).
func (index *FilesIndex) version(space, path string, version int64, ctx context.Context) (store.Row, error) {
	q := store.NewQuery(fileRows).Where(func(f store.ColumnRefs) store.Condition {
		which := store.Eq(f["latest"], int64(1))
		if version != 0 {
			which = store.Eq(f["version"], version)
		}
		return store.And(store.Eq(f["space"], space), store.Eq(f["path"], path), which)
	})
	page, err := index.files.Page(ctx, q, 1, store.PageOptions{})
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	return page.Items[0], nil
}

// folder is the folder at path.
func (index *FilesIndex) folder(space, path string, ctx context.Context) (store.Row, error) {
	q := store.NewQuery(folderRows).Where(func(f store.ColumnRefs) store.Condition {
		return store.And(store.Eq(f["space"], space), store.Eq(f["path"], path))
	})
	page, err := index.folders.Page(ctx, q, 1, store.PageOptions{})
	if err != nil || len(page.Items) == 0 {
		return nil, err
	}
	return page.Items[0], nil
}

// missingFolders are the rows of path and its parents that do not exist yet, parents first; refused where a file is.
func (index *FilesIndex) missingFolders(space, path string, ctx context.Context) ([]store.Row, error) {
	missing := []store.Row{}
	created := time.Now().UnixMilli()
	for _, folder := range foldersOf(path) {
		if len(missing) == 0 {
			existing, err := index.folder(space, folder, ctx)
			if err != nil {
				return nil, err
			}
			if existing != nil {
				continue
			}
		}
		file, err := index.version(space, folder, 0, ctx)
		if err != nil {
			return nil, err
		}
		if file != nil {
			return nil, clash(space, folder, "is a file")
		}
		missing = append(missing, store.Row{"id": uuid.NewString(), "space": space, "parent": ParentOf(folder), "path": folder, "created": created})
	}
	return missing, nil
}

// listCursorState is where a listing continues: in the folders or in the files, after a cursor of that part.
type listCursorState struct {
	part  string
	after *string
}

// listCursor reads a listing's cursor back (a trust boundary: the caller's).
func listCursor(after *string) (listCursorState, error) {
	if after == nil {
		return listCursorState{part: "folders"}, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*after), &members); err != nil {
		return listCursorState{}, data.Invalid(data.ReasonArgs, "files: not a listing cursor")
	}
	// A member counts when it is a string; `"files": null` is the cursor that starts the files after the folders.
	for _, part := range []string{"folders", "files"} {
		var text string
		if raw := members[part]; len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &text) == nil {
			return listCursorState{part: part, after: &text}, nil
		}
	}
	if string(members["files"]) == "null" {
		return listCursorState{part: "files"}, nil
	}
	return listCursorState{}, data.Invalid(data.ReasonArgs, "files: not a listing cursor")
}

// spacePattern is the space name rule: lower case, digits, `.`, `_`, `-`, at most 64 characters.
var spacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// maxPath is the longest path.
const maxPath = 1024

// CheckSpace refuses a space name outside the rule (a trust boundary: the caller's).
func CheckSpace(space string) error {
	if !spacePattern.MatchString(space) {
		return data.Invalid(data.ReasonArgs, fmt.Sprintf(`files: "%s" is no space name (%s)`, space, spacePattern.String()))
	}
	return nil
}

// PathKind is what a path names: a file, or a folder (which may be the root, "").
type PathKind int

// The kinds of path CheckPath takes.
const (
	PathFile PathKind = iota
	PathFolder
)

// CheckPath refuses a path that is not `name/name/…` (a trust boundary: the caller's): no empty segment, no `.` or `..`, no
// NUL, at most 1024 characters. PathFolder allows "", the root.
func CheckPath(path string, kind PathKind) error {
	if kind == PathFolder && path == "" {
		return nil
	}
	bad := len(utf16.Encode([]rune(path))) > maxPath || strings.Contains(path, "\x00")
	if !bad {
		for segment := range strings.SplitSeq(path, "/") {
			if segment == "" || segment == "." || segment == ".." {
				bad = true
				break
			}
		}
	}
	if bad {
		units := utf16.Encode([]rune(path))
		if len(units) > 80 {
			units = units[:80]
		}
		return data.Invalid(data.ReasonArgs, fmt.Sprintf(`files: "%s" is no path (name/name/…, at most %d)`, string(utf16.Decode(units)), maxPath))
	}
	return nil
}

// ParentOf is the folder path is in ("": the root).
func ParentOf(path string) string {
	slash := strings.LastIndex(path, "/")
	if slash < 0 {
		return ""
	}
	return path[:slash]
}

// foldersOf are folder path and its parents, outermost first (none for the root).
func foldersOf(path string) []string {
	if path == "" {
		return nil
	}
	segments := strings.Split(path, "/")
	folders := make([]string, len(segments))
	for index := range segments {
		folders[index] = strings.Join(segments[:index+1], "/")
	}
	return folders
}

// clash is a refusal of a path that is taken or would hold itself.
func clash(space, path, why string) error {
	return data.Invalid(data.ReasonArgs, fmt.Sprintf("files: %s:%s %s", space, path, why), delta.JsonObjectOf("conflict", path))
}
