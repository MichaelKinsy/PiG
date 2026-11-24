import "./chunk-UVW3US7A.js";
import "./chunk-EDPW3NGU.js";
import "./chunk-TA77CKFD.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/api/pi-messages.js
import { bridgeApi } from "../../../pi-ai-bridge.mjs";

// pi-dist/pi-ai/api/pi-messages.impl.js
import { AssistantMessageEventStream } from "../utils/event-stream.js";
import { getProviderEnvValue } from "../utils/provider-env.js";
var PiMessagesResponseError = class extends Error {
  static {
    __name(this, "PiMessagesResponseError");
  }
  code;
  diagnosticDetails;
  constructor(message, code, diagnosticDetails) {
    super(message);
    this.name = "PiMessagesResponseError";
    this.code = code;
    this.diagnosticDetails = diagnosticDetails;
  }
};

// pi-dist/pi-ai/api/pi-messages.js
var { stream, streamSimple } = bridgeApi("pi-messages");
export {
  PiMessagesResponseError,
  stream,
  streamSimple
};
