// Run Pi's Models auth operations and InMemoryModelsStore (packages/ai/src/models.ts, models-store.ts, imported as-is with types stripped)
// with a caller signal in each state. Case: {op, providerId, signal, blocked}.
//   op: checkAuth | getAvailable | getAvailableOfChat | getAllAvailable | logout | storeRead | storeWrite | storeDelete
//   signal: none | aborted (already aborted, default reason) | abortedWithReason (already aborted with Error("why")) | abortsWhileBlocked / abortsWhileBlockedWithReason (abort, with the default reason or Error("why"), while the provider's auth check is blocked)
//   blocked: the provider's auth check waits for the abort (only with abortsWhileBlocked)
// Answers {outcome: "value", value, credentialAfter, storeAfter} or {outcome: "rejected", name, message, credentialAfter, storeAfter}.
// usage: node operation_options_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-ai deps)
import module from "node:module";
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		if (!specifier.startsWith(".") && !specifier.startsWith("node:") && !specifier.startsWith("file:") && !specifier.startsWith("/"))
			return next(specifier, { ...context, parentURL: pathToFileURL(process.env.PI_INTEROP_DIR).href });
		return next(specifier, context);
	},
});
const load = (path) => import(pathToFileURL(`${packages}ai/src/${path}`).href);
const { createModels } = await load("models.ts");
const { InMemoryCredentialStore } = await load("auth/credential-store.ts");
const { InMemoryModelsStore } = await load("models-store.ts");
const model = (id) => ({ id, name: id, api: "test-api", provider: "p1", baseUrl: "https://example.test/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 10000, maxTokens: 1000 });
const deferred = () => { let resolve; const promise = new Promise((r) => (resolve = r)); return { promise, resolve }; };
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const c of JSON.parse(input)) {
	const gate = deferred();
	const started = deferred();
	const credentialStore = new InMemoryCredentialStore();
	const modelsStore = new InMemoryModelsStore();
	await credentialStore.modify("p1", async () => ({ type: "api_key", key: "stored" }));
	await modelsStore.write("p1", { models: [] });
	const runtime = createModels({ credentials: credentialStore, modelsStore });
	const never = () => { throw new Error("not used"); };
	runtime.setProvider({
		id: "p1", name: "p1", getModels: () => [model("a"), model("b")], stream: never, streamSimple: never,
		auth: { apiKey: { name: "Test", check: async () => { started.resolve(); if (c.blocked) await gate.promise; return { type: "api_key", source: "test" }; }, resolve: async () => ({ auth: { apiKey: "k" } }) } },
	});
	const controller = new AbortController();
	if (c.signal === "aborted") controller.abort();
	if (c.signal === "abortedWithReason") controller.abort(new Error("why"));
	const options = c.signal === "none" ? undefined : { signal: controller.signal };
	const providerId = c.providerId === "" ? undefined : c.providerId;
	let promise;
	switch (c.op) {
		case "checkAuth": promise = runtime.checkAuth(c.providerId, options); break;
		case "getAvailable": promise = runtime.getAvailable(providerId, options).then((ms) => ms.map((m) => m.id)); break;
		case "getAvailableOfChat": promise = runtime.getAvailableOfType("chat", providerId, options).then((ms) => ms.map((m) => m.id)); break;
		case "getAllAvailable": promise = runtime.getAllAvailable(providerId, options).then((ms) => ms.map((m) => m.id)); break;
		case "logout": promise = runtime.logout("p1", options); break;
		case "storeRead": promise = modelsStore.read("p1", options); break;
		case "storeWrite": promise = modelsStore.write("p1", { models: [], etag: "new" }, options); break;
		case "storeDelete": promise = modelsStore.delete("p1", options); break;
	}
	if (c.signal === "abortsWhileBlocked" || c.signal === "abortsWhileBlockedWithReason") {
		await started.promise;
		if (c.signal === "abortsWhileBlocked") controller.abort();
		else controller.abort(new Error("why"));
	}
	let result;
	try {
		const value = await promise;
		result = { outcome: "value", value: value === undefined ? null : value };
	} catch (error) {
		result = { outcome: "rejected", name: error.name, message: error.message };
	}
	gate.resolve();
	result.credentialAfter = (await credentialStore.read("p1")) !== undefined;
	result.storeAfter = (await modelsStore.read("p1")) ?? null;
	out.push(result);
}
process.stdout.write(JSON.stringify(out));
