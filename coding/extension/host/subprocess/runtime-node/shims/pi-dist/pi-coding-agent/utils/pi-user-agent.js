// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.
import { pigUserAgent } from "../../../pig-identity.mjs";
export function getPiUserAgent(version) {
    return pigUserAgent("pig");
}
//# sourceMappingURL=pi-user-agent.js.map