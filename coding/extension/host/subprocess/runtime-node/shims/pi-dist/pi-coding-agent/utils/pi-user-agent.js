import { formatUserAgent } from "../../pi-ai/sdk-bundle/index.js";
/** pi's `User-Agent` for its own network requests; the same detailed form pi-ai sends to providers. */
export function getPiUserAgent(version) {
// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
    return formatUserAgent({ userAgent: "detailed" });
}
//# sourceMappingURL=pi-user-agent.js.map