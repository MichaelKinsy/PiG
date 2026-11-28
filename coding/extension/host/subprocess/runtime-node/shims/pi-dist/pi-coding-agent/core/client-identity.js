import { PI_ATTRIBUTION } from "../../pi-ai/sdk-bundle/index.js";
import { VERSION } from "../config.js";
/**
 * How pi's requests identify it to providers: a detailed `User-Agent` with pi's version, and attribution to pi as a
 * CLI agent.
 */
export function getClientIdentity() {
    return {
        userAgent: "detailed",
// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
        version: process.env.PIG_PRODUCT_VERSION ?? VERSION,
        attribution: PI_ATTRIBUTION,
    };
}
//# sourceMappingURL=client-identity.js.map