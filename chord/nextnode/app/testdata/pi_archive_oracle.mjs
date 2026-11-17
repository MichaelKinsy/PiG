// Runs Pi's own compiled chord package (the pin's build, `<interop>/node_modules/@earendil-works/chord/dist`) over a host
// directory and an address archive, for the D462 tests of chord/nextnode/app: Pi's `packData`, `unpackStaging`, `filesFacet`
// and store export, nothing built by hand.
//
// usage: node pi_archive_oracle.mjs pack   <interop dir> <work dir> <manifest json>
//        node pi_archive_oracle.mjs unpack <interop dir> <archive> <parent dir> <manifest json>
//        node pi_archive_oracle.mjs hostdata <interop dir> <archive> <parent dir> <manifest json> <project id>
//        node pi_archive_oracle.mjs repack <interop dir> <archive> <parent dir> <manifest json> <out archive>
//
// pack: makes `<work>/host` (a Pi host over `store.sqlite` and `files/`: the Files facet with versions, a copy, folders and
// uploads, plus the plugin records a notes object keeps: a persisted document and a table), packs it with Pi's `packData` to
// `<work>/pi.tar` (its server part an empty host data file) and prints what Pi's own export of the store held:
// {tables: {name: {puts, version, fingerprint}}, documents: {creates, revisions}, writes, files: {...}}.
// unpack: Pi's `unpackStaging` (accepting the manifest's app and address kind) of the archive; prints the same shape read from
// the staging store, or {error}.
// hostdata: Pi's `unpackStaging` of the archive, then Pi's own `importHostData` of its `server.archive` into a Pi host (a store and the
// meta object's host-keyed document and table) under the project <id>; prints the same shape read from Pi's `exportHostData` of that
// address afterwards, or {error}.
// repack: Pi's `unpackStaging` of the archive, then Pi's `packData` of the staging directory (its own server part) to <out archive>;
// prints the staging store's shape, or {error}.
import { createReadStream } from "node:fs";
import { mkdir, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [mode, interopArg, ...args] = process.argv.slice(2);
const interop = interopArg.replace(/\/?$/, "/");
const pi = (path) => import(pathToFileURL(`${interop}node_modules/@earendil-works/chord/dist/${path}`).href);

const { BACKGROUND_CONTEXT: context } = await pi("context/index.js");
const { createHost, defineFacet } = await pi("host/index.js");
const { arr, hostKind, local, method, int, obj, str } = await pi("contracts/index.js");
const { defineState } = await pi("documents/index.js");
const { openNodeSqliteDatabase } = await pi("sqlite/node.js");
const { decodeJsonl, defineTable, host: hostColumn, openStore, sqliteBackend } = await pi("store/index.js");
const { Files, filesFacet } = await pi("node/app/files.js");
const { packData, unpackStaging } = await pi("node/app/archive.js");

const Notes = local("test.notes", { version: 1, params: {}, members: { text: str(), write: method(str()), visit: method(str(), int()) } });
const Text = defineState("test.text", { schema: obj({ text: str() }), version: 1, initial: () => ({ text: "" }), persist: true, checkpointWhen: () => false });
const Visits = defineTable("test.visits", { version: 1, columns: { page: str(), count: int() }, key: ["page"] });
const notesFacet = defineFacet((host) =>
	host.serve(Notes, (_params, self) => {
		const text = self.state(Text);
		const visits = self.table(Visits);
		return {
			text: self.derive([text], ([value]) => value.text),
			write: async (next, ctx) => {
				await text.change((draft) => {
					draft.text = next;
				}, ctx);
				return null;
			},
			visit: async (page, ctx) =>
				self.tx(async (tx) => {
					const count = ((await tx.get(visits, { page }))?.count ?? 0) + 1;
					tx.put(visits, { page, count });
					return count;
				}, ctx),
		};
	}),
);

// The meta object of the app kit's tests (chord/nextnode/app/app_export_host_test.go: metaPlugin): a document and a table keyed by a project.
const Project = hostKind("project", { id: str() });
const Meta = defineState("test.meta", { schema: obj({ title: str() }), version: 1, initial: () => ({ title: "" }), keyed: Project, persist: true, checkpointWhen: () => false });
const Labels = defineTable("test.labels", { version: 1, columns: { project: hostColumn(Project), label: str() }, key: ["project", "label"] });
const MetaStore = local("test.meta-store", {
	version: 1,
	params: {},
	members: { set: method(obj({ id: str(), title: str(), label: str() })), get: method(str(), obj({ title: str(), labels: arr(str()) })) },
});
const metaFacet = defineFacet((host) =>
	host.serve(MetaStore, (_params, self) => {
		self.state(Meta);
		self.table(Labels);
		return { set: async () => null, get: async () => ({ title: "", labels: [] }) };
	}),
);

/** What Pi's export of `store` holds: per table its puts, version and fingerprint; the documents' records and revisions. */
const described = (store) => describedParts(store.export(context));

/** The same, of any stream of archive parts (a store's export, a host's data export). */
async function describedParts(parts) {
	const tables = {};
	const documents = { creates: 0, revisions: 0 };
	let writes = 0;
	for await (const part of parts) {
		if (part.kind === "table") tables[part.table] = { puts: 0, version: part.version, fingerprint: part.fingerprint, key: part.key, columns: part.columns, indexes: {} };
		else if (part.kind === "put") tables[part.table].puts++;
		else if (part.kind === "index") tables[part.table].indexes[part.name] = part.columns;
		else if (part.kind === "document.create") documents.creates++;
		else if (part.kind === "document.revision") documents.revisions++;
		else if (part.kind === "end") writes = part.writes;
	}
	return { tables, documents, writes };
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

if (mode === "pack") {
	const [work, manifestText] = args;
	const manifest = JSON.parse(manifestText);
	const dir = join(work, "host");
	await mkdir(dir, { recursive: true });
	const store = await openStore(sqliteBackend(await openNodeSqliteDatabase(join(dir, "store.sqlite"))), context);
	const errors = [];
	const host = createHost({ store, onError: (error) => errors.push(String(error)) });
	await host.load(
		[
			{ name: "chord.app.files", facet: filesFacet(join(dir, "files")) },
			{ name: "test.notes", facet: notesFacet },
		],
		context,
	);
	const files = await host.get(Files, {}, context);
	const put = (space, path, text) => files.put({ space, path, mime: "text/plain", source: "test", bytes: encoder.encode(text) }, context);
	const v1 = await put("artifacts", "report", "one");
	const v2 = await put("artifacts", "report", "two");
	await put("uploads", "docs/a.txt", "upload");
	await files.copy({ id: v1.id, space: "shared", path: "copy-of-report" }, context);
	const notes = await host.get(Notes, {}, context);
	await notes.write("buy milk", context);
	await notes.visit("home", context);
	await notes.visit("home", context);
	await notes.visit("about", context);
	notes.release();
	const summary = await files.summary.get("artifacts", context);
	const out = {
		...(await described(store)),
		files: { report: [v2.id, v1.id], texts: { [v1.id]: "one", [v2.id]: "two" }, uploadsDocs: 1, summary: { files: summary.files, versions: summary.versions } },
	};
	files.release();
	await host.close(context);
	await store.close(context);
	const server = join(work, "server.jsonl");
	await writeFile(
		server,
		`${JSON.stringify({ kind: "manifest", format: 1, schema: 3, lastSeq: 0, nextId: 2, host: manifest.address.key })}\n${JSON.stringify({ kind: "end", writes: 0 })}\n`,
	);
	await packData(dir, manifest, server, join(work, "pi.tar"), context);
	if (errors.length > 0) throw new Error(`host errors: ${errors.join("; ")}`);
	process.stdout.write(JSON.stringify(out));
} else if (mode === "unpack" || mode === "hostdata" || mode === "repack") {
	const [archive, parent, manifestText, extra] = args;
	const want = JSON.parse(manifestText);
	const accept = (manifest) => {
		if (manifest.format !== 2) throw new Error(`importData: not a format 2 archive`);
		if (manifest.app !== want.app) throw new Error(`importData: an archive of ${JSON.stringify(manifest.app)}, not ${JSON.stringify(want.app)}`);
		if (manifest.address.kind !== want.address.kind) throw new Error(`importData: an archive of a ${manifest.address.kind}`);
	};
	try {
		const { staging } = await unpackStaging(archive, parent, accept, {}, context);
		let out;
		if (mode === "hostdata") {
			const store = await openStore(sqliteBackend(await openNodeSqliteDatabase(join(parent, "server.sqlite"))), context);
			const errors = [];
			const host = createHost({ store, onError: (error) => errors.push(String(error)) });
			await host.load([{ name: "test.meta-store", facet: metaFacet }], context);
			const address = Project({ id: extra });
			await host.importHostData(decodeJsonl(createReadStream(join(staging, "server.archive"))), context, { address, existing: "refuse" });
			out = await describedParts(host.exportHostData(address, context));
			await host.close(context);
			await store.close(context);
			if (errors.length > 0) throw new Error(`host errors: ${errors.join("; ")}`);
		} else {
			const store = await openStore(sqliteBackend(await openNodeSqliteDatabase(join(staging, "store.sqlite"))), context);
			out = await described(store);
			await store.close(context);
			out.stagingEntries = (await readdir(staging)).sort();
			if (mode === "repack") await packData(staging, want, join(staging, "server.archive"), extra, context);
		}
		process.stdout.write(JSON.stringify(out));
	} catch (error) {
		process.stdout.write(JSON.stringify({ error: error instanceof Error ? error.message : String(error) }));
	}
} else {
	throw new Error(`unknown mode ${mode}`);
}
