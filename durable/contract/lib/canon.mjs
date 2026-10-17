// Canonical JSON and the `ctx` fingerprint (CONTRACT section 5.4): sha256 of the canonical JSON of the pi-ai Context and the
// JSON-visible stream options of a model call. Pi's side wraps `models.streamSimple`; the candidate's side hashes the
// model_context effect (rebuilt to the full form when the core sent the delta form). Both call this function.
import { createHash } from "node:crypto"

export function canonical(value) {
	if (value === null || typeof value !== "object") return JSON.stringify(value) ?? "null"
	if (Array.isArray(value)) return `[${value.map(v => canonical(v === undefined ? null : v)).join(",")}]`
	const parts = []
	for (const key of Object.keys(value).sort()) {
		const v = value[key]
		if (v === undefined || typeof v === "function") continue
		parts.push(`${JSON.stringify(key)}:${canonical(v)}`)
	}
	return `{${parts.join(",")}}`
}

/** Stream options as the contract sees them: everything JSON can carry except the abort signal. */
export function visibleOptions(options) {
	if (!options || typeof options !== "object") return {}
	const { signal: _signal, ...rest } = options
	return JSON.parse(JSON.stringify(rest))
}

export const ctxFingerprint = (context, options) => createHash("sha256").update(canonical({ context, options: visibleOptions(options) })).digest("hex")
