import { createRequire } from "node:module";
import * as path from "node:path";
import { getNativeModuleCandidates } from "./native-module-path.js";
const cjsRequire = createRequire(import.meta.url);
// Cache module loading, not display availability: a disconnected display can recover.
const helpers = new Map();
function loadNativePlatformHelper(platform, suffix = "") {
    const arch = process.arch;
    if (arch !== "x64" && arch !== "arm64")
        return undefined;
    const nativePath = path.join("native", platform, "prebuilds", `${platform}-${arch}`, `${platform}-platform${suffix}.node`);
    if (helpers.has(nativePath))
        return helpers.get(nativePath);
    for (const modulePath of getNativeModuleCandidates(nativePath)) {
        try {
            const helper = cjsRequire(modulePath);
            if (typeof helper?.getText === "function" && typeof helper.getImage === "function") {
                helpers.set(nativePath, helper);
                return helper;
            }
        }
        catch {
            // Try the next possible packaging location.
        }
    }
    helpers.set(nativePath, undefined);
    return undefined;
}
export function getNativePlatformHelper() {
    if (process.platform !== "darwin" && process.platform !== "win32")
        return undefined;
    return loadNativePlatformHelper(process.platform);
}
/** Load a clipboard helper without opening the display until a read is requested. */
export function getNativeClipboard() {
    if (process.platform !== "linux")
        return getNativePlatformHelper();
    if (!process.env.DISPLAY)
        return undefined;
    return loadNativePlatformHelper("linux", "-x11");
}
//# sourceMappingURL=native-platform.js.map