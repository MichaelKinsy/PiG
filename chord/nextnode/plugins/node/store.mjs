// The Node host's store (RuntimeLoad.Store; docs/plan/chord-plugins-node-evaluator.md section 10.5): opened as the apps of Pi's
// examples open theirs, `openStore(sqliteBackend(await openNodeSqliteDatabase(path)))`, over Pi's own `next/store` and
// `sqlite/node.ts` (`node:sqlite`), and given to `createHost({ store })`. The host owns nothing of it: the store is closed
// after the host, as Pi's apps do.
const tree = new URL("./pi/chord/src/", import.meta.url);

/** Opens the SQLite store at `path`; a failure closes the database (Pi's `openStore`) and rejects. */
export async function openHostStore(path, context) {
	const [{ openStore, sqliteBackend }, { openNodeSqliteDatabase }] = await Promise.all([
		import(new URL("store/index.ts", tree).href),
		import(new URL("sqlite/node.ts", tree).href),
	]);
	return openStore(sqliteBackend(await openNodeSqliteDatabase(path)), context);
}
