import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
const moduleRequire = createRequire(import.meta.url);
const TUI_PACKAGE_NAME = "@earendil-works/pi-tui";
export function getNativeModuleCandidates(nativePath, options = {}) {
    const moduleDir = dirname(fileURLToPath(options.moduleUrl ?? import.meta.url));
    const candidates = [];
    try {
        const packageEntry = (options.resolvePackage ?? moduleRequire.resolve)(TUI_PACKAGE_NAME);
        candidates.push(join(dirname(packageEntry), "..", nativePath));
    }
    catch {
        // Standalone binaries do not have an installed TUI package.
    }
    candidates.push(join(moduleDir, "..", nativePath), join(moduleDir, nativePath), join(dirname(options.execPath ?? process.execPath), nativePath));
    return Array.from(new Set(candidates));
}
//# sourceMappingURL=native-module-path.js.map