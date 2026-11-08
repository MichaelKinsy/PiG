// The app kit tests' API v1 (Pi's test/node/app/support.ts `API_V1`): Chord's surface and the kit's local objects plugins use, as the
// app's API module re-exports them. The Go kit serves the same contracts (info.go): plugins get them through the bridge.
import { local, obj, str, unknown } from "@earendil-works/chord/next";

export * from "@earendil-works/chord/next";

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
