// Generates upstream-golden.json: Pi's packages/mcp pure functions (content, JSON-RPC classification, OAuth discovery,
// challenge parsing, metadata parsing, scope step-up) run on a deterministic corpus plus seeded-random inputs. Run from
// the repo root with Node >= 24 (native type stripping):
//   node mcp/testdata/upstream-oracle.mjs > mcp/testdata/upstream-golden.json
// The upstream mirror is .upstream/current (override with PI_UPSTREAM). Inputs travel as JSON text so the oracle and
// the Go test parse identical bytes.
import { writeSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const root = resolve(process.env.PI_UPSTREAM ?? ".upstream/current", "packages/mcp/src");
const load = (file) => import(pathToFileURL(resolve(root, file)).href);
const { toLlmContent } = await load("protocol/content.ts");
const { parseJsonRpcMessage, isJsonRpcResponse, isJsonRpcRequest, isJsonRpcNotification } = await load("protocol/jsonrpc.ts");
const discovery = await load("oauth/discovery.ts");
const types = await load("oauth/types.ts");
const { stepUpScope } = await load("oauth/flow.ts");

let seed = 0x2545f491;
function rnd(n) {
	seed ^= seed << 13;
	seed >>>= 0;
	seed ^= seed >>> 17;
	seed ^= seed << 5;
	seed >>>= 0;
	return seed % n;
}
const pick = (xs) => xs[rnd(xs.length)];
const call = (f) => {
	try {
		const v = f();
		return v === undefined ? { ok: null } : { ok: v };
	} catch (e) {
		return { error: e instanceof Error ? `${e.name}: ${e.message}` : String(e) };
	}
};

const cases = { content: [], jsonrpc: [], challenge: [], discoveryUrls: [], resource: [], stepUp: [], metadata: [], tokens: [], client: [], authServer: [] };

// toLlmContent
const STR = ["", "a", "é", "x\ny", "😀", "image/png", "image/", "IMAGE/png", "audio/wav", "text/plain", "application/octet-stream", "http://x/y"];
// Blocks carry the members their type requires, as the MCP schema does. A server that omits one makes Pi render
// "undefined" (JavaScript has no empty-string default); the accounting document records Pig's different choice.
function block() {
	const t = pick(["text", "image", "audio", "resource_link", "resource", "resource", "weird", "text", "image"]);
	const o = { type: t };
	if (t === "text") o.text = pick(STR);
	if (t === "image" || t === "audio") { o.data = pick(STR); o.mimeType = pick(STR); }
	if (t === "resource_link") { o.name = pick(STR); o.uri = pick(STR); }
	if (t === "resource") {
		const r = { uri: pick(STR) };
		if (rnd(2)) r.text = pick(STR);
		else r.blob = pick(STR);
		if (rnd(3)) r.mimeType = pick(STR);
		o.resource = r;
	}
	if (rnd(4) === 0) { o.annotations = { priority: 1 }; o._meta = { k: 1 }; }
	return o;
}
for (let i = 0; i < 150; i++) {
	const result = { content: Array.from({ length: rnd(4) }, block) };
	if (rnd(5) === 0) delete result.content;
	if (rnd(3) === 0) result.structuredContent = pick([{}, { a: 1 }, { a: [1, { b: null }], c: "é😀" }, { "10": 1, b: 2, "2": 3 }, { n: 1e21, m: -0, k: 1.5e-7 }, { s: "\u2028\ud800" }, { nested: { deep: [[], {}] } }]);
	const text = JSON.stringify(result);
	cases.content.push({ result: text, ...call(() => toLlmContent(JSON.parse(text))) });
}

// toLlmContent on data a server sent without the member types the schema names. Pi reads such data without checking it:
// a member of another type converts as JavaScript converts it, and a null block or a resource without an object throws.
{
	const VALUES = [undefined, "s", 5, true, null, [1, "a"], { a: 1 }, "image/png", "", 0, false, [null, [2]], 1e21];
	const add = (result) => {
		const text = JSON.stringify(result);
		cases.content.push({ result: text, ...call(() => toLlmContent(JSON.parse(text))) });
	};
	for (const type of ["text", "image", "audio", "resource_link", "weird", 5, null, undefined]) {
		for (const key of ["text", "data", "mimeType", "name", "uri"]) {
			for (const value of VALUES) {
				const block = {};
				if (type !== undefined) block.type = type;
				if (value !== undefined) block[key] = value;
				add({ content: [block] });
			}
		}
	}
	for (const block of [null, 5, "x", [], {}, true]) add({ content: [block] });
	for (const resource of [undefined, null, 5, "s", true, [], {}, { uri: "u" }, { text: 5 }, { text: null }, { blob: 5 }, { blob: "b" }, { blob: "b", uri: 5 }]) {
		for (const mimeType of [undefined, null, 5, {}, "", "image/png", "image/", "text/plain"]) {
			const r = resource !== null && typeof resource === "object" && !Array.isArray(resource) && mimeType !== undefined ? { ...resource, mimeType } : resource;
			add({ content: [{ type: "resource", ...(r === undefined ? {} : { resource: r }) }] });
		}
	}
	for (const content of [5, null, {}, "s", true]) add({ content });
	for (const structuredContent of [{ a: 1 }, 5, null, "x", [1, [2]], true]) {
		add({ structuredContent });
		add({ content: [], structuredContent });
	}
	add({});
}

// parseJsonRpcMessage
const IDS = ["1", "0", '"a"', '""', "1.5", "-1", "1e400", "null", "true", "[]", "{}", '"1"'];
const METHODS = ['"m"', '""', "1", "null"];
const BASES = [['"jsonrpc":"2.0"', '"id":1', '"method":"m"'], ['"jsonrpc":"2.0"', '"method":"m"'], ['"jsonrpc":"2.0"', '"id":"x"', '"result":{}'], ['"jsonrpc":"2.0"', '"id":2', '"error":{"code":1,"message":"m"}']];
for (let i = 0; i < 250; i++) {
	let parts = [...pick(BASES)];
	for (let n = rnd(3); n > 0; n--) {
		const key = pick(["jsonrpc", "id", "method", "result", "error", "params"]);
		parts = parts.filter((part) => !part.startsWith(`"${key}":`));
		if (rnd(3) === 0) continue;
		const values = { jsonrpc: ['"2.0"', '"1.0"', "2"], id: IDS, method: METHODS, result: ["{}", "null", "1", '"x"'], error: ["{}", "null", '{"code":1,"message":"m"}', '{"code":"1","message":"m"}', '{"code":1}', "[]", '{"code":1.5,"message":""}'], params: ["{}", "[]", "null", "1"] };
		parts.push(`"${key}":${pick(values[key])}`);
	}
	const text = rnd(25) === 0 ? pick(["[]", "null", "1", '"x"', "{}"]) : `{${parts.join(",")}}`;
	cases.jsonrpc.push({ message: text, ...call(() => {
		const m = parseJsonRpcMessage(JSON.parse(text));
		// McpClient.handleMessage tests in this order.
		const kind = isJsonRpcResponse(m) ? "response" : isJsonRpcRequest(m) ? "request" : isJsonRpcNotification(m) ? "notification" : "none";
		return JSON.stringify({ kind, id: m.id ?? null, method: kind === "response" ? null : m.method });
	}) });
}

// Fixed JSON-RPC cases: error members whose code or message is null or out of the float64 range.
for (const text of [
	'{"jsonrpc":"2.0","id":1,"error":{"code":null,"message":"m"}}', '{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":null}}',
	'{"jsonrpc":"2.0","id":1,"error":{"code":"1","message":"m"}}', '{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":5}}',
	'{"jsonrpc":"2.0","id":1,"error":{"code":1e400,"message":"m"}}', '{"jsonrpc":"2.0","id":1,"error":{"code":-1e400,"message":"m"}}',
	'{"jsonrpc":"2.0","id":1,"error":{"code":true,"message":"m"}}', '{"jsonrpc":"2.0","id":1,"error":{"message":"m"}}',
]) {
	cases.jsonrpc.push({ message: text, ...call(() => {
		const m = parseJsonRpcMessage(JSON.parse(text));
		const kind = isJsonRpcResponse(m) ? "response" : isJsonRpcRequest(m) ? "request" : isJsonRpcNotification(m) ? "notification" : "none";
		return JSON.stringify({ kind, id: m.id ?? null, method: kind === "response" ? null : m.method });
	}) });
}

// parseWwwAuthenticate
const SCHEMES = ["Bearer", "bearer", "DPoP", "dpop", "Basic", "", "  Bearer", "\tBearer", "Bearer,", "Bearerx"];
const FIELDS = ["resource_metadata", "scope", "error", "error_description", "Scope", "ERROR", "xscope"];
const VALUES = ['"a b"', "a", '""', "x,y", '"x,y"', "https://e.com/.well-known/oauth-protected-resource", '"https://e.com/p?q=1#h"', "not a url", '"http://[::1"', "é", '"a\\"b"', "'q'", '"unterminated', 'a"b', '""x', "x=y"];
for (let i = 0; i < 300; i++) {
	let header = pick(SCHEMES);
	for (let n = rnd(5); n > 0; n--) header += pick([" ", ", ", ",", "\t", "  ", ""]) + pick(FIELDS) + pick(["=", "=", " = ", ":"]) + pick(VALUES);
	const out = call(() => {
		const c = discovery.parseWwwAuthenticate(rnd(40) === 0 ? null : header);
		return JSON.stringify({ resourceMetadataUrl: c.resourceMetadataUrl?.href ?? null, scope: c.scope ?? null, error: c.error ?? null, errorDescription: c.errorDescription ?? null });
	});
	cases.challenge.push({ header, ...out });
}
for (const header of ["", "Bearer", "Bearer scope=a", 'Bearer scope="a b", error=insufficient_scope', "Bearer realm=x,scope=a", "Bearer SCOPE=a",
	// JavaScript's \s includes Unicode spaces: they separate fields and end an unquoted value.
	"Bearer\u00a0scope=a", "Bearer realm=x\u2028scope=a", "Bearer scope=a\u3000b", "Bearer error=x\ufeffy", "Bearer\u00a0x scope=a\u00a0b"]) cases.challenge.push({ header, ...call(() => JSON.stringify(((c) => ({ resourceMetadataUrl: c.resourceMetadataUrl?.href ?? null, scope: c.scope ?? null, error: c.error ?? null, errorDescription: c.errorDescription ?? null }))(discovery.parseWwwAuthenticate(header)))) });

// URL cases. ftp:, ws:, wss: and blob: are left out: Pig keeps net/url for schemes other than http and https (see the
// accounting in docs/parity/gap-closure/gap-mcp-codemode-libs.md), and MCP OAuth endpoints must be http(s).
const URLS = [
	"https://example.com", "https://example.com/", "https://example.com/mcp", "https://example.com/mcp/", "https://example.com:443/mcp", "http://example.com:80/x", "https://example.com:8443/a/b?q=1#frag",
	"HTTPS://EXAMPLE.COM/Path", "https://user:pw@example.com/p", "https://example.com/a%20b/%7Ec", "https://example.com/a b", "https://example.com//double//slash", "https://example.com/a/../b/./c", "https://example.com/a\\b",
	"https://[::1]:8080/x", "http://127.0.0.1:3000/mcp", "https://bücher.example/p", "https://xn--bcher-kva.example/p", "https://example.com/é", "https://example.com/?", "https://example.com/#", "https://example.com/a?b#c",
	"file:///tmp/x", "custom://host/path", "mailto:a@b", "data:text/plain,hi", "javascript:alert(1)", "vbscript:x", "about:blank", "example.com", "/relative", "", " ", "https://", "https:///x", "http://exa mple.com",
	"https://example.com/.well-known/oauth-authorization-server", "https://example.com/tenant/v2.0", "https://example.com/tenant/v2.0/", "https://example.com/%2e%2e/x", "https://example.com/a;b=c", "https://example.com:0/x", "https://example.com:65536/x",
	"https://EXAMPLE.com./x", "https://a.b.c.d.e/", "  https://example.com/trim  ", "https://example.com\t/x", "https://example.com/x\n", "http://0x7f.1/x", "http://2130706433/",
];
const asHref = (u) => u.href;
for (const u of URLS) {
	cases.discoveryUrls.push({ url: u, ...call(() => JSON.stringify(discovery.buildAuthorizationServerDiscoveryUrls(u).map((e) => ({ url: e.url.href, type: e.type })))) });
	cases.resource.push({ serverUrl: u, metadata: null, ...call(() => JSON.stringify({ url: discovery.resourceUrlFromServerUrl(u).href, selected: discovery.selectResource(u, undefined) ?? null })) });
}
for (let i = 0; i < 300; i++) {
	const serverUrl = pick(URLS);
	const resource = rnd(2) ? pick(URLS) : new URL(pick(["/", "/mcp", "/mcp/", "/a", "/a/b", "/mcp/x", "", "/%6dcp", "/MCP"]), pick(URLS.filter((u) => URL.canParse(u) && /^https?:/i.test(u.trim())))).href;
	const metadata = JSON.stringify({ resource, authorization_servers: ["https://as.example"] });
	cases.resource.push({ serverUrl, metadata, ...call(() => {
		const m = JSON.parse(metadata);
		return JSON.stringify({ url: discovery.resourceUrlFromServerUrl(serverUrl).href, selected: discovery.selectResource(serverUrl, m) ?? null });
	}) });
}

// Fixed resource cases: Pi compares the percent-encoded pathnames, so an encoded and a decoded spelling of one path differ.
for (const [serverUrl, resource] of [["https://e.com/mcp", "https://e.com/%6dcp"], ["https://e.com/%6dcp/x", "https://e.com/mcp"], ["https://e.com/a%2Fb", "https://e.com/a/b"], ["https://e.com/a/b", "https://e.com/a%2Fb"], ["https://e.com/%6Dcp", "https://e.com/%6dcp"]]) {
	const metadata = JSON.stringify({ resource });
	cases.resource.push({ serverUrl, metadata, ...call(() => JSON.stringify({ url: discovery.resourceUrlFromServerUrl(serverUrl).href, selected: discovery.selectResource(serverUrl, JSON.parse(metadata)) ?? null })) });
}

// stepUpScope
const SCOPES = [undefined, "", "a", "a b", " a  b ", "b a", "a\tb\nc", "a a b", "\u00a0a", "a\u00a0b", "é 😀", "a  ", "read write", "write read admin"];
for (let i = 0; i < 120; i++) {
	const granted = pick(SCOPES);
	const challenged = pick(SCOPES);
	cases.stepUp.push({ granted: granted ?? null, challenged: challenged ?? null, ...call(() => stepUpScope(granted, challenged) ?? null) });
}

// Parsers: start from a valid document and mutate up to two members so most cases get past the first check.
const PU = ["https://e.com/x", "http://e.com", "javascript:x", "data:a", "vbscript:z", "nope", "", null, 5, "JAVASCRIPT:x", " javascript:x", "ftp://e.com", "file:///x", "https://e.com/é"];
const SA = [["a"], [], [1], null, "a", ["a", ""], ["a", null], ["a", "b"]];
const NUMS = [60, "60", 0, -5, 1.5, "abc", "", null, true, false, [], {}, "1e3", " 12 ", "0x10", "Infinity", [5], ["7"], "  ", 1e21];
const TS = ["abc", "", null, 5, "Bearer", "é"];
const BOOLS = [true, false, "true", 1, null];
const nonObjects = [null, "x", [], 1, true];
function mutate(base, options) {
	const o = { ...base };
	for (let n = rnd(3); n > 0; n--) {
		const key = pick(Object.keys(options));
		if (rnd(4) === 0) delete o[key];
		else o[key] = pick(options[key]);
	}
	return o;
}
function parserCases(list, parse, base, options) {
	for (let i = 0; i < 220; i++) {
		const value = rnd(25) === 0 ? pick(nonObjects) : mutate(base, options);
		const t = JSON.stringify(value);
		list.push({ value: t, ...call(() => JSON.stringify(parse(JSON.parse(t)))) });
	}
}
parserCases(cases.metadata, types.parseProtectedResourceMetadata,
	{ resource: "https://e.com/x", authorization_servers: ["https://as.example"], scopes_supported: ["a"], extra: 1 },
	{ resource: PU, authorization_servers: [[pick(PU)], [pick(PU), "https://b.example"], ...SA], scopes_supported: SA, extra: [1, null, { k: 1 }], resource_name: STR });
parserCases(cases.authServer, types.parseAuthorizationServerMetadata,
	{ issuer: "https://as.example", authorization_endpoint: "https://as.example/a", token_endpoint: "https://as.example/t", response_types_supported: ["code"] },
	{ issuer: PU, authorization_endpoint: PU, token_endpoint: PU, registration_endpoint: PU, response_types_supported: SA, scopes_supported: SA, grant_types_supported: SA,
		token_endpoint_auth_methods_supported: SA, code_challenge_methods_supported: SA, client_id_metadata_document_supported: BOOLS, authorization_response_iss_parameter_supported: BOOLS, extra: [1, { a: 1 }] });
parserCases(cases.tokens, types.parseOAuthTokens,
	{ access_token: "tok", token_type: "Bearer" },
	{ access_token: TS, token_type: TS, expires_in: NUMS, scope: TS, refresh_token: TS, id_token: TS, extra: [1] });
for (const t of ['{"access_token":"a","token_type":"b","expires_in":1e400}', '{"access_token":"a","token_type":"b","expires_in":"1e400"}', '{"access_token":"a","token_type":"b","expires_in":-0}', '{"access_token":"a","token_type":"b","expires_in":0.1}'])
	cases.tokens.push({ value: t, ...call(() => JSON.stringify(types.parseOAuthTokens(JSON.parse(t)))) });
parserCases(cases.client, types.parseClientInformation,
	{ client_id: "cid", redirect_uris: ["http://127.0.0.1/cb"] },
	{ client_id: TS, client_secret: TS, client_id_issued_at: [1, "1", 0, 1.5, null, 1e21], client_secret_expires_at: [1, "1", 0, null], redirect_uris: SA, client_name: STR, application_type: ["web", "native", 5] });

// Fixed parser cases: JavaScript reads members by their exact names, so a member that differs only in case, of any type,
// is an unmodeled member.
for (const [list, parse, t] of [
	[cases.metadata, types.parseProtectedResourceMetadata, '{"resource":"https://e.com/x","RESOURCE":"y","Scopes_Supported":5,"Authorization_Servers":[1]}'],
	[cases.authServer, types.parseAuthorizationServerMetadata, '{"issuer":"https://a.example","authorization_endpoint":"https://a.example/a","token_endpoint":"https://a.example/t","response_types_supported":["code"],"ISSUER":"q","Grant_Types_Supported":7}'],
	[cases.client, types.parseClientInformation, '{"client_id":"c","Client_Name":5,"CLIENT_ID":7,"contacts":"bad","token_endpoint_auth_method":"none","grant_types":["a",5]}'],
]) list.push({ value: t, ...call(() => JSON.stringify(parse(JSON.parse(t)))) });

writeSync(1, `${JSON.stringify(cases, null, "\t").replace(/\n\t\t\t/g, " ").replace(/\n\t\t\}/g, " }")}\n`);
