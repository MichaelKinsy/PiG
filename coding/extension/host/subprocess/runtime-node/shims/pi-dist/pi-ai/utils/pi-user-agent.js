function loadNodeOs() {
    if (typeof process === "undefined" || !(process.versions?.node || process.versions?.bun)) {
        return null;
    }
    return process.getBuiltinModule?.("node:os") ?? null;
}
// Keep runtime OS loading browser-safe. A top-level runtime import of node:os breaks browser/Vite builds.
const nodeOs = loadNodeOs();
// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
import { pigUserAgent } from "../../../pig-identity.mjs";
export function getPiUserAgent() {
    return pigUserAgent("pig");
}
//# sourceMappingURL=pi-user-agent.js.map