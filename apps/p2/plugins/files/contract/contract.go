// Package contract ports apps/p2/plugins/files/contract.ts: a session's uploads and download snapshots (a leaf package
// the chat worker imports for `Attachments`).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var (
	c    = p2runtime.Contract
	str  = schema.Str
	num  = schema.Num
	nul  = schema.Nul
	obj  = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop = schema.Prop
	opt  = schema.Opt
	arr  = func(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
		return schema.Arr(item, options...)
	}
	union  = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

// AttachmentSchema and MaxAttachments are the runtime's (upstream re-exports them from the API).
var AttachmentSchema = p2runtime.AttachmentSchema

// MaxAttachments is the most attachments per message.
const MaxAttachments = p2runtime.MaxAttachments

// Attachments: upload IDs to the session's uploads, materialized for a conversation (worker, host-internal). 3: uploads
// live in the session's `Files`; materialized on every machine, this one included. `resolve` writes the files for a
// message to `conversation` to `~/.pi/p2/uploads/<session>/` of the machine where it works (this machine: the data
// directory's `uploads/<session>/`) before the message is admitted; it throws "Unknown attachment" for an ID that is
// not an upload of this session.
var Attachments = must(contracts.Local("p2.attachments", contracts.LocalDeclaration{
	Version: 3,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("resolve", method(
			obj(schema.Properties{prop("ids", arr(str(), schema.MaxItems(MaxAttachments))), prop("conversation", num())}),
			arr(AttachmentSchema),
		)),
	},
}))

// DownloadInfoSchema is a download snapshot's ID, name and size.
var DownloadInfoSchema = obj(schema.Properties{prop("id", str()), prop("name", str()), prop("size", num())})

// DownloadInfo is a download snapshot (DownloadInfoSchema).
type DownloadInfo struct {
	ID   string
	Name string
	Size float64
}

// UploadedSchema is what `POST /upload` answers (and `SessionFiles.finish`): the file's ID (a UUID), its name, size and
// image type.
var UploadedSchema = obj(schema.Properties{prop("id", str()), prop("name", str()), prop("size", num()), opt("mimeType", str())})

// Uploaded is an uploaded file (UploadedSchema).
type Uploaded struct {
	ID       string
	Name     string
	Size     float64
	MimeType *string
}

// DownloadStore: immutable private snapshots of files for the browser to download (server, host-internal): a producer
// on the server saves a copy of a server file into the session's files (space `downloads`; producers on the worker,
// send_file, write there directly). Served by `GET /download?session=<id>&id=<snapshot>` to the owner and the session's
// guests; later changes of the source never show. 2: the snapshot lives in the session's files.
var DownloadStore = must(contracts.Local("p2.downloads", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("save", method(obj(schema.Properties{prop("session", str()), prop("path", str()), opt("name", str())}), DownloadInfoSchema)),
	},
}))

// SessionFiles is a session's uploads and download snapshots for the server's routes and producers (the session's
// worker, the server only): a file streams into the session's `Files` in chunks (`POST /upload` into `uploads`,
// `DownloadStore` into `downloads`), a snapshot is found by its ID and read by range (`GET /download`). The server
// checks who may upload or view; the worker owns the bytes.
var SessionFiles = must(contracts.Remote("p2.session-files", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			// Starts a file `name` of `space` written by `source` (`owner`, `guest:<id>`, `server`): its ID and upload.
			contracts.Meth("begin", method(
				obj(schema.Properties{
					prop("space", union(schema.Lit("uploads"), schema.Lit("downloads"))),
					prop("name", str()), prop("source", str()),
				}),
				obj(schema.Properties{prop("id", str()), prop("upload", str())}),
			)),
			// Appends a chunk (at most 1 MiB) at `offset`, the bytes stored so far.
			contracts.Meth("append", method(obj(schema.Properties{
				prop("upload", str()), prop("offset", schema.Int(schema.Minimum(0))), prop("bytes", schema.Bytes()),
			}))),
			// Ends the file: its ID, name, size and image type.
			contracts.Meth("finish", method(str(), UploadedSchema)),
			// Drops the file under way (its client went away or sent too much).
			contracts.Meth("cancel", method(str())),
			// The download snapshot `id` (a version of the session's files and its name), or null.
			contracts.Meth("download", method(
				str(schema.MaxLength(64)),
				union(obj(schema.Properties{prop("file", str()), prop("name", str()), prop("size", num())}), nul()),
			)),
			// `length` bytes (at most 1 MiB) of the session's download snapshot version `file` from `offset`.
			contracts.Meth("read", method(
				obj(schema.Properties{
					prop("file", str()), prop("offset", schema.Int(schema.Minimum(0))),
					prop("length", schema.Int(schema.Minimum(0), schema.Maximum(1024*1024))),
				}),
				schema.Bytes(),
			)),
		}}),
	},
}))

// SpaceSummarySchema is a space summary of a session's files as the server mirrors it (the kit's `SpaceSummary`).
var SpaceSummarySchema = obj(schema.Properties{
	prop("files", num()), prop("versions", num()), prop("bytes", num()),
	prop("newest", arr(obj(schema.Properties{
		prop("id", str()), prop("space", str()), prop("path", str()), prop("version", num()), prop("size", num()),
		prop("mime", str()), prop("source", str()), opt("ref", str()), opt("key", str()), prop("created", num()),
		opt("meta", schema.Unknown()),
	}))),
})

// FileSummaries is the server's mirror of every session's file summaries, by session and space (server): a session's
// worker pushes a space's summary inside the method that changed it (`push`, its own session only), so a list of a
// session's files shows without starting its worker (the owner's `of`).
var FileSummaries = must(contracts.Remote("p2.file-summaries", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("push", method(obj(schema.Properties{prop("space", str()), prop("summary", SpaceSummarySchema)}))),
		}}),
		contracts.Var("owner", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("of", method(obj(schema.Properties{prop("session", str())}), schema.Map(SpaceSummarySchema))),
		}}),
	},
}))

// FilesMirror pushes a space's summary of this session's files to the server's mirror (worker, host-internal).
var FilesMirror = must(contracts.Local("p2.files-mirror", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("push", method(str()))},
}))
