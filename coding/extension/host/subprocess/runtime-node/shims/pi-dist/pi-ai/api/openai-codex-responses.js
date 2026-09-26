// PiG: the openai-codex-responses implementation runs in PiG's host (D74); see automation/gen/vendor-pi-dist.sh.
import { bridgeApi } from "../../../pi-ai-bridge.mjs";
export const { stream, streamSimple } = bridgeApi("openai-codex-responses");
