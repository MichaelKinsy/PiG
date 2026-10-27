// Module hooks for oracle.mjs: Pi's cloudflare.ts imports two modules that exist only inside a Worker built by Wrangler.
export async function resolve(specifier, context, next) {
	if (specifier === "cloudflare:workers") return { url: "pig-stub:cloudflare-workers", shortCircuit: true };
	if (specifier === "quickjs-wasi/quickjs.wasm") return { url: "pig-stub:quickjs-wasm", shortCircuit: true };
	return next(specifier, context);
}

export async function load(url, context, next) {
	// What Workers provide: the base class, whose constructor keeps its context and env.
	if (url === "pig-stub:cloudflare-workers") {
		return { format: "module", source: "export class DurableObject { constructor(ctx, env) { this.ctx = ctx; this.env = env; } }", shortCircuit: true };
	}
	// What Wrangler's CompiledWasm rule does: the import is a compiled WebAssembly.Module.
	if (url === "pig-stub:quickjs-wasm") {
		return {
			format: "module",
			source: `import { readFileSync } from "node:fs"; export default new WebAssembly.Module(readFileSync(process.env.PI_QUICKJS_WASM));`,
			shortCircuit: true,
		};
	}
	return next(url, context);
}
