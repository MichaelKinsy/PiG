// PiG's product identity for the Pi modules the runtime vendors (D26, D65).
// automation/gen/pi-identity-patches.mjs points Pi's formatUserAgent here.
import { release } from "node:os";

// pigUserAgent returns the detailed user agent the Go host sends (ai.FormatUserAgent):
// "<product>/<version> (<platform> <release>; <arch>)". An identity's version wins; otherwise
// the host's composite release version, named in PIG_PRODUCT_VERSION when it starts this process.
export function pigUserAgent(product, version) {
  return `${product}/${version ?? process.env.PIG_PRODUCT_VERSION ?? "unknown"} (${process.platform} ${release()}; ${process.arch})`;
}
