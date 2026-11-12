// The app kit tests' API v1 (Pi's test/node/app/support.ts `API_V1`): Chord's surface and the kit's local objects plugins use, as the
// app's API module re-exports them. The Go kit serves the same contracts (info.go): plugins get them through the bridge.
import { arr, bool, bytes, int, keyed, lit, local, method, nul, obj, opt, str, union, unknown } from "@earendil-works/chord";

export * from "@earendil-works/chord";

export const AppInfo = local("chord.app.info", {
	version: 2,
	params: {},
	members: { dataDir: str(), name: str(), version: str(), owner: unknown() },
});

export const WorkerInfo = local("chord.app.worker-info", {
	version: 1,
	params: {},
	members: { address: obj({ kind: str(), key: str(), params: unknown() }), start: unknown() },
});

// Pi's test API re-exports the kit's `Files` and `FilesFolder` (node/app/files.ts, files-index.ts), declared here again: contracts are data, and
// the bridge refuses a proxy whose contract does not hash to the Go kit's.
const MetaSchema = unknown();
const FileSchema = obj({
	id: str(),
	space: str(),
	path: str(),
	version: int(),
	size: int(),
	mime: str(),
	source: str(),
	ref: opt(str()),
	key: opt(str()),
	created: int(),
	meta: opt(MetaSchema),
});
const FolderSchema = obj({ space: str(), path: str(), created: int() });
const SummarySchema = obj({ files: int(), versions: int(), bytes: int(), newest: arr(FileSchema) });
const WRITE = {
	space: str(),
	path: str(),
	mime: str(),
	source: str(),
	ref: opt(str({ min: 1 })),
	key: opt(str({ min: 1 })),
	meta: opt(MetaSchema),
	created: opt(int({ min: 0 })),
};
const PAGE = { after: opt(str()), limit: opt(int({ min: 1, max: 1000 })) };
const pageOf = (item) => obj({ items: arr(item), next: opt(str()) });
const MAYBE_FILE = union([FileSchema, nul()]);
const EntrySchema = union([obj({ kind: lit("folder"), folder: FolderSchema }), obj({ kind: lit("file"), file: FileSchema })]);

export const Files = local("chord.app.files", {
	version: 2,
	params: {},
	members: {
		summary: keyed(SummarySchema),
		put: method(obj({ ...WRITE, bytes: bytes() }), FileSchema),
		begin: method(obj(WRITE), union([obj({ kind: lit("upload"), upload: str() }), obj({ kind: lit("exists"), file: FileSchema })])),
		append: method(obj({ upload: str(), offset: int({ min: 0 }), bytes: bytes() })),
		uploaded: method(str(), int()),
		finish: method(str(), FileSchema),
		cancel: method(str()),
		read: method(obj({ id: str(), offset: int({ min: 0 }), length: int({ min: 0 }) }), bytes()),
		file: method(str(), MAYBE_FILE),
		get: method(obj({ space: str(), path: str(), version: opt(int({ min: 1 })) }), MAYBE_FILE),
		byKey: method(obj({ space: str(), key: str({ min: 1 }) }), MAYBE_FILE),
		byRef: method(obj({ space: str(), ref: str({ min: 1 }), ...PAGE }), pageOf(FileSchema)),
		versions: method(obj({ space: str(), path: str(), ...PAGE }), pageOf(FileSchema)),
		list: method(obj({ space: str(), folder: str(), ...PAGE }), pageOf(EntrySchema)),
		mkdir: method(obj({ space: str(), path: str() }), FolderSchema),
		move: method(obj({ space: str(), from: str(), to: str() })),
		copy: method(obj({ id: str(), space: str(), path: str() }), FileSchema),
		remove: method(str()),
		removePath: method(obj({ space: str(), path: str() })),
		removeSpace: method(str()),
	},
});

export const FilesFolder = local("chord.app.files.folder", {
	version: 1,
	params: { space: str(), folder: str() },
	members: { listing: obj({ entries: arr(EntrySchema), more: bool() }) },
});
