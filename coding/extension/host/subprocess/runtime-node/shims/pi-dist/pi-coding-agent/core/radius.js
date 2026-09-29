import { DEFAULT_RADIUS_GATEWAY, normalizeRadiusGatewayUrl } from "../../pi-ai/providers/radius-config.js";
export const RADIUS_PROVIDER_ID = "radius";
export const ENV_RADIUS_GATEWAY = "PI_RADIUS_GATEWAY";
/** Radius gateway origin, honoring the `PI_RADIUS_GATEWAY` override. */
export function getRadiusGatewayUrl() {
    return normalizeRadiusGatewayUrl(process.env[ENV_RADIUS_GATEWAY] ?? DEFAULT_RADIUS_GATEWAY);
}
//# sourceMappingURL=radius.js.map