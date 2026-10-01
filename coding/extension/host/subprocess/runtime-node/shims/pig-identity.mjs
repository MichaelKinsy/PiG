// PiG's product identity for the Pi modules the runtime vendors (D26, D65).
// automation/gen/pi-identity-patches.mjs points Pi's getPiUserAgent here.
import { release } from "node:os";

// pigUserAgent returns the user agent the Go host sends (ai.PiUserAgent):
// "<product>/<version> (<platform> <release>; <arch>)". The host names its
// composite release version in PIG_PRODUCT_VERSION when it starts this process.
export function pigUserAgent(product) {
  return `${product}/${process.env.PIG_PRODUCT_VERSION ?? "unknown"} (${process.platform} ${release()}; ${process.arch})`;
}
