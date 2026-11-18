// PiG (D74): the `openai` package is not shipped to extension processes. api/openai-completions.impl.js is Pi's module with its one import of the SDK pointed here:
// its pure values (convertMessages) work, and the request code, which alone constructs the client, runs in PiG's host through the bridge (api/openai-completions.js).
export default class OpenAI {
  constructor() {
    throw new Error("The openai SDK is not available in an extension process: PiG runs the openai-completions requests in its host (D74).");
  }
}
