package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/node/app/files.ts
//
// Files (slice 13 §4; decision "Files v2"): the kit's file storage, one `Files` object per host (`self.get(Files, {})`), its
// bytes in the host's `files/` and its index in the host's store (`files_index.go`). Every file lives in a space (`shared`,
// `artifacts`, `uploads`, …) at a path (`docs/report.md`); folders are index entries, and a move changes only the index.
// Bytes never change: writing to a path adds a version (a new blob), `copy` shares a blob, and a blob goes once no version
// references it. Small files in one call (`put`), large ones in chunks (design §23 "Chunked transfers": `begin`, `append` at
// the stored offset, `uploaded` to resume after a drop, `finish`; unfinished uploads expire); a `key` makes a write
// idempotent; reads by range; caps per host; a summary per space for an app's server index; `FilesFolder` lists one folder
// live. Plugins never see a path on disk.
//
// Async contract (AGENTS.md "TypeScript async/Promise parity"). Each method handler is Pi's async function: its synchronous
// part (argument checks, and for `append` the acceptance of the bytes and of their offset) runs inside the section that
// admits the call, in call order; it suspends (chordctx.Suspend) before its first wait, and the rest blocks. `turn`, Pi's
// promise chain, is a mutex: changes run one at a time, in the order they took it. A failed handler is the rejection with
// that error.
//
// Go forced differences: Pi's generic principal parameter of `filesFacet<P>` is designed out (the facet is the same for
// every principal); `Files`' bytes are []byte, its numbers float64 on the wire; the caps are int64 (0 is the default);
// `startServer` is not ported, so the facet is loaded by the app that hosts its own hosts.

// filesName is the name of the `Files` contract: who owns its tables and documents in a store.
const filesName = "chord.app.files"

// FilesCaps are the caps of one host's files (bytes); 0 is the default.
type FilesCaps struct {
	// MaxFile is the largest file: 256 MiB by default.
	MaxFile int64
	// MaxTotal is all blobs on disk together (a copy shares its blob), uploads under way included: 4 GiB by default.
	MaxTotal int64
}

func mustLocal(name string, declaration contracts.LocalDeclaration) *contracts.Contract {
	contract, err := contracts.Local(name, declaration)
	if err != nil {
		panic(err)
	}
	return contract
}

// writeProps are a write's fields: where, its type, who wrote it (a string the writer sets), its producer's reference, key,
// `meta`, and an import's original time.
func writeProps() []contracts.Property {
	return []contracts.Property{
		contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("mime", contracts.Str()),
		contracts.Prop("source", contracts.Str()), contracts.Prop("ref", contracts.Opt(contracts.Str(contracts.Min(1)))),
		contracts.Prop("key", contracts.Opt(contracts.Str(contracts.Min(1)))), contracts.Prop("meta", contracts.Opt(MetaSchema)),
		// For imports only: when the file was made where it comes from (ms since the epoch); the write's time otherwise.
		contracts.Prop("created", contracts.Opt(contracts.Int(contracts.Min(0)))),
	}
}

// pageProps are a page's arguments: a cursor of the page before, and how many (100 by default, at most 1,000).
func pageProps() []contracts.Property {
	return []contracts.Property{
		contracts.Prop("after", contracts.Opt(contracts.Str())),
		contracts.Prop("limit", contracts.Opt(contracts.Int(contracts.Min(1), contracts.Max(MaxPage)))),
	}
}

func objWith(props []contracts.Property, more ...contracts.Property) contracts.Schema {
	return contracts.Obj(append(append([]contracts.Property{}, props...), more...)...)
}

// pageOf is a page of item schemas and the cursor of the next one.
func pageOf(item contracts.Schema) contracts.Schema {
	return contracts.Obj(contracts.Prop("items", contracts.Arr(item)), contracts.Prop("next", contracts.Opt(contracts.Str())))
}

// entrySchema is one entry of a folder's listing.
var entrySchema = contracts.Union([]contracts.Schema{
	contracts.Obj(contracts.Prop("kind", contracts.Lit("folder")), contracts.Prop("folder", FolderSchema)),
	contracts.Obj(contracts.Prop("kind", contracts.Lit("file")), contracts.Prop("file", FileSchema)),
})

var maybeFile = contracts.Union([]contracts.Schema{FileSchema, contracts.Nul()})

// Files is the host's files (one per host): writes, reads, the index's queries, folders, moves, copies, removal, summaries.
var Files = mustLocal(filesName, contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		// Each space's summary (counts, bytes, the newest versions): what an app mirrors to its server's index.
		contracts.Mem("summary", contracts.Keyed(SummarySchema)),
		// Stores a small file (at most 4 MiB) in one call as a new version at `path`, one past the highest it ever had. A
		// `key` the space has already stores nothing and answers the version written with it.
		contracts.Mem("put", contracts.NewMethod(objWith(writeProps(), contracts.Prop("bytes", contracts.Bytes())), FileSchema)),
		// Starts a chunked upload of what `finish` stores as `put` would: its upload id; with a `key` the space has, the
		// version written with it (`exists`), or the upload under way with it. `unavailable` (`busy`) while the host's cap
		// of uploads are under way.
		contracts.Mem("begin", contracts.NewMethod(contracts.Obj(writeProps()...), contracts.Union([]contracts.Schema{
			contracts.Obj(contracts.Prop("kind", contracts.Lit("upload")), contracts.Prop("upload", contracts.Str())),
			contracts.Obj(contracts.Prop("kind", contracts.Lit("exists")), contracts.Prop("file", FileSchema)),
		}))),
		// Appends a chunk (at most 4 MiB; about 1 MiB is the usual size) to upload `upload` at `offset`, which must be the
		// bytes stored so far (else `invalid`, its details `{ offset }` the stored one).
		contracts.Mem("append", contracts.NewMethod(contracts.Obj(
			contracts.Prop("upload", contracts.Str()), contracts.Prop("offset", contracts.Int(contracts.Min(0))), contracts.Prop("bytes", contracts.Bytes())))),
		// The bytes stored so far of upload `upload`: where a writer resumes after a drop.
		contracts.Mem("uploaded", contracts.NewMethod(contracts.Str(), contracts.Int())),
		// Ends upload `upload`: the version it made.
		contracts.Mem("finish", contracts.NewMethod(contracts.Str(), FileSchema)),
		// Drops upload `upload` and what it stored (an unknown one: nothing): its writer gave up.
		contracts.Mem("cancel", contracts.NewMethod(contracts.Str())),
		// `length` bytes (at most 4 MiB) of version `id` from `offset` (fewer at its end).
		contracts.Mem("read", contracts.NewMethod(contracts.Obj(
			contracts.Prop("id", contracts.Str()), contracts.Prop("offset", contracts.Int(contracts.Min(0))), contracts.Prop("length", contracts.Int(contracts.Min(0)))), contracts.Bytes())),
		// Version `id`, or `null`.
		contracts.Mem("file", contracts.NewMethod(contracts.Str(), maybeFile)),
		// Version `version` of the file at `path` (its newest without one), or `null`.
		contracts.Mem("get", contracts.NewMethod(contracts.Obj(
			contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str()), contracts.Prop("version", contracts.Opt(contracts.Int(contracts.Min(1))))), maybeFile)),
		// The version written with `key` in `space`, or `null`.
		contracts.Mem("byKey", contracts.NewMethod(contracts.Obj(
			contracts.Prop("space", contracts.Str()), contracts.Prop("key", contracts.Str(contracts.Min(1)))), maybeFile)),
		// A page of the versions with `ref` in `space`, oldest first.
		contracts.Mem("byRef", contracts.NewMethod(objWith([]contracts.Property{
			contracts.Prop("space", contracts.Str()), contracts.Prop("ref", contracts.Str(contracts.Min(1)))}, pageProps()...), pageOf(FileSchema))),
		// A page of the versions of the file at `path`, newest first.
		contracts.Mem("versions", contracts.NewMethod(objWith([]contracts.Property{
			contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str())}, pageProps()...), pageOf(FileSchema))),
		// A page of folder `folder`'s entries (`""`: the space's root): its folders, then its files' newest versions.
		contracts.Mem("list", contracts.NewMethod(objWith([]contracts.Property{
			contracts.Prop("space", contracts.Str()), contracts.Prop("folder", contracts.Str())}, pageProps()...), pageOf(entrySchema))),
		// Makes folder `path` and its missing parents (an existing one: itself); refused where a file is.
		contracts.Mem("mkdir", contracts.NewMethod(contracts.Obj(contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str())), FolderSchema)),
		// Moves (renames) the file or folder `from` to `to`, changing only the index; refused when `to` exists or lies
		// inside `from`.
		contracts.Mem("move", contracts.NewMethod(contracts.Obj(
			contracts.Prop("space", contracts.Str()), contracts.Prop("from", contracts.Str()), contracts.Prop("to", contracts.Str())))),
		// Copies version `id` as a new version at `path` of `space`, sharing its blob (its metadata but its key).
		contracts.Mem("copy", contracts.NewMethod(contracts.Obj(
			contracts.Prop("id", contracts.Str()), contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str())), FileSchema)),
		// Removes version `id` (an unknown one: nothing); the next older version becomes the path's newest.
		contracts.Mem("remove", contracts.NewMethod(contracts.Str())),
		// Removes the file at `path` (every version) or the folder at `path` with everything under it.
		contracts.Mem("removePath", contracts.NewMethod(contracts.Obj(contracts.Prop("space", contracts.Str()), contracts.Prop("path", contracts.Str())))),
		// Removes everything in space `space`.
		contracts.Mem("removeSpace", contracts.NewMethod(contracts.Str())),
	},
})

// FilesFolder is one folder of the host's files, live: its first 500 entries (folders, then files) and whether more follow.
var FilesFolder = mustLocal("chord.app.files.folder", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("space", contracts.Str()), contracts.Prop("folder", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("listing", contracts.Obj(contracts.Prop("entries", contracts.Arr(entrySchema)), contracts.Prop("more", contracts.Bool()))),
	},
})

const (
	// maxPiece is the largest piece a call carries (a `put`, an `append`, a `read`): 4 MiB, well under a link's part limit.
	maxPiece = 4 * 1024 * 1024
	// pageSize is a page's size when the caller names none.
	pageSize = 100
	// folderView is how many entries a `FilesFolder` shows.
	folderView = 500
	// uploadIdle is how long an upload may go unwritten before it expires: an hour.
	uploadIdle = 60 * time.Minute
	// sweepEvery is how often expired uploads are dropped: every minute.
	sweepEvery = time.Minute
)

func objectOf(args delta.JsonValue) *delta.JsonObject {
	object, _ := args.(*delta.JsonObject)
	if object == nil {
		return delta.NewJsonObject(0)
	}
	return object
}

func optString(object *delta.JsonObject, key string) string {
	text, _ := object.Value(key).(string)
	return text
}

func optInt(object *delta.JsonObject, key string) int64 {
	number, _ := object.Value(key).(float64)
	//portlint:allow numbers decoded JSON numbers are finite, so only fractions and values past 2^63 differ from JS; callers compare the value with the piece and file limits
	return int64(number)
}

// writeOf is the write the fields of object name.
func writeOf(object *delta.JsonObject) FileWrite {
	write := FileWrite{
		Space: optString(object, "space"), Path: optString(object, "path"), Mime: optString(object, "mime"), Source: optString(object, "source"),
		Ref: optString(object, "ref"), Key: optString(object, "key"), Created: optInt(object, "created"),
	}
	// `meta: null` is no metadata (the index stores none).
	write.Meta = object.Value("meta")
	return write
}

func pageJSON[T any](items []T, next *string, json func(T) *delta.JsonObject) *delta.JsonObject {
	list := make([]any, len(items))
	for index, item := range items {
		list[index] = json(item)
	}
	page := delta.JsonObjectOf("items", list)
	if next != nil {
		page.Set("next", *next)
	}
	return page
}

func fileJSON(file FileInfo) *delta.JsonObject { return file.JSON() }

func maybeFileJSON(file *FileInfo) delta.JsonValue {
	if file == nil {
		return nil
	}
	return file.JSON()
}

// FilesFacet is the kit's facet serving `Files` and `FilesFolder` with the host's files directory dir (`<host dir>/files`),
// caps and at most uploads uploads under way (64 for 0). Blobs live in `dir/blobs/`, uploads under way in
// `dir/.uploads/<run>/` (`Uploads`), ending with the object (an unfinished one is dropped when the next one goes live). For
// apps that host their own hosts; `startServer` and `startWorker` load it on theirs.
//
// pig divergence (D430): the facet starts the appends of a chunked upload with Reference.Start and awaits them apart.
func FilesFacet(dir string, caps FilesCaps, uploads int) host.Facet {
	maxFile := caps.MaxFile
	if maxFile == 0 {
		maxFile = 256 * 1024 * 1024
	}
	maxTotal := caps.MaxTotal
	if maxTotal == 0 {
		maxTotal = 4 * 1024 * 1024 * 1024
	}
	if uploads == 0 {
		uploads = 64
	}
	uploadsDir := filepath.Join(dir, ".uploads")
	blobs := NewBlobs(filepath.Join(dir, "blobs"))
	views := NewFolderViews()
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		if err := h.Serve(Files, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			return filesObject(self.(host.StoreSelf), uploadsDir, blobs, views, maxFile, maxTotal, uploads)
		}, host.ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(FilesFolder, func(params *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
			space, folder := optString(params, "space"), optString(params, "folder")
			if err := CheckSpace(space); err != nil {
				return contracts.Implementation{}, err
			}
			if err := CheckPath(folder, PathFolder); err != nil {
				return contracts.Implementation{}, err
			}
			// Its listing is read through the host's `Files`, which must be live first.
			self.Get(Files, delta.NewJsonObject(0))
			listing := self.Follow(func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
				attached, err := views.Attach(space, folder, observer, ctx)
				if err != nil {
					return nil, err
				}
				return &host.FollowAttachment[delta.JsonValue]{Value: attached.Value.JSON(), Detach: attached.Detach}, nil
			})
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"listing": listing},
			}}, nil
		}, host.ServeOptions{})
	})
}

// tooLarge is the refusal of a write a cap does not allow.
func tooLarge(format string, args ...any) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeTooLarge, Message: "files: " + fmt.Sprintf(format, args...)}, nil)
}

// checkWrite refuses a write's space or path outside the rules (a trust boundary: the caller's).
func checkWrite(write FileWrite) error {
	if err := CheckSpace(write.Space); err != nil {
		return err
	}
	return CheckPath(write.Path, PathFile)
}

// filesObject is the factory of the host's `Files`: one index, one run of uploads, one turn.
func filesObject(
	self host.StoreSelf, uploadsDir string, blobs *Blobs, views *FolderViews, maxFile, maxTotal int64, maxUploads int,
) (contracts.Implementation, error) {
	index := NewFilesIndex(self)
	// This run's uploads in their own directory: an upload left by an earlier run (its process ended) can never finish and
	// is dropped, while this run's may already have begun.
	run := filepath.Join(uploadsDir, NewToken(12))
	underway := NewUploads[FileWrite](run, maxUploads)
	views.Bind(func(space, folder string, ctx context.Context) (FolderListing, error) {
		page, err := index.List(space, folder, folderView, nil, ctx)
		if err != nil {
			return FolderListing{}, err
		}
		return FolderListing{Entries: page.Items, More: page.Next != nil}, nil
	})
	// Changes take turns: a version number, a key and a blob's count are decided by the one change running.
	var turnLock sync.Mutex
	turn := func(ctx context.Context, work func() (delta.JsonValue, error)) (delta.JsonValue, error) {
		chordctx.Suspend(ctx)
		turnLock.Lock()
		defer turnLock.Unlock()
		return work()
	}
	// usage is the bytes on disk: read on the line (a transaction), or, in a handler's synchronous part, from the document.
	usageBytes := func(ctx context.Context) (int64, error) {
		value, err := self.Tx(func(tx documents.Tx) (any, error) {
			draft, err := tx.Draft(index.Usage)
			if err != nil {
				return nil, err
			}
			return draft.Get("bytes"), nil
		}, ctx)
		if err != nil {
			return 0, err
		}
		//portlint:allow numbers the usage document holds a byte count this package wrote as an integer
		return int64(value.(float64)), nil
	}
	// Uploads under way count against the total too: two of them cannot pass it together.
	room := func(used, adding, fileSize, underwaySize int64) error {
		if fileSize > maxFile {
			return tooLarge("a file of %d bytes is larger than this host's cap (%d)", fileSize, maxFile)
		}
		if used+underwaySize+adding > maxTotal {
			return tooLarge("this host's files would exceed their cap (%d bytes)", maxTotal)
		}
		return nil
	}
	// A new blob's version; the blob goes again when its index commit fails.
	store := func(write FileWrite, blob string, size int64, ctx context.Context) (FileInfo, error) {
		file, err := index.Add(NewToken(12), write, BlobUse{ID: blob, Size: size, Fresh: true}, ctx)
		if err == nil {
			err = views.Changed(write.Space, ctx)
		}
		if err != nil {
			_ = blobs.Remove([]string{blob})
			return FileInfo{}, err
		}
		return file, nil
	}
	known := func(write FileWrite, ctx context.Context) (*FileInfo, error) {
		if write.Key == "" {
			return nil, nil
		}
		return index.ByKey(write.Space, write.Key, ctx)
	}
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		return host.AwaitingCtx(ctx, func() error {
			err := sweep(uploadsDir, run, underway, func() error {
				chordctx.Suspend(ctx)
				turnLock.Lock()
				defer turnLock.Unlock()
				return blobs.Sweep(func(id string) (bool, error) { return index.HasBlob(id, ctx) })
			}, ctx)
			if ctx.Err() != nil {
				// The object ended: the abort rejects the routine's sleep, quietly.
				return nil
			}
			return err
		})
	})
	summary := host.ExposeKeyed(index.Summary)
	return contracts.Implementation{Members: contracts.VariantImplementation{
		Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"summary": summary},
		Methods: map[string]contracts.MethodFunction{
			"put": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				write, bytes := writeOf(object), object.Value("bytes").([]byte)
				if err := checkWrite(write); err != nil {
					return nil, err
				}
				if len(bytes) > maxPiece {
					return nil, tooLarge("put takes at most %d bytes: use begin, append, finish", maxPiece)
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					existing, err := known(write, ctx)
					if err != nil || existing != nil {
						return maybeFileJSON(existing), err
					}
					used, err := usageBytes(ctx)
					if err != nil {
						return nil, err
					}
					if err := room(used, int64(len(bytes)), int64(len(bytes)), underway.Size()); err != nil {
						return nil, err
					}
					blob := NewToken(12)
					staging := filepath.Join(run, "put-"+blob)
					if err := blobs.Write(blob, bytes, staging); err != nil {
						_ = os.Remove(staging)
						return nil, err
					}
					file, err := store(write, blob, int64(len(bytes)), ctx)
					if err != nil {
						return nil, err
					}
					return file.JSON(), nil
				})
			},
			"begin": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				write := writeOf(objectOf(args))
				if err := checkWrite(write); err != nil {
					return nil, err
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					existing, err := known(write, ctx)
					if err != nil {
						return nil, err
					}
					if existing != nil {
						return delta.JsonObjectOf("kind", "exists", "file", existing.JSON()), nil
					}
					if resumed, ok := underway.Find(func(each FileWrite) bool { return each.Space == write.Space && each.Key != "" && each.Key == write.Key }); ok {
						return delta.JsonObjectOf("kind", "upload", "upload", resumed), nil
					}
					kind, err := index.Exists(write.Space, write.Path, ctx)
					if err != nil {
						return nil, err
					}
					if kind == "folder" {
						return nil, data.Invalid(data.ReasonArgs, fmt.Sprintf("files: %s:%s is a folder", write.Space, write.Path), delta.JsonObjectOf("conflict", write.Path))
					}
					id, err := underway.Begin(write)
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("kind", "upload", "upload", id), nil
				})
			},
			"append": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				bytes := object.Value("bytes").([]byte)
				if len(bytes) > maxPiece {
					return nil, tooLarge("append takes at most %d bytes", maxPiece)
				}
				// Accepted in this section, in the order the calls came: the usage is the document's value, read here.
				used := int64(0)
				if value, err := index.Usage.Value(); err == nil {
					//portlint:allow numbers the usage document holds a byte count this package wrote as an integer
					used = int64(value.Value("bytes").(float64))
				}
				accepted, err := underway.accept(optString(object, "upload"), optInt(object, "offset"), bytes, func(adding, fileSize, underwaySize int64) error {
					return room(used, adding, fileSize, underwaySize)
				})
				if err != nil {
					return nil, err
				}
				chordctx.Suspend(ctx)
				return nil, accepted.Run()
			},
			"uploaded": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				size, err := underway.Uploaded(args.(string))
				return float64(size), err
			},
			"finish": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return turn(ctx, func() (delta.JsonValue, error) {
					blob := NewToken(12)
					if err := blobs.Prepare(); err != nil {
						return nil, err
					}
					finished, err := underway.Finish(args.(string), blobs.Path(blob))
					if err != nil {
						return nil, err
					}
					existing, err := known(finished.Data, ctx)
					if err != nil {
						return nil, err
					}
					if existing != nil {
						_ = blobs.Remove([]string{blob})
						return existing.JSON(), nil
					}
					file, err := store(finished.Data, blob, finished.Size, ctx)
					if err != nil {
						return nil, err
					}
					return file.JSON(), nil
				})
			},
			"cancel": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				return nil, underway.Cancel(args.(string))
			},
			"read": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				id, offset, length := optString(object, "id"), optInt(object, "offset"), optInt(object, "length")
				if length > maxPiece {
					return nil, tooLarge("read takes at most %d bytes", maxPiece)
				}
				blob, size, found, err := index.BlobOf(id, ctx)
				if err != nil {
					return nil, err
				}
				if !found {
					return nil, data.Invalid(data.ReasonArgs, "files: no file "+id)
				}
				return blobs.Read(blob, size, offset, length)
			},
			"file": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				file, err := index.File(args.(string), ctx)
				return maybeFileJSON(file), err
			},
			"get": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, path := optString(object, "space"), optString(object, "path")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(path, PathFile); err != nil {
					return nil, err
				}
				file, err := index.Get(space, path, optInt(object, "version"), ctx)
				return maybeFileJSON(file), err
			},
			"byKey": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space := optString(object, "space")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				file, err := index.ByKey(space, optString(object, "key"), ctx)
				return maybeFileJSON(file), err
			},
			"byRef": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space := optString(object, "space")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				page, err := index.ByRef(space, optString(object, "ref"), limitOf(object), afterOf(object), ctx)
				if err != nil {
					return nil, err
				}
				return pageJSON(page.Items, page.Next, fileJSON), nil
			},
			"versions": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, path := optString(object, "space"), optString(object, "path")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(path, PathFile); err != nil {
					return nil, err
				}
				page, err := index.Versions(space, path, limitOf(object), afterOf(object), ctx)
				if err != nil {
					return nil, err
				}
				return pageJSON(page.Items, page.Next, fileJSON), nil
			},
			"list": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, folder := optString(object, "space"), optString(object, "folder")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(folder, PathFolder); err != nil {
					return nil, err
				}
				page, err := index.List(space, folder, limitOf(object), afterOf(object), ctx)
				if err != nil {
					return nil, err
				}
				return pageJSON(page.Items, page.Next, FolderEntry.JSON), nil
			},
			"mkdir": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, path := optString(object, "space"), optString(object, "path")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(path, PathFile); err != nil {
					return nil, err
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					folder, err := index.Mkdir(space, path, ctx)
					if err != nil {
						return nil, err
					}
					if err := views.Changed(space, ctx); err != nil {
						return nil, err
					}
					return folder.JSON(), nil
				})
			},
			"move": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, from, to := optString(object, "space"), optString(object, "from"), optString(object, "to")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				for _, path := range []string{from, to} {
					if err := CheckPath(path, PathFile); err != nil {
						return nil, err
					}
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					if err := index.Move(space, from, to, ctx); err != nil {
						return nil, err
					}
					return nil, views.Changed(space, ctx)
				})
			},
			"copy": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				id, space, path := optString(object, "id"), optString(object, "space"), optString(object, "path")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(path, PathFile); err != nil {
					return nil, err
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					file, err := index.File(id, ctx)
					if err != nil {
						return nil, err
					}
					blob, size, found, err := index.BlobOf(id, ctx)
					if err != nil {
						return nil, err
					}
					if file == nil || !found {
						return nil, data.Invalid(data.ReasonArgs, "files: no file "+id)
					}
					write := FileWrite{Space: space, Path: path, Mime: file.Mime, Source: file.Source, Ref: file.Ref, Meta: file.Meta}
					copied, err := index.Add(NewToken(12), write, BlobUse{ID: blob, Size: size, Fresh: false}, ctx)
					if err != nil {
						return nil, err
					}
					if err := views.Changed(space, ctx); err != nil {
						return nil, err
					}
					return copied.JSON(), nil
				})
			},
			"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return turn(ctx, func() (delta.JsonValue, error) {
					removed, err := index.Remove(args.(string), ctx)
					if err != nil || removed == nil {
						return nil, err
					}
					if err := blobs.Remove(removed.Gone); err != nil {
						return nil, err
					}
					return nil, views.Changed(removed.Space, ctx)
				})
			},
			"removePath": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object := objectOf(args)
				space, path := optString(object, "space"), optString(object, "path")
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				if err := CheckPath(path, PathFile); err != nil {
					return nil, err
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					gone, err := index.RemovePath(space, path, ctx)
					if err != nil {
						return nil, err
					}
					if err := blobs.Remove(gone); err != nil {
						return nil, err
					}
					return nil, views.Changed(space, ctx)
				})
			},
			"removeSpace": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				space := args.(string)
				if err := CheckSpace(space); err != nil {
					return nil, err
				}
				return turn(ctx, func() (delta.JsonValue, error) {
					gone, err := index.RemoveSpace(space, ctx)
					if err != nil {
						return nil, err
					}
					if err := blobs.Remove(gone); err != nil {
						return nil, err
					}
					return nil, views.Changed(space, ctx)
				})
			},
		},
	}}, nil
}

func limitOf(object *delta.JsonObject) int64 {
	if limit := optInt(object, "limit"); limit != 0 {
		return limit
	}
	return pageSize
}

func afterOf(object *delta.JsonObject) *string {
	if after, ok := object.Value("after").(string); ok {
		return &after
	}
	return nil
}

// sweep is the files object's routine once live: drops the uploads earlier runs left (every directory under uploadsDir but
// run) and the blobs no commit took (sweepBlobs), then every minute the uploads unwritten for an hour; when the object ends,
// every upload still under way.
func sweep(uploadsDir, run string, uploads *Uploads[FileWrite], sweepBlobs func() error, ctx host.Ctx) error {
	if entries, err := os.ReadDir(uploadsDir); err == nil {
		for _, entry := range entries {
			if filepath.Join(uploadsDir, entry.Name()) != run {
				if err := os.RemoveAll(filepath.Join(uploadsDir, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	defer func() {
		if ctx.Err() != nil {
			_ = uploads.Close()
		}
	}()
	if err := sweepBlobs(); err != nil {
		return err
	}
	for {
		if err := host.Sleep(sweepEvery, ctx); err != nil {
			return err
		}
		if err := uploads.Expire(uploadIdle); err != nil {
			return err
		}
	}
}
