// Drive the installed pi-ai OpenAI OAuth logins up to the authorization URL, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const oauth = root + "node_modules/@earendil-works/pi-ai/dist/auth/oauth/";
const { openaiChatGPTOAuth } = await import(pathToFileURL(oauth + "openai-chatgpt.js"));
const { openaiCodexOAuth } = await import(pathToFileURL(oauth + "openai-codex.js"));
const providers = { chatgpt: openaiChatGPTOAuth, codex: openaiCodexOAuth };
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const test of JSON.parse(input)) {
  const controller = new AbortController();
  let url = null;
  const interaction = {
    signal: controller.signal,
    notify(event) {
      if (event.type === "auth_url" && url === null) url = event.url;
    },
    // The first select option is the browser login; an empty pasted code then ends the login with an error right after the authorization URL was announced.
    prompt: async (request) => (request.type === "select" ? request.options[0].id : ""),
  };
  const options = { getDeviceId: () => "0b1f9c3e-5a47-4a28-9d0f-6a1d2c3b4e5f" };
  if (test.agentName !== null) options.agentName = test.agentName;
  let failure; await providers[test.provider].login(interaction, options).catch((error) => { failure = String(error); });
  if (url === null) throw new Error(`no authorization URL for ${test.provider}: ${failure}`);
  const params = new URL(url).searchParams;
  results.push({ hint: params.get("agent_name_hint"), originator: params.get("originator") });
}
process.stdout.write(JSON.stringify(results));
