// provider-hooks-log.mjs records each provider request event an extension can observe, in the order they fire.
import { appendFileSync } from "node:fs";

const log = (event) => appendFileSync(process.env.WIRING_REPORT, event + "\n");

export default function providerHooksLog(pi) {
  pi.on("before_provider_headers", () => log("before_provider_headers"));
  pi.on("before_provider_request", () => log("before_provider_request"));
  pi.on("after_provider_response", () => log("after_provider_response"));
  pi.on("provider_stream_event", () => log("provider_stream_event"));
}
