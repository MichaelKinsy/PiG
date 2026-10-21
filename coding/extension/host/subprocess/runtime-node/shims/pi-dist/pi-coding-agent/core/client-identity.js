import { PI_ATTRIBUTION } from "../../pi-ai/sdk-bundle/index.js";
import { VERSION } from "../config.js";
import { isInstallTelemetryEnabled } from "./telemetry.js";
/**
 * How pi's requests identify it to providers: a detailed `User-Agent` with pi's version, and attribution to pi as a
 * CLI agent while install telemetry is enabled.
 */
export function getClientIdentity(settingsManager) {
    return {
        userAgent: "detailed",
// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
        version: process.env.PIG_PRODUCT_VERSION ?? VERSION,
        attribution: isInstallTelemetryEnabled(settingsManager) ? PI_ATTRIBUTION : null,
    };
}
//# sourceMappingURL=client-identity.js.map