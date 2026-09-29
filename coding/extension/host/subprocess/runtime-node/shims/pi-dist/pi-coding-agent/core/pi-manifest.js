import { readFileSync } from "node:fs";
import { stripBom } from "../utils/text.js";
const RESOURCE_FIELDS = ["extensions", "skills", "prompts", "themes"];
function isObject(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
export function readPiManifest(packageJsonPath) {
    try {
        const pkg = JSON.parse(stripBom(readFileSync(packageJsonPath, "utf-8")));
        if (!isObject(pkg) || !isObject(pkg.pi)) {
            return null;
        }
        const manifest = {};
        for (const field of RESOURCE_FIELDS) {
            const entries = pkg.pi[field];
            if (Array.isArray(entries) && entries.every((entry) => typeof entry === "string")) {
                manifest[field] = entries;
            }
        }
        return manifest;
    }
    catch {
        return null;
    }
}
//# sourceMappingURL=pi-manifest.js.map