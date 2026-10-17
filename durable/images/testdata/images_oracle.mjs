// Run Pi's Photon image processor (packages/durable/src/images/index.ts, imported as-is with types stripped) over cases from stdin.
// Case: {bytes: base64, mimeType, limits: {maxWidth, maxHeight, maxBytes}}. stdout: per case null (cannot be prepared) or {data, mimeType, resized?, convertedFrom?}.
// usage: node images_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @silvia-odwyer/photon)
import { readFileSync } from "node:fs";
import module from "node:module";
import { pathToFileURL } from "node:url";
const packages = process.argv[2].replace(/\/?$/, "/");
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier.startsWith("@silvia-odwyer/photon/")) return next(specifier, { ...context, parentURL: pathToFileURL(interop).href });
		return next(specifier, context);
	},
});
const { createPhotonImages } = await import(pathToFileURL(`${packages}durable/src/images/index.ts`).href);
const wasm = readFileSync(`${interop}node_modules/@silvia-odwyer/photon/photon_rs_bg.wasm`);
const images = createPhotonImages(wasm);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const c of JSON.parse(input)) {
	const prepared = await images.prepare(Uint8Array.from(Buffer.from(c.bytes, "base64")), c.mimeType, c.limits);
	out.push(prepared === undefined ? null : prepared);
}
process.stdout.write(JSON.stringify(out));
